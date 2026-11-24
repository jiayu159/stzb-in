package model

import "strconv"

type PowerRank struct {
	Rank        int    `json:"rank" gorm:"column:rank;primaryKey"`       // 排名
	UserId      int    `json:"user_id" gorm:"column:user_id"`            // 玩家ID
	Name        string `json:"name" gorm:"column:name"`                  // 玩家名称
	Power       int    `json:"power" gorm:"column:power"`                // 势力值(战力)
	BranchCity  int    `json:"branch_city_count" gorm:"column:branch_city_count"` // 分城
	ShuCheng    int    `json:"shu_cheng_count" gorm:"column:shu_cheng_count"`     // 戌城
	LandCount   int    `json:"land_count" gorm:"column:land_count"`      // 领地数量(土地)
	RefreshTime int64  `json:"refresh_time" gorm:"column:refresh_time"`  // 榜单刷新时间
}

func (PowerRank) TableName() string {
	return "power_rank"
}

/*
	势力榜响应索引 (cmd=700)
	[0, -1, {自己信息}, 300, [[0,{条目}],[1,{条目}],...], null, {土地id:等级}]

	条目字段: name/power/land_count/fort_count/force/region/role_id/user_id/
	         area/branch_city_count/shu_cheng_count/show_info/refresh_time
	本功能只保留: 排名 / 名称 / 势力值 / 分城 / 戌城 / 领地数量
*/

// ToPowerRank 由 cmd=700 条目 dict 构造势力榜记录
func ToPowerRank(rank int, d map[string]interface{}) PowerRank {
	num := func(k string) int {
		if v, ok := d[k]; ok {
			switch n := v.(type) {
			case float64:
				return int(n)
			case int:
				return n
			case string:
				x, _ := strconv.Atoi(n)
				return x
			}
		}
		return 0
	}
	name := ""
	if v, ok := d["name"]; ok {
		name, _ = v.(string)
	}
	pr := PowerRank{
		Rank:        rank,
		Name:        name,
		Power:       num("power"),
		BranchCity:  num("branch_city_count"),
		ShuCheng:    num("shu_cheng_count"),
		LandCount:   num("land_count"),
		RefreshTime: int64(num("refresh_time")),
	}
	if v, ok := d["user_id"]; ok {
		switch n := v.(type) {
		case float64:
			pr.UserId = int(n)
		case int:
			pr.UserId = n
		}
	}
	return pr
}