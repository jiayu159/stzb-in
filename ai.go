package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"stzbHelper/global"
	"stzbHelper/model"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	aiBaseURL   = "https://ai.gitee.com/v1/chat/completions"
	aiModelName = "Qwen3-8B"
	aiAPIKey    = "stzb"
	aiMaxHistory = 20
)

type aiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

var aiHistory []aiMessage

var aiThinking = true // 思考模式开关，默认开启

// aiSchema 提供给规划模型的数据表结构说明
const aiSchema = `数据库为 SQLite 只读库，共 3 张表：
1. team_user 同盟成员
   id INTEGER 角色ID(用户说"角色id/成员id"指此字段)
   name TEXT 角色名
   group TEXT 分组(成员归属的同盟分组。注意: 用户说的"队伍/部队/阵容"绝不指这个字段! group是SQLite保留字,查询该列时必须加反引号转义,写成 反引号group反引号 的形式,否则SQL报错)
   contribute_total INTEGER 总贡献
   contribute_week INTEGER 本周贡献
   pos INTEGER 坐标
   power INTEGER 势力
   wu INTEGER 武勋
   join_time INTEGER 加入时间(秒级时间戳)
2. battle_report 战报(战斗详情，含玩家部队信息)
   battle_id INTEGER 主键
   time INTEGER 战报时间(秒级时间戳，越大越新)
   wid_name TEXT 战斗地点名(如城池名"营陵"、"平寿"，或地块"沃土Lv.6"、"土地Lv.5"。用户提到城池/地点时按此字段匹配)
   attack_name TEXT 进攻方角色名
   attack_union_name TEXT 进攻方同盟
   defend_name TEXT 防守方角色名
   defend_union_name TEXT 防守方同盟
   npc INTEGER 是否与npc战斗(1=是)
   result INTEGER 战斗结果(0=攻击方失败 1=攻击方胜利 2=平局)
   attack_hp INTEGER 攻击方兵力
   defend_hp INTEGER 防守方兵力
   attack_hero1_id/attack_hero2_id/attack_hero3_id INTEGER 攻击方队伍的3个武将ID(大营/中军/前锋)
   attack_hero1_level/attack_hero2_level/attack_hero3_level INTEGER 武将等级
   attack_hero1_star/attack_hero2_star/attack_hero3_star INTEGER 武将红度
   attack_total_star INTEGER 攻击方队伍总红度
   attack_all_hero_info TEXT 攻击方队伍武将+战法详情文本(JSON)
   all_skill_info TEXT 攻击方技能战法信息文本
   defend_hero1_id/defend_hero2_id/defend_hero3_id INTEGER 防守方队伍的3个武将ID
   defend_hero1_level/defend_hero2_level/defend_hero3_level INTEGER 防守方武将等级
   defend_hero1_star/defend_hero2_star/defend_hero3_star INTEGER 防守方武将红度
   defend_total_star INTEGER 防守方队伍总红度
   defend_all_hero_info TEXT 防守方队伍武将+战法详情文本(JSON)
   attacker_gongxun INTEGER 攻击方获得的武勋
   defender_gongxun INTEGER 防守方获得的武勋
   garrison INTEGER 0=主力 1=拆迁队
   durability INTEGER 攻城时耐久下降值(拆迁数值，仅新版本抓包有数据，旧库可能为空/缺列)
   其余字段为细节文本，一般无需查询
3. task 攻城任务
   id INTEGER
   name TEXT 任务名
   target_user_num INTEGER 目标参与人数
   complete_user_num INTEGER 已完成人数
   status INTEGER 任务状态
   target TEXT 目标描述

编写规则：
- 数据库为只读执行环境，你可以放心使用 SQLite 的基础查询与搜索语法自由检索：SELECT、WHERE、LIKE '%关键词%'、IN、BETWEEN、GROUP BY、ORDER BY、LIMIT、CASE WHEN、聚合函数(COUNT/SUM/AVG/MAX/MIN)、时间函数 strftime 等都可以按需组合
- 角色名用 LIKE 模糊匹配，例: name LIKE '%张三%' 或 attack_name LIKE '%张三%'
- 用户问题中的"队伍/部队/阵容/配队/用的什么武将/什么战法"等词，全部指玩家在战斗中使用的部队(三个武将及战法)，查询 battle_report 表中该玩家的武将字段(attack_hero* 系列或 defend_hero* 系列、attack_all_hero_info、all_skill_info)，与 team_user.group 分组字段完全无关！
- 查询某玩家的队伍示例: SELECT attack_hero1_id, attack_hero2_id, attack_hero3_id, attack_hero1_level, attack_hero2_level, attack_hero3_level, attack_hero1_star, attack_hero2_star, attack_hero3_star, all_skill_info FROM battle_report WHERE attack_name LIKE '%玩家名%' ORDER BY time DESC LIMIT 5
- 时间戳格式化: strftime('%m-%d %H:%M', time, 'unixepoch', 'localtime')
- 统计类问题(战斗次数、胜负、武勋排行等)优先用聚合+GROUP BY
- 战报数量可能很多，为了让结果精炼，常用 LIMIT 限制条数(如 LIMIT 20)，但不是强制
- 若问题需要多个维度的数据，可以用 UNION ALL 合并多条 SELECT`

// planAiQuery 阶段1：先推理用户话语意图（他想查什么对象/什么维度），再产出 SQL 查询计划
func planAiQuery(message string) (string, error) {
	planPrompt := "你是查询规划器。用户会问一句关于同盟数据的话，可能说得很口语化，没提表名和字段名。你的任务分两步：\n" +
		"第1步 推理意图：仔细分析这句话的真实含义，推断用户关心的数据对象和维度。可能的对象：角色/成员(势力、武勋、贡献、坐标、分组、队伍阵容)、城池/战斗地点(wid_name)、同盟、战报(战斗次数、胜负、兵损、武勋获得)、攻城任务(task)、时间段(最近N天/本周)。即使用户没明确说出关键词，也要从他的用词推断出最匹配的对象。\n" +
		"第2步 编写SQL：根据推理出的对象，编写一条 SQLite SELECT 查询从数据库获取所需数据，字段尽量覆盖用户关心的所有维度。\n" +
		"只输出一个 JSON 对象，格式: {\"sql\": \"这里写SQL\"}，不要输出任何其他内容或解释。\n\n表结构说明：\n" + aiSchema + "\n\n用户问题: " + message

	raw, err := callAiOpenAI([]aiMessage{{Role: "user", Content: planPrompt}})
	if err != nil {
		return "", err
	}
	_, raw = splitThinking(raw)
	return extractSqlPlan(raw)
}

// extractSqlPlan 从模型回复中提取 {"sql": "..."} 计划（从后往前找最后一个合法JSON，跳过思考文本）
func extractSqlPlan(raw string) (string, error) {
	if _, r := splitThinking(raw); r != "" {
		raw = r
	}
	for idx := strings.LastIndex(raw, "{"); idx != -1; idx = strings.LastIndex(raw[:idx], "{") {
		var plan struct {
			SQL string `json:"sql"`
		}
		if err := json.Unmarshal([]byte(raw[idx:]), &plan); err == nil && strings.TrimSpace(plan.SQL) != "" {
			return plan.SQL, nil
		}
	}
	return "", fmt.Errorf("查询分析未产出有效计划: %s", raw)
}

// executeAiQuery 阶段2：本地只读执行 SQL（允许基础查询/搜索语法，如 SELECT/WHERE/LIKE/GROUP BY 等；写操作一律拒绝）
func executeAiQuery(sql string) (string, error) {
	trimmed := strings.TrimSpace(sql)
	upper := strings.ToUpper(trimmed)
	if !strings.HasPrefix(upper, "SELECT") {
		return "", fmt.Errorf("仅支持查询语句，请以 SELECT 开头")
	}
	for _, kw := range []string{"INSERT", "UPDATE", "DELETE", "CREATE", "DROP", "ALTER", "ATTACH", "DETACH", "PRAGMA", "REPLACE INTO", "VACUUM", "BEGIN", "COMMIT", "ROLLBACK"} {
		if strings.Contains(upper, kw) {
			return "", fmt.Errorf("查询包含非只读操作，已拒绝")
		}
	}

	rows, err := model.Conn.Raw(trimmed).Rows()
	if err != nil {
		return "", fmt.Errorf("查询执行失败: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString(strings.Join(cols, " | "))
	sb.WriteString("\n")
	count := 0
	for rows.Next() {
		count++
		if count > 200 {
			sb.WriteString("(结果较多，已显示前200行)\n")
			break
		}
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			switch t := v.(type) {
			case nil:
				parts[i] = ""
			case []byte:
				parts[i] = string(t)
			case int64:
				parts[i] = strconv.FormatInt(t, 10)
			case float64:
				parts[i] = strconv.FormatFloat(t, 'f', -1, 64)
			case string:
				parts[i] = t
			default:
				parts[i] = fmt.Sprint(t)
			}
		}
		sb.WriteString(strings.Join(parts, " | "))
		sb.WriteString("\n")
	}
	if rows.Err() != nil {
		return "", rows.Err()
	}
	if count == 0 {
		return "", nil
	}

	result := sb.String()
	if len(result) > 30000 {
		result = result[:30000] + "\n...(结果过长已截断)"
	}
	return result, nil
}

// callAiOpenAI 调用大模型接口（思考模式跟随开关）
func callAiOpenAI(messages []aiMessage) (string, error) {
	payload := map[string]interface{}{
		"model":    aiModelName,
		"stream":   false,
		"messages": messages,
	}
	if aiThinking {
		payload["chat_template_kwargs"] = map[string]interface{}{"enable_thinking": true}
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", aiBaseURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("请求构造失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+aiAPIKey)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("AI 接口调用失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("AI 接口返回异常(%d): %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil || len(result.Choices) == 0 {
		return "", fmt.Errorf("AI 响应解析失败")
	}

	return strings.TrimSpace(result.Choices[0].Message.Content), nil
}

// splitThinking 把 Gitee AI 返回内容中 <think> 块切分为思考过程和正文
var thinkTagRe = regexp.MustCompile(`(?s)^\s* thinking(.*?) response\s*(.*)$`)

func splitThinking(content string) (thinking, reply string) {
	if m := thinkTagRe.FindStringSubmatch(content); m != nil {
		return strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	}
	idx := strings.Index(content, " thinking")
	if idx == -1 {
		if strings.HasPrefix(content, "thinking") {
			idx = 0
		} else {
			return "", content
		}
	}
	end := strings.Index(content[idx:], " response")
	if end == -1 {
		return strings.TrimSpace(content[idx:]), ""
	}
	end += idx
	return strings.TrimSpace(content[idx+len("thinking") : end]), strings.TrimSpace(content[end+len(" response"):])
}

// AiChat 妲己小秘书：分析用户意图→本地SQL查询→基于查询结果回答
func (a *App) AiChat(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return global.Response{Message: "请输入问题"}.Error()
	}
	if model.Conn == nil {
		return global.Response{Message: "数据库未连接"}.Error()
	}

	// 阶段1：让模型分析用户问题意图，编写 SQL 查询计划
	sql, err := planAiQuery(message)
	if err != nil {
		return global.Response{Message: err.Error()}.Error()
	}

	// 阶段2：本地执行查询，拿真实数据
	resultText, err := executeAiQuery(sql)
	if err != nil {
		return global.Response{Message: err.Error()}.Error()
	}

	// 阶段3：把查询结果交给模型生成回答
	var systemContent string
	if strings.TrimSpace(resultText) == "" {
		systemContent = "你是「妲己小秘书」，率土之滨同盟数据库的智能助手。程序已在本地数据库执行查询，但未查询到任何数据（可能是该角色不存在、名称不对或数据库暂无相关数据）。请直接如实回答：如果用户问的是具体角色/人物，就说\"没有查到该角色的相关信息\"；如果是其他统计类问题，说明暂无数据。不要编造任何数据。"
	} else {
		systemContent = "你是「妲己小秘书」，率土之滨同盟数据库的智能助手。程序已根据你的意图分析在本地数据库执行了查询，以下是查询结果（TSV表格，首行为列名）。请基于查询结果回答用户问题：列出关键数据并给出结论。要求：简洁有条理、引用具体数字、绝不编造结果之外的数据；结果不足时如实说明。如果用户问的是某玩家的队伍/阵容，应描述其武将搭配、红度、等级与战法信息；结果中武将只有ID没有名字时，应如实说明数据库未存武将名映射，只列出ID/等级/红度/战法。\n\n重要：查询结果的列名是英文缩写参数名，回答用户时必须把列名翻译成对应的中文对象名，例如：power=势力、wu=武勋、contribute_total=总贡献、contribute_week=本周贡献、join_time=加入时间、pos=坐标、wid_name=战斗地点名(城池名)、attack_name=进攻方角色名、defend_name=防守方角色名、attack_hp=攻击方兵力、defend_hp=防守方兵力、garrison=队伍类型(0=主力 1=拆迁队)、npc=是否与NPC战斗(1=是)、result=战斗结果(0=攻方失败 1=攻方胜利 2=平局)、attacker_gongxun=攻击方武勋、defender_gongxun=防守方武勋、attack_hero1_id/attack_hero2_id/attack_hero3_id=武将ID、attack_hero*_level=武将等级、attack_hero*_star=武将红度、attack_total_star=队伍总红度、name=角色名、group=分组、attack_union_name=进攻方同盟、defend_union_name=防守方同盟。绝不要直接输出 power/wu/wid_name 这类原始参数名，一律用中文表述。\n\n语言要求：用准确、自然、地道的中文口语回答，术语使用率土之滨的常用说法(势力、武勋、贡献、拆迁、守军、主力、攻城等)，句子通顺完整，像懂行的同盟参谋在汇报数据，不要机械罗列或机翻腔。\n\n查询结果:\n" + resultText
	}

	messages := []aiMessage{{Role: "system", Content: systemContent}}
	messages = append(messages, aiHistory...)
	messages = append(messages, aiMessage{Role: "user", Content: message})

	reply, err := callAiOpenAI(messages)
	if err != nil {
		return global.Response{Message: err.Error()}.Error()
	}

	thinking, reply := splitThinking(reply)

	// 记录对话历史（保留最近 N 条）
	aiHistory = append(aiHistory,
		aiMessage{Role: "user", Content: message},
		aiMessage{Role: "assistant", Content: reply},
	)
	if len(aiHistory) > aiMaxHistory {
		aiHistory = aiHistory[len(aiHistory)-aiMaxHistory:]
	}

	log.Printf("妲己小秘书: 提问[%s] SQL[%s] 思考%d字 回复%d字\n", message, sql, len(thinking), len(reply))

	return global.Response{Data: map[string]interface{}{
		"reply":    reply,
		"thinking": thinking,
	}}.Success()
}

// SetAiThinking 设置思考模式开关
func (a *App) SetAiThinking(enable bool) string {
	aiThinking = enable
	log.Printf("妲己小秘书: 思考模式 -> %v\n", enable)
	return global.Response{Message: "思考模式已切换"}.Success()
}

// ClearAiChat 清空妲己小秘书对话历史
func (a *App) ClearAiChat() string {
	aiHistory = nil
	if global.AppCtx != nil {
		runtime.EventsEmit(global.AppCtx, "aiChatCleared", nil)
	}
	return global.Response{Message: "对话已清空"}.Success()
}
