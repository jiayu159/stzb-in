package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"stzbHelper/global"
	"stzbHelper/model"
)

// Supabase(PostgreSQL) 云数据库增量同步器
// 配置: exe 同目录 supabase.json {"host":"aws-0-xx.pooler.supabase.com","port":6543,"user":"postgres.xxx","password":"...","dbname":"postgres","sslmode":"require"}，文件缺失时同步禁用
// 多盟共库: 云端三张表统一在最前有 alliance 列(TEXT NOT NULL DEFAULT '')，主键为 (alliance, 本地主键)；
// 联盟标识优先取配置文件可选字段 alliance；未配置时从本地数据库自动识别同盟名(同 app resolveMyUnion)；
// 识别不到才回落 "default"，互不覆盖
// 原理: 每张表按主键游标增量读取本地记录，通过 pgx 批量 INSERT ... ON CONFLICT 到 Supabase

type supabaseConfig struct {
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	Password  string `json:"password"`
	DBName    string `json:"dbname"`
	SSLMode   string `json:"sslmode"`
	Alliance  string `json:"alliance,omitempty"` // 可选: 联盟标识(多盟共库时区分数据)，为空时回落 "default"
}

// syncAlliance 返回本机同步数据的联盟标识：配置文件 alliance > 本地自动识别 > "default"
// (不配置也识别不到时回落 "default"，避免空值撞上旧数据(旧行 alliance=''))
func syncAlliance() string {
	syncMu.Lock()
	defer syncMu.Unlock()
	if syncCfg.Alliance != "" {
		return syncCfg.Alliance
	}
	if autoAlliance != "" {
		return autoAlliance
	}
	return "default"
}

// autoAlliance 从本地数据库自动识别的联盟名(多盟共库时区分数据)；配置文件未指定 alliance 时使用。
// 每轮同步前由 refreshAutoAlliance 刷新，识别失败保留上次有效值
var autoAlliance string

// detectAllianceFromDB 从当前本地库推断同盟名(app 端 resolveMyUnion 同源 SQL，仅读本地库，不发网络请求)。
// 返回空串表示未能识别
func detectAllianceFromDB() string {
	if model.Conn == nil {
		return ""
	}
	var union string
	model.Conn.Raw(`SELECT attack_union_name FROM battle_report
		WHERE attack_name IN (SELECT name FROM team_user WHERE name != '')
		AND attack_union_name != '' AND attack_union_name != defend_union_name
		GROUP BY attack_union_name ORDER BY COUNT(*) DESC LIMIT 1`).Scan(&union)
	if union == "" {
		model.Conn.Raw(`SELECT defend_union_name FROM battle_report
			WHERE defend_name IN (SELECT name FROM team_user WHERE name != '')
			AND defend_union_name != '' AND defend_union_name != attack_union_name
			GROUP BY defend_union_name ORDER BY COUNT(*) DESC LIMIT 1`).Scan(&union)
	}
	if union == "" {
		model.Conn.Raw(`SELECT attack_union_name FROM battle_report
			WHERE attack_union_name != ''
			GROUP BY attack_union_name ORDER BY COUNT(*) DESC LIMIT 1`).Scan(&union)
	}
	return union
}

// refreshAutoAlliance 每轮同步前刷新自动识别结果；识别为空时保留旧值，
// 避免临时识别不到导致联盟标识漂移
func refreshAutoAlliance() {
	v := detectAllianceFromDB()
	if v == "" {
		return
	}
	syncMu.Lock()
	defer syncMu.Unlock()
	autoAlliance = v
	log.Printf("同步器: 自动识别同盟联盟=%s", v)
}

type syncTable struct {
	Name       string
	PkColumn   string
	ReplaceAll bool // true=整表覆盖(如成员表，与本地全量替换语义一致)
	RecentFix  int64 // >0 时手动推送最后执行一次"最新N条分批补齐"（每批100条），用于补游标区间空洞
}

var syncTables = []syncTable{
	{Name: "battle_report", PkColumn: "battle_id", RecentFix: 3000},
	{Name: "reports", PkColumn: "battle_id"},
	{Name: "team_user", PkColumn: "id", ReplaceAll: true}, // 按 name 刷新: 只删本盟(同 alliance)本批名字，云端他人/他盟记录保留(并集)
}

type syncStatus struct {
	Enabled bool  `json:"enabled"`
	LastRun int64 `json:"last_run"`
	LastErr string `json:"last_err"`
}

var (
	syncMu         sync.Mutex
	syncCfg        supabaseConfig
	pgPool         *pgxpool.Pool
	syncEnabled    bool
	syncConfigOK   bool // supabase.json 存在且 host/user/password 完整(与是否连上云端/是否打开数据库无关)，供前端展示
	syncLastRun    int64
	syncLastErr    string
	syncNotifyCh   = make(chan struct{}, 1)
	syncRunning    bool
	syncWaitingDB  bool // 等待数据库打开(临时)，由 StartSyncLoop 循环重试 initSync
)

func initSync() {
	// 读取 exe 同目录外部 supabase.json(便于换库/换密码)，缺失则云同步禁用
	var data []byte
	exePath, err := os.Executable()
	if err == nil {
		cfgPath := filepath.Join(filepath.Dir(exePath), "supabase.json")
		data, err = os.ReadFile(cfgPath)
		if err != nil {
			log.Println("同步器: 未找到外部 supabase.json，云同步禁用")
		}
	}
	if len(data) == 0 {
		log.Println("同步器: 无 supabase 配置，云同步禁用")
		syncConfigOK = false
		syncWaitingDB = false
		return
	}
	var cfg supabaseConfig
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.Host == "" || cfg.User == "" || cfg.Password == "" {
		log.Println("同步器: supabase.json 格式错误(host/user/password 不能为空)，云同步禁用")
		syncConfigOK = false
		syncWaitingDB = false
		return
	}
	syncCfg = cfg
	syncConfigOK = true
	if syncCfg.Port == 0 {
		syncCfg.Port = 6543 // Supavisor 连接池默认端口(支持 IPv4)
	}
	if syncCfg.DBName == "" {
		syncCfg.DBName = "postgres"
	}
	if syncCfg.SSLMode == "" {
		syncCfg.SSLMode = "require"
	}
	if err := initPGPool(); err != nil {
		log.Printf("同步器: 连接 Supabase 失败: %v，云同步禁用", err)
		syncWaitingDB = false
		return
	}
	// 数据库绑定校验已解除: 不再校验库名，允许任意本地库同步到云端
	if global.CurrentDbName == "" {
		// 数据库还没打开，属于临时状态: 由 StartSyncLoop 循环重试 initSync
		syncWaitingDB = true
		log.Println("同步器: 数据库未打开，云同步等待")
		return
	}
	syncWaitingDB = false
	syncEnabled = true
	log.Printf("同步器: 已启用，目标 %s:%d/%s, 联盟=%s", syncCfg.Host, syncCfg.Port, syncCfg.DBName, syncAlliance())
}

// initPGPool 初始化 Supabase 的 PostgreSQL 连接池(Supavisor 连接池, IPv4 可达)。
// 连接失败视为配置不可用，云同步禁用；改动配置后重启应用即可重试
func initPGPool() error {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		syncCfg.Host, syncCfg.Port, syncCfg.User, syncCfg.Password, syncCfg.DBName, syncCfg.SSLMode)
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("解析连接串失败: %w", err)
	}
	poolCfg.MaxConns = 4
	poolCfg.MinConns = 1
	poolCfg.MaxConnLifetime = time.Hour
	poolCfg.ConnConfig.ConnectTimeout = 20 * time.Second
	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return err
	}
	if pgPool != nil {
		pgPool.Close()
	}
	pgPool = pool
	return nil
}

// 每张表上次成功同步时的本地数据指纹(MAX(pk):COUNT)，用于推送前检测本地是否有变化
var syncFp = map[string]string{}

// dataFingerprint 计算单张表本地数据指纹。battle_report/reports 用 MAX(battle_id):COUNT，能识别新增/删除；
// team_user 用 MAX(id):COUNT，识别成员数量或最大 id 变化。均只查本地库，不产生云端请求
func dataFingerprint(t syncTable) string {
	var maxID int64
	var cnt int64
	model.Conn.Raw(
		fmt.Sprintf("SELECT COALESCE(MAX(%s),0), COUNT(*) FROM %s", t.PkColumn, t.Name),
	).Row().Scan(&maxID, &cnt)
	return fmt.Sprintf("%d:%d", maxID, cnt)
}

// localChanged 检测本地数据是否与上次成功同步后发生变化。从未成功同步过视为有变化(必须推送)
func localChanged() bool {
	for _, t := range syncTables {
		fp := dataFingerprint(t)
		if last, ok := syncFp[t.Name]; !ok || last != fp {
			return true
		}
	}
	return false
}

// markSynced 每张表全部同步成功后记录其指纹，供下一轮变化检测使用
func markSynced() {
	for _, t := range syncTables {
		syncFp[t.Name] = dataFingerprint(t)
	}
}

// StartSyncLoop 后台同步循环: 数据库未打开时每 5 秒重试初始化，就绪后同步一次，之后每 30 秒或收到入库信号时同步
func StartSyncLoop() {
	for {
		initSync()
		if syncEnabled {
			break
		}
		if !syncWaitingDB {
			return // 永久禁用(无配置/格式错/库名不匹配)
		}
		time.Sleep(5 * time.Second)
	}
	if model.Conn == nil {
		log.Println("同步器: 数据库未连接，等待")
		time.Sleep(5 * time.Second)
	}
	go func() {
		for {
			syncOnce()
			select {
			case <-syncNotifyCh:
			case <-time.After(30 * time.Second):
			}
		}
	}()
}

// NotifySync 数据入库后触发一次同步（非阻塞）
func NotifySync() {
	select {
	case syncNotifyCh <- struct{}{}:
	default:
	}
}

// syncOnce 同步一轮所有表
func syncOnce() {
	syncMu.Lock()
	if syncRunning {
		syncMu.Unlock()
		return
	}
	syncRunning = true
	syncMu.Unlock()
	defer func() {
		syncMu.Lock()
		syncRunning = false
		syncMu.Unlock()
	}()

	if !syncEnabled || model.Conn == nil {
		return
	}

	// 每轮同步前刷新自动识别同盟名(识别失败保留旧值)
	refreshAutoAlliance()

	// 推送前先检测本地数据是否有变化，没有变化则跳过本轮推送，避免频繁请求浪费云端资源
	if !localChanged() {
		log.Println("同步器: 本地数据无变化，跳过本轮推送")
		return
	}

	ensureSyncTables()
	if err := ensureCloudSchema(); err != nil {
		log.Printf("同步器: 云端建表失败: %v", err)
		syncMu.Lock()
		syncLastErr = fmt.Sprintf("云端建表失败: %v", err)
		syncMu.Unlock()
		// 作废指纹，下一轮即使本地无变化也会重试
		syncFp = map[string]string{}
		return
	}

	for _, t := range syncTables {
		if _, err := syncTableDelta(t); err != nil {
			syncMu.Lock()
			syncLastErr = fmt.Sprintf("%s: %v", t.Name, err)
			syncMu.Unlock()
			log.Printf("同步器: 表 %s 同步失败: %v", t.Name, err)
			// 作废该表指纹，下一轮重试
			delete(syncFp, t.Name)
			return
		}
	}

	syncMu.Lock()
	syncLastRun = time.Now().Unix()
	syncLastErr = ""
	syncMu.Unlock()
	markSynced()
	log.Println("同步器: 本轮同步完成")
}

// syncResult 单张表的同步结果
type syncResult struct {
	Table  string `json:"table"`
	Status string `json:"status"` // ok / fail
	Count  int64  `json:"count"`
	Err    string `json:"err"`
}

// syncOnceDetailed 手动推送用的逐表同步：每张表都尝试并记录成功/失败明细，失败不中断后续表
func syncOnceDetailed() []syncResult {
	syncMu.Lock()
	if syncRunning {
		syncMu.Unlock()
		return nil
	}
	syncRunning = true
	syncMu.Unlock()
	defer func() {
		syncMu.Lock()
		syncRunning = false
		syncMu.Unlock()
	}()

	var results []syncResult
	if !syncEnabled || model.Conn == nil {
		return results
	}

	// 手动推送同样先刷新自动识别同盟名
	refreshAutoAlliance()

	ensureSyncTables()
	if err := ensureCloudSchema(); err != nil {
		results = append(results, syncResult{Table: "云端建表", Status: "fail", Err: err.Error()})
		syncMu.Lock()
		syncLastErr = err.Error()
		syncMu.Unlock()
		log.Printf("同步器: 手动推送 云端建表失败: %v", err)
		syncFp = map[string]string{}
		return results
	}

	for _, t := range syncTables {
		count, err := syncTableDelta(t)
		if err != nil {
			results = append(results, syncResult{Table: t.Name, Status: "fail", Err: err.Error()})
			syncMu.Lock()
			syncLastErr = fmt.Sprintf("%s: %v", t.Name, err)
			syncMu.Unlock()
			log.Printf("同步器: 手动推送 表 %s 推送失败: %v", t.Name, err)
			delete(syncFp, t.Name)
			continue
		}
		results = append(results, syncResult{Table: t.Name, Status: "ok", Count: count})
		log.Printf("同步器: 手动推送 表 %s 推送成功 %d 条", t.Name, count)

		// 增量推完后，若配置了 RecentFix(>0)，再执行一次"最新N条分批补齐"（每批100条），
		// 用于补游标区间内的历史空洞；日志按第 x/y 批输出
		if t.RecentFix > 0 {
			fixPushed, fixFailed, _, _, _, fixMsgs, fixErr := pushRecentBatches(t.RecentFix)
			if fixErr != nil {
				results = append(results, syncResult{Table: t.Name + "(分批补齐)", Status: "fail", Err: fixErr.Error()})
				syncMu.Lock()
				syncLastErr = fmt.Sprintf("%s 分批补齐失败: %v", t.Name, fixErr)
				syncMu.Unlock()
				log.Printf("同步器: 手动推送 表 %s 分批补齐失败: %v", t.Name, fixErr)
				delete(syncFp, t.Name)
				continue
			}
			if fixFailed > 0 {
				msg := strings.Join(fixMsgs, ";")
				results = append(results, syncResult{Table: t.Name + "(分批补齐)", Status: "fail", Count: fixPushed, Err: msg})
				syncMu.Lock()
				syncLastErr = fmt.Sprintf("%s 分批补齐: 成功%d 失败%d", t.Name, fixPushed, fixFailed)
				syncMu.Unlock()
				log.Printf("同步器: 手动推送 表 %s 分批补齐: 成功 %d 失败 %d", t.Name, fixPushed, fixFailed)
				delete(syncFp, t.Name)
				continue
			}
			// 把分批补齐成功数并入该表结果
			for i := range results {
				if results[i].Table == t.Name && results[i].Status == "ok" {
					results[i].Count += fixPushed
				}
			}
			log.Printf("同步器: 手动推送 表 %s 分批补齐完成，共补 %d 条", t.Name, fixPushed)
		}
	}

	syncMu.Lock()
	syncLastRun = time.Now().Unix()
	allOK := true
	for _, r := range results {
		if r.Status != "ok" {
			allOK = false
			break
		}
	}
	if allOK {
		syncLastErr = ""
		markSynced()
		log.Println("同步器: 手动推送 本轮全部完成")
	}
	syncMu.Unlock()
	return results
}

// syncTableDelta 同步单张表。ReplaceAll 表按 name 刷新(只删本地这批名字的云端旧记录，保留其他机器同步的成员)，
// 其余按主键增量，失败不推进游标。返回成功条数；失败时错误信息包含具体数据(主键)与原因
func syncTableDelta(t syncTable) (int64, error) {
	var total int64
	var lastID int64
	if t.ReplaceAll {
		// 从本地读出全部成员名，云端仅删除本盟(同 alliance)这些名字的旧记录(分批)，避免本地是云端子集时整表覆盖丢失他人数据
		allianceLit := sqlLiteral(syncAlliance())
		var names []string
		model.Conn.Model(&model.TeamUser{}).Where("name != ''").Pluck("name", &names)
		for i := 0; i < len(names); i += 500 {
			end := i + 500
			if end > len(names) {
				end = len(names)
			}
			var lits []string
			for _, n := range names[i:end] {
				lits = append(lits, sqlLiteral(n))
			}
			if err := pgExec("DELETE FROM team_user WHERE alliance = " + allianceLit + " AND name IN (" + strings.Join(lits, ",") + ")"); err != nil {
				return 0, fmt.Errorf("按 name 刷新 %d 个成员失败: %v", len(names), err)
			}
		}
		// 同步退盟删除：云端本盟还有、但本地名单已没有的成员(退盟者)一并删除，
		// 与本地 parseTeamUser 的"保存最新全量+删除不在名单成员"语义一致，云端只保留当前在盟成员
		cloudRows, err := pgQueryRows("SELECT name FROM team_user WHERE name != '' AND alliance = " + allianceLit)
		if err != nil {
			return 0, fmt.Errorf("查询云端成员名单失败: %v", err)
		}
		localSet := make(map[string]struct{}, len(names))
		for _, n := range names {
			localSet[n] = struct{}{}
		}
		var stale []string
		for _, r := range cloudRows {
			if len(r) == 0 || r[0] == "" {
				continue
			}
			if _, ok := localSet[r[0]]; !ok {
				stale = append(stale, r[0])
			}
		}
		for i := 0; i < len(stale); i += 500 {
			end := i + 500
			if end > len(stale) {
				end = len(stale)
			}
			var lits []string
			for _, n := range stale[i:end] {
				lits = append(lits, sqlLiteral(n))
			}
			if err := pgExec("DELETE FROM team_user WHERE alliance = " + allianceLit + " AND name IN (" + strings.Join(lits, ",") + ")"); err != nil {
				return 0, fmt.Errorf("清理云端退盟成员失败(共%d个): %v", len(stale), err)
			}
		}
		if len(stale) > 0 {
			log.Printf("同步器: %s 清理云端退盟成员 %d 个", t.Name, len(stale))
		}
		log.Printf("同步器: %s 按 name 刷新 %d 个成员", t.Name, len(names))
		lastID = 0
		total = int64(len(names))
	} else {
		var err error
		lastID, err = getCursor(t.Name)
		if err != nil {
			return 0, fmt.Errorf("读取游标失败: %w", err)
		}
	}

	for {
		rows, err := model.Conn.Raw(
			fmt.Sprintf("SELECT * FROM %s WHERE %s > ? ORDER BY %s LIMIT 100", t.Name, t.PkColumn, t.PkColumn),
			lastID).Rows()
		if err != nil {
			return total, err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return total, err
		}
		colsQuoted := make([]string, len(cols))
		for i, c := range cols {
			colsQuoted[i] = `"` + c + `"`
		}

		var batch []string
		var ids []string
		maxID := lastID
		count := 0
		for rows.Next() {
			vals := make([]interface{}, len(cols))
			ptrs := make([]interface{}, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				return total, err
			}
			var valParts []string
			for i, v := range vals {
				valParts = append(valParts, sqlLiteral(v))
				if cols[i] == t.PkColumn {
					if n, ok := toInt64(v); ok {
						if n > maxID {
							maxID = n
						}
						ids = append(ids, strconv.FormatInt(n, 10))
					}
				}
			}
			batch = append(batch, "("+strings.Join(valParts, ",")+")")
			count++
		}
		rows.Close()
		if rows.Err() != nil {
			return total, rows.Err()
		}

		if count == 0 {
			return total, nil
		}

		sql := upsertSQL(t.Name, cols, batch, syncAlliance(), t.PkColumn)
		if err := pgExec(sql); err != nil {
			// 失败时把本批数据(主键)和云端原因一并返回，便于定位
			rangeDesc := strings.Join(ids, ",")
			if len(ids) > 20 {
				rangeDesc = strings.Join(ids[:20], ",") + fmt.Sprintf("...共%d条", len(ids))
			}
			return total, fmt.Errorf("推送本批 %d 条失败(主键: %s): %v", count, rangeDesc, err)
		}

		total += int64(count)

		// 游标推进到本批最大主键（ReplaceAll 模式无需游标）
		if !t.ReplaceAll {
			if err := setCursor(t.Name, maxID); err != nil {
				return total, fmt.Errorf("游标写入失败: %w", err)
			}
		}
		log.Printf("同步器: %s 已同步 %d 条(游标 %d)", t.Name, count, maxID)

		if count < 100 {
			return total, nil
		}
		lastID = maxID
	}
}

// pgExec 通过 pgx 执行单条 SQL 到 Supabase(PostgreSQL)
func pgExec(sql string) error {
	if pgPool == nil {
		return fmt.Errorf("Supabase 连接池未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := pgPool.Exec(ctx, sql); err != nil {
		return fmt.Errorf("Supabase 执行失败: %v", err)
	}
	return nil
}

// pgQueryRows 查询 Supabase 并返回每行各列的字符串值。用于推送前查询云端已存在的主键，
// 避免把已存在的行重复推送浪费额度；null 值返回空字符串
func pgQueryRows(sql string) ([][]string, error) {
	if pgPool == nil {
		return nil, fmt.Errorf("Supabase 连接池未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	rows, err := pgPool.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("云端查询失败: %v", err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make([]string, len(vals))
		for i, v := range vals {
			if v == nil {
				row[i] = ""
			} else {
				row[i] = fmt.Sprintf("%v", v)
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// upsertSQL 生成 PostgreSQL 兼容的批量 upsert: INSERT ... ON CONFLICT ("alliance", pk) DO UPDATE SET ...
// alliance 为当前机器配置的联盟标识；rows 的每行与 cols(本地列)一一对应，
// 云端 INSERT 列自动在最前补 alliance 列
func upsertSQL(table string, cols []string, rows []string, alliance, pk string) string {
	cloudCols := make([]string, 0, len(cols)+1)
	cloudCols = append(cloudCols, `"alliance"`)
	for _, c := range cols {
		cloudCols = append(cloudCols, `"`+c+`"`)
	}
	lit := sqlLiteral(alliance)
	cloudRows := make([]string, len(rows))
	for i, r := range rows {
		// r 形如 (v1,v2,...) -> (lit,v1,v2,...)
		cloudRows[i] = "(" + lit + "," + r[1:]
	}
	sets := make([]string, 0, len(cols))
	for _, c := range cols {
		if c == pk {
			continue
		}
		sets = append(sets, fmt.Sprintf(`"%s" = EXCLUDED."%s"`, c, c))
	}
	if len(sets) == 0 {
		return fmt.Sprintf(`INSERT INTO %s (%s) VALUES %s ON CONFLICT ("alliance", "%s") DO NOTHING`,
			table, strings.Join(cloudCols, ","), strings.Join(cloudRows, ","), pk)
	}
	return fmt.Sprintf(`INSERT INTO %s (%s) VALUES %s ON CONFLICT ("alliance", "%s") DO UPDATE SET %s`,
		table, strings.Join(cloudCols, ","), strings.Join(cloudRows, ","), pk, strings.Join(sets, ","))
}

// sqlLiteral 把数据库值转成 SQL 字面量
func sqlLiteral(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "NULL"
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "1"
		}
		return "0"
	case []byte:
		return "'" + strings.ReplaceAll(string(t), "'", "''") + "'"
	case string:
		return "'" + strings.ReplaceAll(t, "'", "''") + "'"
	default:
		return "'" + strings.ReplaceAll(fmt.Sprint(t), "'", "''") + "'"
	}
}

func toInt64(v interface{}) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case float64:
		return int64(t), true
	case []byte:
		n, err := strconv.ParseInt(string(t), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// getCursor/setCursor 游标存储在本地库 sync_cursor 表
func getCursor(table string) (int64, error) {
	var last int64
	err := model.Conn.Raw("SELECT COALESCE(MAX(last_id), 0) FROM sync_cursor WHERE table_name = ?", table).Scan(&last).Error
	return last, err
}

func setCursor(table string, id int64) error {
	return model.Conn.Exec(
		"INSERT OR REPLACE INTO sync_cursor (table_name, last_id, update_time) VALUES (?, ?, ?)",
		table, id, time.Now().Unix()).Error
}

// ensureSyncTables 确保游标表存在
func ensureSyncTables() {
	if model.Conn == nil {
		return
	}
	if err := model.Conn.Exec(
		`CREATE TABLE IF NOT EXISTS sync_cursor (
			table_name TEXT PRIMARY KEY,
			last_id INTEGER DEFAULT 0,
			update_time INTEGER DEFAULT 0
		)`).Error; err != nil {
		log.Println("同步器: 创建 sync_cursor 表失败:", err)
	}
}

// colType 归一化列类型，空类型(旧库遗留)兜底为 TEXT
func colType(t string) string {
	if strings.TrimSpace(t) == "" {
		return "TEXT"
	}
	return t
}

// sqliteToPG 本地 SQLite 表类型到 PostgreSQL 类型的映射
var sqliteToPG = map[string]string{
	"INTEGER":    "BIGINT",
	"INT":        "INTEGER",
	"BIGINT":     "BIGINT",
	"TINYINT":    "SMALLINT",
	"TEXT":       "TEXT",
	"VARCHAR":    "TEXT",
	"CHAR":       "TEXT",
	"CLOB":       "TEXT",
	"REAL":       "DOUBLE PRECISION",
	"FLOAT":      "DOUBLE PRECISION",
	"DOUBLE":     "DOUBLE PRECISION",
	"NUMERIC":    "NUMERIC",
	"DECIMAL":    "NUMERIC",
	"BOOLEAN":    "BOOLEAN",
	"DATE":       "DATE",
	"DATETIME":   "TIMESTAMP",
	"BLOB":       "BYTEA",
}

// pgType 把 SQLite 列类型映射为 PostgreSQL 类型，未知名一律 TEXT
func pgType(sqliteType string) string {
	key := strings.ToUpper(strings.TrimSpace(sqliteType))
	if t, ok := sqliteToPG[key]; ok {
		return t
	}
	return "TEXT"
}

// ensureCloudSchema 在 Supabase 按本地表结构建表(含主键)。主键缺失时用同步主键列兜底，
// 保证后续 INSERT ... ON CONFLICT (alliance, pk) 可用。
// 云端统一在最前增加 alliance 列(TEXT NOT NULL DEFAULT '')，主键升级为 (alliance, 本地主键)；
// 已存在的旧表(无 alliance 列/旧单列主键)不重建，通过 ALTER 加列 + 重建主键平滑升级，旧行保留( alliance='' )
func ensureCloudSchema() error {
	for _, t := range syncTables {
		rows, err := model.Conn.Raw(fmt.Sprintf("PRAGMA table_info(%s)", t.Name)).Rows()
		if err != nil {
			return err
		}
		var colDefs []string
		var pkCols []string
		hasPK := false
		for rows.Next() {
			var cid, notnull, pk int
			var name, ctype string
			var dflt interface{}
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				rows.Close()
				return err
			}
			colDefs = append(colDefs, fmt.Sprintf(`"%s" %s`, name, pgType(ctype)))
			if pk > 0 {
				pkCols = append(pkCols, name)
				hasPK = true
			}
		}
		rows.Close()
		if len(colDefs) == 0 {
			continue
		}
		effectivePK := pkCols
		if !hasPK {
			// 本地表缺主键时用同步主键兜底，保证 upsert 可用
			effectivePK = []string{t.PkColumn}
		}
		// 云端列 = alliance(首列) + 本地列；主键 = (alliance, 本地主键)
		colDefs = append([]string{`"alliance" TEXT NOT NULL DEFAULT ''`}, colDefs...)
		quotedPK := make([]string, len(effectivePK))
		for i, c := range effectivePK {
			quotedPK[i] = `"` + c + `"`
		}
		colDefs = append(colDefs, "PRIMARY KEY (\"alliance\","+strings.Join(quotedPK, ",")+")")
		sql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", t.Name, strings.Join(colDefs, ","))
		if err := pgExec(sql); err != nil {
			return fmt.Errorf("云端建表 %s 失败: %v", t.Name, err)
		}
		// 旧表没有 alliance 列时补列(幂等)
		if err := pgExec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS "alliance" TEXT NOT NULL DEFAULT ''`, t.Name)); err != nil {
			return fmt.Errorf("云端表 %s 补 alliance 列失败: %v", t.Name, err)
		}
		// 主键统一为 (alliance, 本地主键)
		if err := ensureCloudPK(t.Name, effectivePK...); err != nil {
			return fmt.Errorf("云端表 %s 主键升级失败: %v", t.Name, err)
		}
	}
	return nil
}

// ensureCloudPK 保证云端表主键为 ("alliance", localPK...) 复合主键(幂等)。
// 已有旧单列主键时先删除再重建，数据保留(旧行 alliance='')；
// 新表或已升级过(主键列恰好匹配)时直接跳过
func ensureCloudPK(table string, localPK ...string) error {
	rows, err := pgQueryRows(fmt.Sprintf(
		`SELECT a.attname FROM pg_index i
		 JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		 WHERE i.indrelid = %s::regclass AND i.indisprimary
		 ORDER BY array_position(i.indkey, a.attnum)`, sqlLiteral(table)))
	if err != nil {
		return err
	}
	current := make([]string, 0, len(rows))
	for _, r := range rows {
		if len(r) > 0 && r[0] != "" {
			current = append(current, r[0])
		}
	}
	expected := append([]string{"alliance"}, localPK...)
	if slices.Equal(current, expected) {
		return nil
	}
	if len(current) > 0 {
		cn, err := pgQueryRows(fmt.Sprintf(
			`SELECT conname FROM pg_constraint WHERE conrelid = %s::regclass AND contype = 'p' LIMIT 1`, sqlLiteral(table)))
		if err != nil {
			return err
		}
		if len(cn) > 0 && len(cn[0]) > 0 && cn[0][0] != "" {
			if err := pgExec(fmt.Sprintf(`ALTER TABLE %s DROP CONSTRAINT "%s"`, table, cn[0][0])); err != nil {
				return fmt.Errorf("删除旧主键 %s 失败: %v", cn[0][0], err)
			}
			log.Printf("同步器: 云端表 %s 旧主键 %s 已删除，重建复合主键 (alliance+%v)", table, cn[0][0], localPK)
		}
	}
	quoted := make([]string, len(localPK))
	for i, c := range localPK {
		quoted[i] = `"` + c + `"`
	}
	if err := pgExec(fmt.Sprintf(`ALTER TABLE %s ADD PRIMARY KEY ("alliance", %s)`, table, strings.Join(quoted, ","))); err != nil {
		return fmt.Errorf("重建复合主键失败: %v", err)
	}
	return nil
}

// GetSyncStatus 查询云同步状态
func (a *App) GetSyncStatus() string {
	syncMu.Lock()
	defer syncMu.Unlock()
	// alliance 为展示用有效联盟名: 配置指定 > 本地自动识别；都为空则前端不显示(实际推送回落 "default")
	alliance := syncCfg.Alliance
	if alliance == "" {
		alliance = autoAlliance
	}
	return global.Response{Data: map[string]interface{}{
		"enabled":    syncEnabled,
		"host":       syncCfg.Host,
		"config_ok":  syncConfigOK,
		"waiting_db": syncWaitingDB,
		"alliance":   alliance,
		"last_run":   syncLastRun,
		"last_err":   syncLastErr,
	}}.Success()
}

// ManualSync 手动强制检测配置并推送一轮数据到云端
func (a *App) ManualSync() string {
	if model.Conn == nil {
		return global.Response{Message: "数据库未连接，请先选择数据库"}.Error()
	}

	// 重新读取 supabase.json 并检查数据库状态，已禁用的配置不会自动启用
	initSync()

	syncMu.Lock()
	enabled := syncEnabled
	syncMu.Unlock()
	if !enabled {
		return global.Response{Message: "云同步未启用（supabase.json 缺失或 host/user/password 为空），无法推送"}.Error()
	}

	// 逐表执行一轮同步并收集成功/失败明细
	results := syncOnceDetailed()
	if results == nil {
		return global.Response{Message: "已有同步任务正在运行，请稍后再试"}.Error()
	}

	okCount, failCount := 0, 0
	for _, r := range results {
		if r.Status == "ok" {
			okCount++
		} else {
			failCount++
		}
	}
	log.Printf("同步器: 手动推送完成, 成功 %d 张表, 失败 %d 张表（详见上方逐表日志）", okCount, failCount)

	if failCount > 0 {
		return global.Response{
			Message: fmt.Sprintf("同步完成，成功 %d 张表、失败 %d 张表（详见运行日志）", okCount, failCount),
			Data:    results,
		}.Error()
	}
	return global.Response{Data: results}.Success()
}

// pushRecentBatches 把本地最新 count 条战报(battle_id 倒序)分 100 条一批推送到云端。
// 不走增量游标：直接按行 INSERT OR REPLACE，用于补齐游标区间内的数据空洞。
// 推送前先按 500 个 id/批查询云端已存在的 battle_id，已存在的行跳过、只推缺失行，节省云额度。
// 返回: 成功条数, 失败条数, 失败明细, 总读取条数, 最小/最大 battle_id, 致命错误(本地读取失败/云端查询失败)
func pushRecentBatches(count int64) (pushed, failed, total, minBid, maxBid int64, failMsgs []string, readErr error) {
	rows, err := model.Conn.Raw(
		"SELECT * FROM battle_report ORDER BY battle_id DESC LIMIT ?", count).Rows()
	if err != nil {
		return 0, 0, 0, 0, 0, nil, fmt.Errorf("读取本地战报失败: %v", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, 0, 0, 0, 0, nil, fmt.Errorf("读取列信息失败: %v", err)
	}
	colsQuoted := make([]string, len(cols))
	for i, c := range cols {
		colsQuoted[i] = `"` + c + `"`
	}

	var values []string
	var ids []string
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return 0, 0, 0, 0, 0, nil, fmt.Errorf("读取本地战报行失败: %v", err)
		}
		var valParts []string
		for i, v := range vals {
			valParts = append(valParts, sqlLiteral(v))
			if cols[i] == "battle_id" {
				if n, ok := toInt64(v); ok {
					if total == 0 {
						maxBid = n
					}
					minBid = n
					ids = append(ids, strconv.FormatInt(n, 10))
				}
			}
		}
		values = append(values, "("+strings.Join(valParts, ",")+")")
		total++
	}
	if rows.Err() != nil {
		return 0, 0, 0, 0, 0, nil, fmt.Errorf("读取本地战报结束失败: %v", rows.Err())
	}
	if total == 0 {
		return 0, 0, 0, 0, 0, nil, fmt.Errorf("本地 battle_report 表暂无数据")
	}

	// 先批量(每批500个id)查询云端已存在的 battle_id，只推送缺失的行
	existing := map[string]bool{}
	for i := 0; i < len(ids); i += 500 {
		end := i + 500
		if end > len(ids) {
			end = len(ids)
		}
		lits := make([]string, 0, end-i)
		for _, id := range ids[i:end] {
			lits = append(lits, sqlLiteral(id))
		}
		cloudRows, err := pgQueryRows(
			"SELECT battle_id FROM battle_report WHERE alliance = " + sqlLiteral(syncAlliance()) + " AND battle_id IN (" + strings.Join(lits, ",") + ")")
		if err != nil {
			return 0, 0, 0, 0, 0, nil, fmt.Errorf("查询云端已存在战报失败: %v", err)
		}
		for _, r := range cloudRows {
			if len(r) > 0 {
				existing[r[0]] = true
			}
		}
	}

	missVals := make([]string, 0, len(values))
	missIds := make([]string, 0, len(ids))
	var skipped int64
	for i, id := range ids {
		if existing[id] {
			skipped++
			continue
		}
		missVals = append(missVals, values[i])
		missIds = append(missIds, id)
	}
	if skipped == total {
		log.Printf("同步器: 分批推送最新战报 云端已包含全部最新 %d 条，无需推送", total)
		return 0, 0, total, minBid, maxBid, nil, nil
	}
	if skipped > 0 {
		log.Printf("同步器: 分批推送最新战报 跳过云端已有 %d 条，仅推 %d 条", skipped, len(missVals))
	}

	for i := 0; i < len(missVals); i += 100 {
		end := i + 100
		if end > len(missVals) {
			end = len(missVals)
		}
		sql := upsertSQL("battle_report", cols, missVals[i:end], syncAlliance(), "battle_id")
		if err := pgExec(sql); err != nil {
			failed += int64(end - i)
			desc := strings.Join(missIds[i:end], ",")
			if end-i > 20 {
				desc = strings.Join(missIds[i:i+20], ",") + fmt.Sprintf("...共%d条", end-i)
			}
			msg := fmt.Sprintf("本批 %d 条失败(主键: %s): %v", end-i, desc, err)
			failMsgs = append(failMsgs, msg)
			log.Printf("同步器: 分批推送最新战报 第 %d 批失败: %s", i/100+1, msg)
			continue
		}
		pushed += int64(end - i)
		// 100 条一批循环推送，日志标明第几批，便于确认推送节奏
		totalBatches := (len(missVals) + 99) / 100
		log.Printf("同步器: 分批推送最新战报 第 %d/%d 批 成功 %d 条(本批 battle_id %s~%s)",
			i/100+1, totalBatches, end-i, missIds[i], missIds[end-1])
	}
	return pushed, failed, total, minBid, maxBid, failMsgs, nil
}

// ManualPushRecent 手动检查本地最新 count 条战报(battle_id 倒序)哪些云端缺失并补齐。
// 不走增量游标：直接按行 INSERT OR REPLACE，用于补齐游标区间内的数据空洞；已存在的行跳过、只推缺失行
func (a *App) ManualPushRecent(count int64) string {
	if count <= 0 || count > 3000 {
		return global.Response{Message: "推送数量需在 1~3000 之间"}.Error()
	}
	if model.Conn == nil {
		return global.Response{Message: "数据库未连接，请先选择数据库"}.Error()
	}

	// 重新读取 supabase.json 并检查数据库状态，已禁用的配置不会自动启用
	initSync()

	syncMu.Lock()
	enabled := syncEnabled
	syncMu.Unlock()
	if !enabled {
		return global.Response{Message: "云同步未启用（supabase.json 缺失或 host/user/password 为空），无法推送"}.Error()
	}

	// 推送前刷新自动识别同盟名(配置未指定 alliance 时使用识别值)
	refreshAutoAlliance()

	// 确保云端表结构存在（已存在的表不会重复建）
	if err := ensureCloudSchema(); err != nil {
		return global.Response{Message: "云端建表失败: " + err.Error()}.Error()
	}

	pushed, failed, total, minBid, maxBid, failMsgs, err := pushRecentBatches(count)
	if err != nil {
		return global.Response{Message: err.Error()}.Error()
	}

	syncMu.Lock()
	syncLastRun = time.Now().Unix()
	if len(failMsgs) > 0 {
		syncLastErr = fmt.Sprintf("手动推送最新%d条: 成功%d 失败%d", count, pushed, failed)
	}
	syncMu.Unlock()

	log.Printf("同步器: 手动推送最新战报完成, 共 %d 条, 成功 %d, 失败 %d(battle_id %d~%d)",
		total, pushed, failed, minBid, maxBid)

	data := map[string]interface{}{
		"total":  total,
		"pushed": pushed,
		"failed": failed,
		"max_battle_id": maxBid,
		"min_battle_id": minBid,
	}
	if failed > 0 {
		data["errors"] = failMsgs
		return global.Response{Message: fmt.Sprintf("推送完成：成功 %d 条、失败 %d 条（详见运行日志）", pushed, failed), Data: data}.Error()
	}
	return global.Response{Data: data}.Success()
}