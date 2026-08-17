package main

import (
	"bytes"
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

// Turso 云数据库增量同步器
// 配置: exe 同目录 turso.json {"url":"https://xxx.turso.io","token":"..."}，文件缺失时同步禁用
// 原理: 每张表按主键游标增量读取本地记录，通过 Hrana over HTTP 批量 INSERT OR REPLACE 到 Turso

type tursoConfig struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

type syncTable struct {
	Name     string
	PkColumn string
	ReplaceAll bool // true=整表覆盖(如成员表，与本地全量替换语义一致)
}

var syncTables = []syncTable{
	{Name: "battle_report", PkColumn: "battle_id"},
	{Name: "reports", PkColumn: "battle_id"},
	{Name: "team_user", PkColumn: "id", ReplaceAll: true},
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
	syncHTTP      = &http.Client{Timeout: 30 * time.Second}
)

func initSync() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	cfgPath := filepath.Join(filepath.Dir(exePath), "turso.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		log.Println("同步器: 未找到 turso.json，云同步禁用")
		return
	}
	var cfg tursoConfig
	if err := json.Unmarshal(data, &cfg); err != nil || cfg.URL == "" || cfg.Token == "" {
		log.Println("同步器: turso.json 格式错误(url/token 不能为空)，云同步禁用")
		return
	}
	syncCfg = cfg
	// Hrana over HTTP 需要 https 端点，libsql:// 前缀自动转换
	syncCfg.URL = strings.Replace(syncCfg.URL, "libsql://", "https://", 1)
	syncEnabled = true
	log.Printf("同步器: 已启用，目标 %s", syncCfg.URL)
}

// StartSyncLoop 后台同步循环: 启动即同步一次，之后每 30 秒或收到入库信号时同步
func StartSyncLoop() {
	initSync()
	if !syncEnabled {
		return
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
		if err := syncTableDelta(t); err != nil {
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

// syncTableDelta 同步单张表。ReplaceAll 表整表覆盖，其余按主键增量，失败不推进游标
func syncTableDelta(t syncTable) error {
	var lastID int64
	if t.ReplaceAll {
		if err := tursoExecute("DELETE FROM " + t.Name); err != nil {
			return err
		}
		lastID = 0
	} else {
		var err error
		lastID, err = getCursor(t.Name)
		if err != nil {
			return fmt.Errorf("读取游标失败: %w", err)
		}
	}

	for {
		rows, err := model.Conn.Raw(
			fmt.Sprintf("SELECT * FROM %s WHERE %s > ? ORDER BY %s LIMIT 100", t.Name, t.PkColumn, t.PkColumn),
			lastID).Rows()
		if err != nil {
			return err
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			return err
		}
		colsQuoted := make([]string, len(cols))
		for i, c := range cols {
			colsQuoted[i] = `"` + c + `"`
		}

		var batch []string
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
				return err
			}
			var valParts []string
			for i, v := range vals {
				valParts = append(valParts, sqlLiteral(v))
				if cols[i] == t.PkColumn {
					if n, ok := toInt64(v); ok && n > maxID {
						maxID = n
					}
				}
			}
			batch = append(batch, "("+strings.Join(valParts, ",")+")")
			count++
		}
		rows.Close()
		if rows.Err() != nil {
			return rows.Err()
		}

		if count == 0 {
			return nil
		}

		sql := fmt.Sprintf("INSERT OR REPLACE INTO %s (%s) VALUES %s",
			t.Name, strings.Join(colsQuoted, ","), strings.Join(batch, ","))
		if err := tursoExecute(sql); err != nil {
			return err
		}

		// 游标推进到本批最大主键（ReplaceAll 模式无需游标）
		if !t.ReplaceAll {
			if err := setCursor(t.Name, maxID); err != nil {
				return fmt.Errorf("游标写入失败: %w", err)
			}
		}
		log.Printf("同步器: %s 已同步 %d 条(游标 %d)", t.Name, count, maxID)

		if count < 100 {
			return nil
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
			return err
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