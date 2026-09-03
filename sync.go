package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"stzbHelper/global"
	"stzbHelper/model"
)

//go:embed turso.json
var embeddedTursoJSON []byte

// Turso 云数据库增量同步器
// 配置: exe 同目录 turso.json {"url":"https://xxx.turso.io","token":"..."}，文件缺失时同步禁用
// 原理: 每张表按主键游标增量读取本地记录，通过 Hrana over HTTP 批量 INSERT OR REPLACE 到 Turso

type tursoConfig struct {
	URL       string `json:"url"`
	Token     string `json:"token"`
	AllowedDb string `json:"allowedDb"` // 可选: 允许同步的数据库文件名(不含 .db)，防止别人用此配置把其他库数据推入
}

type syncTable struct {
	Name     string
	PkColumn string
	ReplaceAll bool // true=整表覆盖(如成员表，与本地全量替换语义一致)
}

var syncTables = []syncTable{
	{Name: "battle_report", PkColumn: "battle_id"},
	{Name: "reports", PkColumn: "battle_id"},
	{Name: "team_user", PkColumn: "id", ReplaceAll: true}, // 按 name 刷新: 只删本批名字，云端他人记录保留(并集)
}

type syncStatus struct {
	Enabled bool  `json:"enabled"`
	LastRun int64 `json:"last_run"`
	LastErr string `json:"last_err"`
}

var (
	syncMu        sync.Mutex
	syncCfg       tursoConfig
	syncEnabled   bool
	syncLastRun   int64
	syncLastErr   string
	syncNotifyCh  = make(chan struct{}, 1)
	syncRunning   bool
	syncWaitingDB bool // 等待数据库打开(临时)，由 StartSyncLoop 循环重试 initSync
	syncHTTP      = &http.Client{Timeout: 30 * time.Second}
)

func initSync() {
	// 优先读 exe 同目录外部 turso.json(便于换库/换 token)，缺失时使用编译嵌入的配置
	var data []byte
	exePath, err := os.Executable()
	if err == nil {
		cfgPath := filepath.Join(filepath.Dir(exePath), "turso.json")
		data, err = os.ReadFile(cfgPath)
		if err != nil {
			log.Println("同步器: 未找到外部 turso.json，使用内置配置")
		}
	}
	if len(data) == 0 {
		data = embeddedTursoJSON
	}
	if len(data) == 0 {
		log.Println("同步器: 无 turso 配置，云同步禁用")
		syncWaitingDB = false
		return
	}
	var cfg tursoConfig
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.URL == "" || cfg.Token == "" {
		log.Println("同步器: turso.json 格式错误(url/token 不能为空)，云同步禁用")
		syncWaitingDB = false
		return
	}
	syncCfg = cfg
	// Hrana over HTTP 需要 https 端点，libsql:// 前缀自动转换
	syncCfg.URL = strings.Replace(syncCfg.URL, "libsql://", "https://", 1)
	// 数据库绑定校验已解除: 不再校验库名，允许任意本地库同步到云端
	if global.CurrentDbName == "" {
		// 数据库还没打开，属于临时状态: 由 StartSyncLoop 循环重试 initSync
		syncWaitingDB = true
		log.Println("同步器: 数据库未打开，云同步等待")
		return
	}
	syncWaitingDB = false
	syncEnabled = true
	log.Printf("同步器: 已启用，目标 %s", syncCfg.URL)
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

	ensureSyncTables()
	if err := ensureCloudSchema(); err != nil {
		log.Printf("同步器: 云端建表失败: %v", err)
		syncMu.Lock()
		syncLastErr = fmt.Sprintf("云端建表失败: %v", err)
		syncMu.Unlock()
		return
	}

	for _, t := range syncTables {
		if _, err := syncTableDelta(t); err != nil {
			syncMu.Lock()
			syncLastErr = fmt.Sprintf("%s: %v", t.Name, err)
			syncMu.Unlock()
			log.Printf("同步器: 表 %s 同步失败: %v", t.Name, err)
			return
		}
	}

	syncMu.Lock()
	syncLastRun = time.Now().Unix()
	syncLastErr = ""
	syncMu.Unlock()
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

	ensureSyncTables()
	if err := ensureCloudSchema(); err != nil {
		results = append(results, syncResult{Table: "云端建表", Status: "fail", Err: err.Error()})
		syncMu.Lock()
		syncLastErr = err.Error()
		syncMu.Unlock()
		log.Printf("同步器: 手动推送 云端建表失败: %v", err)
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
			continue
		}
		results = append(results, syncResult{Table: t.Name, Status: "ok", Count: count})
		log.Printf("同步器: 手动推送 表 %s 推送成功 %d 条", t.Name, count)
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
		// 从本地读出全部成员名，云端仅删除这些名字的旧记录(分批)，避免本地是云端子集时整表覆盖丢失他人数据
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
			if err := tursoExecute("DELETE FROM team_user WHERE name IN (" + strings.Join(lits, ",") + ")"); err != nil {
				return 0, fmt.Errorf("按 name 刷新 %d 个成员失败: %v", len(names), err)
			}
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

		sql := fmt.Sprintf("INSERT OR REPLACE INTO %s (%s) VALUES %s",
			t.Name, strings.Join(colsQuoted, ","), strings.Join(batch, ","))
		if err := tursoExecute(sql); err != nil {
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

// tursoExecute 通过 Hrana over HTTP 执行单条 SQL（批量语句）
func tursoExecute(sql string) error {
	payload := map[string]interface{}{
		"requests": []map[string]interface{}{
			{
				"type": "execute",
				"stmt": map[string]interface{}{"sql": sql},
			},
		},
		"batches": []interface{}{},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", syncCfg.URL+"/v2/pipeline", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+syncCfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := syncHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}
	var result struct {
		Results []struct {
			Type     string `json:"type"`
			Response struct {
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("响应解析失败: %v (%s)", err, string(respBody))
	}
	if len(result.Results) > 0 && result.Results[0].Type == "error" {
		return fmt.Errorf("Turso 执行失败: %s", string(respBody))
	}
	return nil
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

// ensureCloudSchema 在云端按本地表结构建表(含主键)，保证 INSERT OR REPLACE 可用
func ensureCloudSchema() error {
	for _, t := range syncTables {
		rows, err := model.Conn.Raw(fmt.Sprintf("PRAGMA table_info(%s)", t.Name)).Rows()
		if err != nil {
			return err
		}
		var colDefs []string
		var pkCols []string
		for rows.Next() {
			var cid, notnull, pk int
			var name, ctype string
			var dflt interface{}
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				rows.Close()
				return err
			}
			colDefs = append(colDefs, fmt.Sprintf(`"%s" %s`, name, colType(ctype)))
			if pk > 0 {
				pkCols = append(pkCols, fmt.Sprintf(`"%s"`, name))
			}
		}
		rows.Close()
		if len(colDefs) == 0 {
			continue
		}
		if len(pkCols) > 0 {
			colDefs = append(colDefs, "PRIMARY KEY ("+strings.Join(pkCols, ",")+")")
		}
		sql := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s)", t.Name, strings.Join(colDefs, ","))
		if err := tursoExecute(sql); err != nil {
			return fmt.Errorf("云端建表 %s 失败: %v", t.Name, err)
		}
	}
	return nil
}

// GetSyncStatus 查询云同步状态
func (a *App) GetSyncStatus() string {
	syncMu.Lock()
	defer syncMu.Unlock()
	return global.Response{Data: map[string]interface{}{
		"enabled":  syncEnabled,
		"url":      syncCfg.URL,
		"last_run": syncLastRun,
		"last_err": syncLastErr,
	}}.Success()
}

// ManualSync 手动强制检测配置并推送一轮数据到云端
func (a *App) ManualSync() string {
	if model.Conn == nil {
		return global.Response{Message: "数据库未连接，请先选择数据库"}.Error()
	}

	// 重新读取 turso.json 并检查数据库状态，已禁用的配置不会自动启用
	initSync()

	syncMu.Lock()
	enabled := syncEnabled
	syncMu.Unlock()
	if !enabled {
		return global.Response{Message: "云同步未启用（turso.json 缺失或 url/token 为空），无法推送"}.Error()
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

// ManualPushRecent 手动把本地最新 count 条战报(battle_id 倒序)强制推送到云端。
// 不走增量游标：直接按行 INSERT OR REPLACE，用于补齐游标区间内的数据空洞。
func (a *App) ManualPushRecent(count int64) string {
	if count <= 0 || count > 3000 {
		return global.Response{Message: "推送数量需在 1~3000 之间"}.Error()
	}
	if model.Conn == nil {
		return global.Response{Message: "数据库未连接，请先选择数据库"}.Error()
	}

	// 重新读取 turso.json 并检查数据库状态，已禁用的配置不会自动启用
	initSync()

	syncMu.Lock()
	enabled := syncEnabled
	syncMu.Unlock()
	if !enabled {
		return global.Response{Message: "云同步未启用（turso.json 缺失或 url/token 为空），无法推送"}.Error()
	}

	// 确保云端表结构存在（已存在的表不会重复建）
	if err := ensureCloudSchema(); err != nil {
		return global.Response{Message: "云端建表失败: " + err.Error()}.Error()
	}

	rows, err := model.Conn.Raw(
		"SELECT * FROM battle_report ORDER BY battle_id DESC LIMIT ?", count).Rows()
	if err != nil {
		return global.Response{Message: "读取本地战报失败: " + err.Error()}.Error()
	}
	cols, err := rows.Columns()
	if err != nil {
		rows.Close()
		return global.Response{Message: "读取列信息失败: " + err.Error()}.Error()
	}
	colsQuoted := make([]string, len(cols))
	for i, c := range cols {
		colsQuoted[i] = `"` + c + `"`
	}

	var values []string
	var ids []string
	var maxBid, minBid int64
	total := int64(0)
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			rows.Close()
			return global.Response{Message: "读取本地战报行失败: " + err.Error()}.Error()
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
	rows.Close()
	if rows.Err() != nil {
		return global.Response{Message: "读取本地战报结束失败: " + rows.Err().Error()}.Error()
	}
	if total == 0 {
		return global.Response{Message: "本地 battle_report 表暂无数据"}.Error()
	}

	pushed, failed := int64(0), int64(0)
	var failMsgs []string
	for i := 0; i < len(values); i += 100 {
		end := i + 100
		if end > len(values) {
			end = len(values)
		}
		sql := fmt.Sprintf("INSERT OR REPLACE INTO battle_report (%s) VALUES %s",
			strings.Join(colsQuoted, ","), strings.Join(values[i:end], ","))
		if err := tursoExecute(sql); err != nil {
			failed += int64(end - i)
			desc := strings.Join(ids[i:end], ",")
			if end-i > 20 {
				desc = strings.Join(ids[i:i+20], ",") + fmt.Sprintf("...共%d条", end-i)
			}
			msg := fmt.Sprintf("推送最新%d条中 本批 %d 条失败(主键: %s): %v", count, end-i, desc, err)
			failMsgs = append(failMsgs, msg)
			log.Printf("同步器: %s", msg)
			continue
		}
		pushed += int64(end - i)
		log.Printf("同步器: 手动推送最新战报 成功 %d 条(本批 battle_id %s~%s)", end-i, ids[i], ids[end-1])
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