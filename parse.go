package main

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"stzbHelper/global"
	"stzbHelper/model"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"gorm.io/gorm/clause"
)

func parseBookData(data []byte) {
	var raw []interface{}
	err := json.Unmarshal(data, &raw)
	if err != nil {
		log.Println("解析主公簿数据失败:", err)
		return
	}
	if len(raw) < 2 {
		return
	}
	dataMap, ok := raw[1].(map[string]interface{})
	if !ok {
		return
	}

	result := map[string]interface{}{}

	// log.role_name, log.liked
	if logData, ok := dataMap["log"].(map[string]interface{}); ok {
		if v, ok := logData["role_name"].(string); ok {
			result["role_name"] = v
		}
		if v, ok := logData["liked"]; ok {
			result["likes"] = v
		}
	}

	// server[0]
	if server, ok := dataMap["server"].([]interface{}); ok && len(server) > 0 {
		result["server"] = server[0]
	}

	// personal 数组
	if personal, ok := dataMap["personal"].([]interface{}); ok {
		if len(personal) > 14 {
			result["power"] = personal[14]
		}
		if len(personal) > 41 {
			result["main_city_pos"] = personal[41]
		}
	}

	// union [0,"",id,"同盟名","分组名",...]
	if union, ok := dataMap["union"].([]interface{}); ok {
		if len(union) > 3 {
			result["alliance_name"] = union[3]
		}
		if len(union) > 4 {
			result["group_name"] = union[4]
		}
	}

	// history [登录天数, 最高灭敌, 最高武勋, 最高势力, 赛季参与数, 最高攻城, [灭敌数,武将名,武将ID], ...]
	if history, ok := dataMap["history"].([]interface{}); ok {
		if len(history) > 0 {
			result["login_days"] = history[0]
		}
		if len(history) > 1 {
			result["max_season_kills"] = history[1]
		}
		if len(history) > 2 {
			result["max_merit"] = history[2]
		}
		if len(history) > 3 {
			result["max_power"] = history[3]
		}
		if len(history) > 4 {
			result["season_count"] = history[4]
		}
		if len(history) > 5 {
			result["max_season_siege"] = history[5]
		}
		if len(history) > 6 {
			if team, ok := history[6].([]interface{}); ok && len(team) > 2 {
				result["best_team_kills"] = team[0]
				result["best_team_hero_name"] = team[1]
				result["best_team_hero_id"] = team[2]
			}
		}
		if len(history) > 7 {
			result["max_season_demolish"] = history[7]
		}
	}

	// zanAndvistor [访客数, 点赞数, []]
	if zv, ok := dataMap["zanAndvistor"].([]interface{}); ok {
		if len(zv) > 0 {
			result["visitors"] = zv[0]
		}
	}

	// city_card ["[\"ip\",\"code\",\"location\"]", ...]
	if cc, ok := dataMap["city_card"].([]interface{}); ok && len(cc) > 0 {
		if ccStr, ok := cc[0].(string); ok && ccStr != "" {
			var ccArr []interface{}
			if json.Unmarshal([]byte(ccStr), &ccArr) == nil {
				if len(ccArr) > 0 {
					result["ip"] = ccArr[0]
				}
				if len(ccArr) > 2 {
					result["location"] = ccArr[2]
				}
			}
		}
	}

	result["raw"] = dataMap

	if global.AppCtx != nil {
		runtime.EventsEmit(global.AppCtx, "bookData", result)
	}
}

func parseBattleCallData(data []byte) {
	var raw []interface{}
	err := json.Unmarshal(data, &raw)
	if err != nil {
		log.Println("解析战役叫阵数据失败:", err)
		return
	}

	var messages []map[string]interface{}
	for _, item := range raw {
		entry, ok := item.([]interface{})
		if !ok || len(entry) < 2 {
			continue
		}
		fields, ok := entry[1].([]interface{})
		if !ok {
			continue
		}
		msg := map[string]interface{}{}
		if len(fields) > 4 {
			msg["content"] = fields[4]
		}
		if len(fields) > 5 {
			msg["timestamp"] = fields[5]
		}
		if len(fields) > 7 {
			msg["alliance_name"] = fields[7]
		}
		if len(fields) > 44 {
			msg["player_name"] = fields[44]
		}
		messages = append(messages, msg)
	}

	if global.AppCtx != nil {
		runtime.EventsEmit(global.AppCtx, "battleCallData", messages, string(data))
	}
}

func ParseData(cmdId int, data []byte) {
	if global.IsDebug {
		log.Println("收到[" + strconv.Itoa(cmdId) + "]消息:" + string(parseZlibData(data)))
	}

	if cmdId == 724 {
		if global.ExVar.NeedPushBattleCallData || global.ExVar.NeedPushEnemyMonitor {
			go parseBattleCallData(parseZlibData(data))
		}
	} else if cmdId == 103 {
		parseTeamUser(data)
	} else if cmdId == 92 {
		if global.ExVar.NeedGetBattleData || global.ExVar.NeedAutoScroll || global.ExVar.NeedAutoScrollDetect {
			log.Println("已开启获取详细战报,目前会暂停考勤战报的获取")
			parseBattleData(data)
		} else {
			parseReport(data)
		}
	}
}

func DecodeType5(data []byte) string {
	if data[0] == 5 {
		result := make([]byte, len(data)-1)
		for index, value := range data[1:] {
			result[index] = value ^ 152
		}
		return string(result)
	}
	return ""
}

// 原始数据结构
type RawData []interface{}

type BattleData struct {
	BattleId              int64       `json:"battle_id"`
	AttackHelpId          string      `json:"attack_help_id"`
	Time                  int64       `json:"time"`
	Wid                   interface{} `json:"wid"`
	WidName               string      `json:"wid_name"`
	AttackName            string      `json:"attack_name"`
	AttackUnionName       string      `json:"attack_union_name"`
	AttackClanName        string      `json:"attack_clan_name"`
	DefendClanName        string      `json:"defend_clan_name"`
	DefendName            string      `json:"defend_name"`
	DefendUnionName       string      `json:"defend_union_name"`
	AttackAdvance         string      `json:"attack_advance"`
	AttackAllHeroInfo     string      `json:"attack_all_hero_info"`
	AttackerGearInfo      string      `json:"attacker_gear_info"`
	DefendAdvance         string      `json:"defend_advance"`
	DefendAllHeroInfo     string      `json:"defend_all_hero_info"`
	DefenderGearInfo      string      `json:"defender_gear_info"`
	AttackHeroType        string      `json:"attack_hero_type"`
	AttackHeroTypeAdvance string      `json:"attack_hero_type_advance"`
	DefendHeroType        string      `json:"defend_hero_type"`
	DefendHeroTypeAdvance string      `json:"defend_hero_type_advance"`
	AttackHp              int64       `json:"attack_hp"`
	DefendHp              int64       `json:"defend_hp"`
	Npc                   int64       `json:"npc"`
	AllSkillInfo          string      `json:"all_skill_info"`
	Result                int64       `json:"result"`
	ExtraResult           int64       `json:"extra_result"`
	AttackIdu             string      `json:"attack_idu"` //进攻方队伍ID
	DefendIdu             string      `json:"defend_idu"` //防守方队伍ID
	AttackerGongxun       int64       `json:"attacker_gongxun"` //进攻方武勋
	DefenderGongxun       int64       `json:"defender_gongxun"` //防守方武勋
	AttackerXwc           int64       `json:"attacker_xwc"`     //进攻方武策
	DefenderXwc           int64       `json:"defender_xwc"`     //防守方武策
	Garrison              int64       `json:"garrison"`         // 0=主力 1=拆迁队
	CityType              int64       `json:"city_type"`        // 城市类型
	FightType             int64       `json:"fight_type"`       // 战斗类型
	AttackBaseHeroid      int64       `json:"attack_base_heroid"`
	AttackBaseLevel       int64       `json:"attack_base_level"`
	DefendBaseHeroid      int64       `json:"defend_base_heroid"`
	DefendBaseLevel       int64       `json:"defend_base_level"`
	FirstOccupyLvnLand    int64       `json:"first_occupy_lvn_land"` // 首占标记
	PressAttack           int64       `json:"press_attack"`          // 压制进攻
	Military              int64       `json:"military"`              // 军事(集结?)标记
}

// findBattleDesc 递归扫描战斗数据，提取含"占领了/拆除"的战报描述文本
func findBattleDesc(v interface{}) string {
	switch t := v.(type) {
	case string:
		if strings.Contains(t, "占领了") || strings.Contains(t, "拆除") {
			return t
		}
	case []interface{}:
		for _, e := range t {
			if d := findBattleDesc(e); d != "" {
				return d
			}
		}
	case map[string]interface{}:
		for _, e := range t {
			if d := findBattleDesc(e); d != "" {
				return d
			}
		}
	}
	return ""
}

// parseBattleData 解析战报数据
func parseBattleData(data []byte) {
	msgdata := parseZlibData(data)
	fmt.Println("原始数据:", string(msgdata))

	// 检测模式：标记已收到战报数据
	if global.ExVar.NeedAutoScrollDetect {
		log.Println("自动翻阅检测: 收到同盟战报数据,页面确认成功")
		global.ExVar.AutoScrollDetected = true
	}

	// 调试日志：看看检测标志位的状态
	if global.ExVar.NeedAutoScrollDetect || global.ExVar.NeedAutoScroll {
		log.Printf("自动翻阅: parseBattleData 被调用, NeedAutoScrollDetect=%v NeedAutoScroll=%v AutoScrollDetected=%v",
			global.ExVar.NeedAutoScrollDetect, global.ExVar.NeedAutoScroll, global.ExVar.AutoScrollDetected)
	}

	// 自动翻阅模式：检查时间边界
	if (global.ExVar.NeedAutoScroll || global.ExVar.NeedAutoScrollDetect || global.ExVar.NeedAdbScroll) && global.AppCtx != nil {
		latestTime := int64(0)
		var jsondata [][]any
		json.Unmarshal(msgdata, &jsondata)
		for _, v := range jsondata {
			reportJSON, _ := json.Marshal(v[0])
			var report model.Report
			json.Unmarshal(reportJSON, &report)
			if int64(report.Time) > latestTime {
				latestTime = int64(report.Time)
			}
			// 如果战报时间早于截止时间，记录停止点
			if global.ExVar.AutoScrollTargetTime > 0 && int64(report.Time) < global.ExVar.AutoScrollTargetTime && int64(report.Time) > 0 {
				if int64(report.Time) > global.ExVar.AutoScrollStopTime {
					global.ExVar.AutoScrollStopTime = int64(report.Time)
				}
			}
		}
	}

	if len(msgdata) > 0 {
		var rawData RawData
		err := json.Unmarshal(msgdata, &rawData)
		if err != nil {
			log.Printf("解析JSON失败: %v", err)
			return
		}

		fmt.Printf("数据长度: %d\n", len(rawData))

		// 遍历所有战斗记录
		battleCount := 0
		for _, item := range rawData {
			// 每个item是一个数组 [战斗数据, 其他数据...]
			battleArray, ok := item.([]interface{})
			if !ok || len(battleArray) == 0 {
				continue
			}

			// 第一个元素是战斗数据
			battleMap, ok := battleArray[0].(map[string]interface{})
			if !ok {
				continue
			}

			// 转换为结构体
			var battleData BattleData
			jsonData, err := json.Marshal(battleMap)
			if err != nil {
				log.Printf("转换战斗数据失败: %v", err)
				continue
			}

			if err := json.Unmarshal(jsonData, &battleData); err != nil {
				log.Printf("解析战斗数据失败: %v", err)
				continue
			}

			fmt.Printf("处理战斗ID: %d\n", battleData.BattleId)

			widStr := ""
			switch v := battleData.Wid.(type) {
			case string:
				widStr = v
			case float64:
				widStr = strconv.FormatInt(int64(v), 10)
			case int64:
				widStr = strconv.FormatInt(v, 10)
			case int:
				widStr = strconv.Itoa(v)
			default:
				// 尝试转换为字符串
				widStr = fmt.Sprintf("%v", v)
			}

			// 创建战斗报告
			report := model.BattleReport{
				BattleId:              battleData.BattleId,
				AttackHelpId:          battleData.AttackHelpId,
				Time:                  battleData.Time,
				Wid:                   widStr,
				WidName:               battleData.WidName,
				BattleDesc:            findBattleDesc(battleArray),
				AttackName:            battleData.AttackName,
				AttackUnionName:       battleData.AttackUnionName,
				AttackClanName:        battleData.AttackClanName,
				DefendClanName:        battleData.DefendClanName,
				DefendName:            battleData.DefendName,
				DefendUnionName:       battleData.DefendUnionName,
				AttackAdvance:         battleData.AttackAdvance,
				AttackAllHeroInfo:     battleData.AttackAllHeroInfo,
				AttackerGearInfo:      battleData.AttackerGearInfo,
				DefendAdvance:         battleData.DefendAdvance,
				DefendAllHeroInfo:     battleData.DefendAllHeroInfo,
				DefenderGearInfo:      battleData.DefenderGearInfo,
				AttackHeroType:        battleData.AttackHeroType,
				AttackHeroTypeAdvance: battleData.AttackHeroTypeAdvance,
				DefendHeroType:        battleData.DefendHeroType,
				DefendHeroTypeAdvance: battleData.DefendHeroTypeAdvance,
				AttackHp:              battleData.AttackHp,
				DefendHp:              battleData.DefendHp,
				Npc:                   battleData.Npc,
				AllSkillInfo:          battleData.AllSkillInfo,
				Result:                battleData.Result,
				ExtraResult:           battleData.ExtraResult,
				AttackIdu:             battleData.AttackIdu,
				DefendIdu:             battleData.DefendIdu,
				AttackerGongxun:       battleData.AttackerGongxun,
				DefenderGongxun:       battleData.DefenderGongxun,
				AttackerXwc:           battleData.AttackerXwc,
				DefenderXwc:           battleData.DefenderXwc,
				Garrison:              battleData.Garrison,
				CityType:              battleData.CityType,
				FightType:             battleData.FightType,
				AttackBaseHeroid:      battleData.AttackBaseHeroid,
				AttackBaseLevel:       battleData.AttackBaseLevel,
				DefendBaseHeroid:      battleData.DefendBaseHeroid,
				DefendBaseLevel:       battleData.DefendBaseLevel,
				FirstOccupyLvnLand:    battleData.FirstOccupyLvnLand,
				PressAttack:           battleData.PressAttack,
				Military:              battleData.Military,
			}

			// 解析进阶信息和武将信息
			report = parseHeroInfo(report)

			fmt.Printf("保存战斗报告: %+v\n", report)

			// 保存到数据库（upsert，避免重复 battle_id 冲突）
			result := model.Conn.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "battle_id"}},
				UpdateAll: true,
			}).Create(&report)
			if result.Error != nil {
				log.Printf("保存战斗报告失败: %v", result.Error)
			} else {
				battleCount++
				NotifySync()
			}
		}

		log.Printf("共处理 %d 条战斗记录", battleCount)
	}

	// 自动翻阅模式：推送进度到前端
	if global.ExVar.NeedAutoScroll && global.AppCtx != nil {
		var totalCount int64
		model.Conn.Model(&model.BattleReport{}).Count(&totalCount)
		runtime.EventsEmit(global.AppCtx, "autoScrollProgress", map[string]interface{}{
			"reportCount": totalCount,
			"latestTime":  global.ExVar.AutoScrollStopTime,
			"targetTime":  global.ExVar.AutoScrollTargetTime,
		})
	}
}

// 解析武将信息
func parseHeroInfo(report model.BattleReport) model.BattleReport {
	// 解析进攻方进阶信息
	attackAdvance := splitAndFilter(report.AttackAdvance, ";")
	fmt.Printf("进攻方进阶信息: %v\n", attackAdvance)

	attackTotal := int64(0)
	for i, advance := range attackAdvance {
		if i == 0 { // 跳过第一个元素
			continue
		}
		if len(advance) > 0 {
			star, err := strconv.ParseInt(advance[0], 10, 64)
			if err == nil {
				switch i {
				case 1:
					report.AttackHero1Star = star
				case 2:
					report.AttackHero2Star = star
				case 3:
					report.AttackHero3Star = star
				}
				attackTotal += star
			}
		}
	}
	report.AttackTotalStar = attackTotal

	// 解析防守方进阶信息
	defendAdvance := splitAndFilter(report.DefendAdvance, ";")
	fmt.Printf("防守方进阶信息: %v\n", defendAdvance)

	defendTotal := int64(0)
	for i, advance := range defendAdvance {
		if i == 3 { // 跳过第三个元素
			continue
		}
		if len(advance) > 0 {
			star, err := strconv.ParseInt(advance[0], 10, 64)
			if err == nil {
				switch i {
				case 0:
					report.DefendHero3Star = star
				case 1:
					report.DefendHero2Star = star
				case 2:
					report.DefendHero1Star = star
				}
				defendTotal += star
			}
		}
	}
	report.DefendTotalStar = defendTotal

	// 解析进攻方武将信息
	attackHeroInfo := splitAndFilter(report.AttackAllHeroInfo, ";")
	fmt.Printf("进攻方武将信息: %v\n", attackHeroInfo)

	for i, hero := range attackHeroInfo {
		if len(hero) >= 2 {
			heroID, _ := strconv.ParseInt(hero[0], 10, 64)
			heroLevel, _ := strconv.ParseInt(hero[1], 10, 64)

			switch i {
			case 0:
				report.AttackHero1Id = heroID
				report.AttackHero1Level = heroLevel
			case 1:
				report.AttackHero2Id = heroID
				report.AttackHero2Level = heroLevel
			case 2:
				report.AttackHero3Id = heroID
				report.AttackHero3Level = heroLevel
			}
		}
	}

	// 解析防守方武将信息
	defendHeroInfo := splitAndFilter(report.DefendAllHeroInfo, ";")
	fmt.Printf("防守方武将信息: %v\n", defendHeroInfo)

	var defendHpAfter int64 = 0
	for i, hero := range defendHeroInfo {
		if len(hero) >= 2 {
			heroID, _ := strconv.ParseInt(hero[0], 10, 64)
			heroLevel, _ := strconv.ParseInt(hero[1], 10, 64)
			// hero格式: id,等级,战前兵力,战后兵力,经验  -> 战后兵力在 index 3
			if len(hero) > 3 {
				hpAfter, _ := strconv.ParseInt(hero[3], 10, 64)
				defendHpAfter += hpAfter
			}

			switch i {
			case 0:
				report.DefendHero1Id = heroID
				report.DefendHero1Level = heroLevel
			case 1:
				report.DefendHero2Id = heroID
				report.DefendHero2Level = heroLevel
			case 2:
				report.DefendHero3Id = heroID
				report.DefendHero3Level = heroLevel
			}
		}
	}
	report.DefendHpAfter = defendHpAfter

	return report
}

// 分割和过滤字符串
func splitAndFilter(input string, delimiter string) [][]string {
	if input == "" {
		return [][]string{}
	}

	parts := strings.Split(input, delimiter)
	var result [][]string

	for _, part := range parts {
		if part != "" {
			// 进一步按逗号分割
			subParts := strings.Split(part, ",")
			var filtered []string
			for _, subPart := range subParts {
				if subPart != "" {
					filtered = append(filtered, subPart)
				}
			}
			if len(filtered) > 0 {
				result = append(result, filtered)
			}
		}
	}

	return result
}

func parseReport(data []byte) {
	if !global.ExVar.NeedGetReport && !global.ExVar.NeedAutoListenReport {
		return
	}
	log.Println("收到同盟战报消息")
	msgdata := parseZlibData(data)
	if len(msgdata) > 0 {
		var jsondata [][]any
		json.Unmarshal(msgdata, &jsondata)

		var reports []model.Report
		var neededreports []model.Report
		var latestTime int64

		// 探测原始JSON中的耐久/守军字段：优先使用明确命中，否则保持0
		durabilityKeys := []string{"durability", "endure", "endurance", "defend_durability", "city_durability", "jian_zhi", "durability_down"}
		armyNumKeys := []string{"defend_army_num", "army_num", "defend_army", "defend_army_count", "shou_jun", "army_count"}

		extractField := func(m map[string]interface{}, keys []string) int {
			for _, k := range keys {
				if v, ok := m[k]; ok {
					switch t := v.(type) {
					case float64:
						return int(t)
					case int:
						return t
					case string:
						n, _ := strconv.Atoi(strings.TrimSpace(t))
						return n
					}
				}
			}
			return 0
		}

		for _, v := range jsondata {
			reportJSON, err := json.Marshal(v[0])
			if err != nil {
				fmt.Println("Error marshalling report:", err)
				continue
			}

			var rawMap map[string]interface{}
			var report model.Report
			if json.Unmarshal(reportJSON, &rawMap) == nil {
				err = json.Unmarshal(reportJSON, &report)
			} else {
				reportJSON = nil
			}
			if err != nil {
				fmt.Println("Error unmarshalling report:", err)
				continue
			}

			// 探测耐久/守军字段
			if rawMap != nil {
				report.Durability = extractField(rawMap, durabilityKeys)
				report.DefendArmyNum = extractField(rawMap, armyNumKeys)
				if report.Durability > 0 || report.DefendArmyNum > 0 {
					log.Printf("战报[%d] 耐久下降=%d 守军数量=%d\n", report.BattleID, report.Durability, report.DefendArmyNum)
				}
				report.DefendHpAfter = sumHeroHpAfter(report.DefendAllHeroInfo)
				report.RawJson = string(reportJSON)
			}

			reports = append(reports, report)

			// 攻城考勤：检查坐标 + 时间截止
			if global.ExVar.NeedGetReport && report.Wid == global.ExVar.NeededReportPos {
				if global.ExVar.NeededReportEndTime > 0 && int64(report.Time) < global.ExVar.NeededReportEndTime {
					// 战报时间早于截止时间，标记为已到达边界
					if global.AppCtx != nil {
						runtime.EventsEmit(global.AppCtx, "reportTimeReached", map[string]interface{}{
							"time":    report.Time,
							"endTime": global.ExVar.NeededReportEndTime,
						})
					}
					continue
				}
				neededreports = append(neededreports, report)
			}

			// 自动监听：推送新战报到前端
			if global.ExVar.NeedAutoListenReport && global.AppCtx != nil {
				runtime.EventsEmit(global.AppCtx, "newReport", report)
			}

			if int64(report.Time) > latestTime {
				latestTime = int64(report.Time)
			}
		}

		log.Println("解析同盟战报成功,共" + strconv.Itoa(len(reports)) + "条 符合条件的共" + strconv.Itoa(len(neededreports)) + "条")

		// 保存攻城考勤战报
		if len(neededreports) > 0 {
			action := model.Conn.Save(&neededreports)
			fmt.Println("数据库共新增" + strconv.Itoa(int(action.RowsAffected)) + "条战报")
			NotifySync()
		}

		// 推送到前端（用于实时显示抓取进度）
		if global.ExVar.NeedGetReport && global.AppCtx != nil {
			var totalCount int64
			model.Conn.Model(&model.Report{}).Where("wid = ?", global.ExVar.NeededReportPos).Count(&totalCount)
			runtime.EventsEmit(global.AppCtx, "reportProgress", map[string]interface{}{
				"count":      totalCount,
				"latestTime": latestTime,
				"batchSize":  len(neededreports),
			})
		}

		// 保存所有战报到 reports 表（自动监听模式）
		if global.ExVar.NeedAutoListenReport {
			action := model.Conn.Save(&reports)
			fmt.Println("自动监听: 新增" + strconv.Itoa(int(action.RowsAffected)) + "条战报")
			NotifySync()
		}
	} else {
		log.Println("解析同盟战报消息失败")
	}
}

func parseTeamUser(data []byte) {
	log.Println("收到同盟成员消息")
	if global.IsDebug {
		log.Println(string(parseZlibData(data)))
	}

	msgdata := parseZlibData(data)
	if len(msgdata) > 0 {
		var jsondata [][]any
		json.Unmarshal(msgdata, &jsondata)

		var ids []int
		var teamUsers []model.TeamUser
		for _, item := range jsondata {
			teamUsers = append(teamUsers, model.ToTeamUser(item))
			ids = append(ids, int(item[0].(float64)))
		}

		log.Println("同盟成员消息解析成功！共" + strconv.Itoa(len(teamUsers)) + "人")
		model.Conn.Save(teamUsers)
		model.Conn.Not("id", ids).Delete(model.TeamUser{})
		NotifySync()
	} else {
		log.Println("解析同盟成员消息失败")
	}
}

// sumHeroHpAfter 从武将信息串(格式 id,等级,战前兵力,战后兵力,经验;...)累加战后总兵力
func sumHeroHpAfter(info string) int {
	total := 0
	if info == "" {
		return 0
	}
	for _, part := range strings.Split(info, ";") {
		if part == "" {
			continue
		}
		fields := strings.Split(part, ",")
		if len(fields) > 3 {
			n, err := strconv.Atoi(strings.TrimSpace(fields[3]))
			if err == nil {
				total += n
			}
		}
	}
	return total
}

// parseZlibData 解压 zlib 数据
func parseZlibData(data []byte) []byte {
	if len(data) >= 2 && data[0] == 120 && data[1] == 156 {
		compressedReader := bytes.NewReader(data)
		zlibReader, err := zlib.NewReader(compressedReader)
		if err != nil {
			fmt.Println("Error creating zlib reader:", err)
			return []byte{}
		}
		defer zlibReader.Close()

		uncompressedData, err := io.ReadAll(zlibReader)
		if err != nil {
			fmt.Println("Error reading uncompressed data:", err)
			return []byte{}
		}
		return uncompressedData
	}
	return data
}
