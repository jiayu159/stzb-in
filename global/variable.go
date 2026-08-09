package global

import "context"

type WebExVar struct {
	NeededReportPos       int   //需要获取战报的坐标
	NeedGetReport         bool  //是否需要获取战报
	NeedSyncTeamUser      bool  //是否需要同步同盟成员信息
	BindIpInfo            bool  //是否绑定IP信息 开启后将过滤掉其他IP的数据包(特殊情况使用)
	NeedGetBattleData     bool  //是否开启抓取详细战报数据 用于抓取队伍
	NeedPushBookData      bool  //是否推送主公簿数据到前端
	NeedPushBattleCallData bool //是否推送战役叫阵数据到前端
	NeedPushEnemyMonitor  bool //是否推送敌军动向监控到前端
	NeedAutoListenReport  bool  //是否开启同盟战报自动监听
	NeededReportEndTime   int64 //攻城考勤截止时间(0=不限)
	NeedAutoScroll        bool  //是否开启自动翻阅
	AutoScrollTargetTime  int64 //自动翻阅截止时间戳(秒，0=不限)
	AutoScrollStopTime    int64 //自动翻阅实际停止时间戳(检测到早于此时间的战报时停止)
	NeedAutoScrollDetect  bool  //是否正在检测同盟战报页面
	AutoScrollDetected    bool  //是否检测到同盟战报数据
	NeedCaptureRequests   bool  //是否开启客户端请求分析（抓取客户端->服务器请求包用于逆向重放）
	ScrollMode            string //自动翻阅模式：mouse=鼠标滚轮 adb=连接模拟器
	NeedAdbScroll         bool  //是否开启adb模拟器自动翻阅
}

var ExVar = WebExVar{
	NeededReportEndTime: 0,
	ScrollMode:          "mouse",
}

var IsDebug bool = false
var Version string = "0.0.4Beta202605030300"
var OnlySrcIp = ""
var OnlyDstIp = ""
var PacketLoss = false
var LossBytes []byte
var LossCmdId = 0
var NeedBufSize = 0
var AppCtx context.Context
