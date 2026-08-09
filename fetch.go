package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"stzbHelper/global"
	"stzbHelper/model"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// FetchTemplate 最近一次捕获到的 44871 翻页请求信息（逆向得到，用于直连重放）
type FetchTemplate struct {
	ServerAddr string `json:"server_addr"` // 游戏服务器 地址:端口
	PlayerID   string `json:"player_id"`   // 玩家ID(ASCII hex)
	Page       byte   `json:"page"`        // 页码计数器
	Key        byte   `json:"key"`         // 本次XOR密钥
	Counter    uint32 `json:"counter"`     // 请求计数器 [48:52]
	Sub3       string `json:"sub3"`        // [52:55] 子参数(hex)
	Payload    string `json:"payload"`     // 解码后的载荷（游标数字）
	Frame      []byte `json:"-"`           // 原始帧（重放测试用）
	GotHost    bool   `json:"got_host"`
	GotData    bool   `json:"got_data"`
}

var (
	fetchTplMu sync.Mutex
	fetchTpl   = FetchTemplate{}

	// 建连握手帧(新连接首个44871大帧,含token) — 直连前必须重放
	loginFrameMu    sync.Mutex
	loginFrame      []byte
	loginFrameHost  string
	loginFrameSeen  = map[string]bool{} // 每连接只缓存一次

	// 登录后的初始化指令帧序列(打开战报列表/切换同盟战报等)，翻页请求前必须按序重放
	initFramesMu   sync.Mutex
	initFrames     [][]byte
	initFramesHost string
	initFirstPage  []byte                  // 与初始化帧同连接的首个翻页帧(用于测试重放)
	initCollect    = map[string][][]byte{} // 按连接暂存收集中的初始化帧
	initWinCount   = map[string]int{}      // 每连接初始化窗口内已统计请求数
	initWinPage    = map[string][]byte{}   // 每连接窗口内首个翻页帧
	initOpenSeq    = map[string][][]byte{} // 每连接打开的"战报列表"序列帧(10ea/2b6/15f96)，会话动态

	// mode=0000000a 批量战报详情请求帧(翻页后游戏用它获取每条战报的完整数据cmd=92) — 每服务器缓存最新一条
	batchFrameMu   sync.Mutex
	batchFrame     []byte
	batchFrameHost string

	// mode=0000005c 战报详情请求帧(载荷[[1,2],[1,3],[-1],battle_id,0]，回放可得cmd=92完整战报) — 每服务器缓存最新一条
	detailFrameMu   sync.Mutex
	detailFrame     []byte
	detailFrameHost string

		// mode=000010e9 打开战报面板请求帧(载荷[<主城ID>,0]，详情请求前必须先发) — 每服务器缓存最新一条
	openPanelMu   sync.Mutex

	// 直连测试登录后绑定的连接级会话身份(来自 98888 登录确认帧)。
	// 实测: playerID 和 counter seed 每次登录都会变，业务帧必须用新会话身份构造，不能复用旧模板。
	activeSessionMu   sync.Mutex
	activeSessionPID  string // 32字节 ASCII hex 玩家ID
	activeSessionVer  []byte // 4字节 版本(98888[12:16]与[52:56]重复一致)
	activeSessionSeed uint32 // 3字节 counter seed([17:20]大端)，首业务帧counter=seed+4
	activeSessionOK   bool
	openPanel     []byte
	openPanelHost string

	fetchRunning bool
	fetchStop    chan struct{}
)

const fetchCmdID = 0xAF47 // 44871

// ---------------- 直连缓存持久化（握手帧/初始化帧/批量帧/请求模板） ----------------
// 握手帧token只在游戏重新登录时出现，程序重启后需从缓存恢复，否则无法直连。

const fetchCacheFile = "directfetch_cache.json"

var fetchDebugMu sync.Mutex

// stripBOM 兼容带UTF-8 BOM的缓存文件(如PowerShell脚本生成的)
func stripBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

func fetchDebug(format string, args ...interface{}) {
	fetchDebugMu.Lock()
	defer fetchDebugMu.Unlock()
	f, err := os.OpenFile("fetch_debug.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(time.Now().Format("15:04:05.000") + " " + fmt.Sprintf(format, args...) + "\n")
}

type fetchCache struct {
	LoginFrameHex  string   `json:"login_frame_hex"`
	LoginFrameHost string   `json:"login_frame_host"`
	InitFramesHex  []string `json:"init_frames_hex"`
	InitFirstPage  string   `json:"init_first_page"`
	InitFramesHost string   `json:"init_frames_host"`
	BatchFrameHex  string   `json:"batch_frame_hex"`
	BatchFrameHost string   `json:"batch_frame_host"`
	DetailFrameHex string   `json:"detail_frame_hex"`
	DetailFrameHost string  `json:"detail_frame_host"`
	OpenPanelHex   string   `json:"open_panel_hex"`
	OpenPanelHost  string   `json:"open_panel_host"`
	TplServerAddr  string   `json:"tpl_server_addr"`
	TplPlayerID    string   `json:"tpl_player_id"`
	TplPage        byte     `json:"tpl_page"`
	TplKey         byte     `json:"tpl_key"`
	TplCounter     uint32   `json:"tpl_counter"`
	TplSub3        string   `json:"tpl_sub3"`
	TplPayload     string   `json:"tpl_payload"`
	TplFrameHex    string   `json:"tpl_frame_hex"`
	TplGotHost     bool     `json:"tpl_got_host"`
	TplGotData     bool     `json:"tpl_got_data"`
}

func saveFetchCache() {
	// 先从磁盘读取旧值：内存为空时保留旧缓存(如握手帧只在游戏重新登录时出现)
	var prev fetchCache
	if data, err := os.ReadFile(fetchCacheFile); err != nil {
		fetchDebug("save: 读取旧缓存失败 cwd=%v err=%v", cwd(), err)
	} else if err := json.Unmarshal(stripBOM(data), &prev); err != nil {
		fetchDebug("save: 解析旧缓存失败 err=%v", err)
	}

	c := fetchCache{}
	loginFrameMu.Lock()
	if len(loginFrame) > 0 {
		c.LoginFrameHex = hex.EncodeToString(loginFrame)
		c.LoginFrameHost = loginFrameHost
	} else {
		c.LoginFrameHex = prev.LoginFrameHex
		c.LoginFrameHost = prev.LoginFrameHost
	}
	loginFrameMu.Unlock()
	initFramesMu.Lock()
	if len(initFrames) > 0 {
		for _, f := range initFrames {
			c.InitFramesHex = append(c.InitFramesHex, hex.EncodeToString(f))
		}
		c.InitFirstPage = hex.EncodeToString(initFirstPage)
		c.InitFramesHost = initFramesHost
	} else {
		c.InitFramesHex = prev.InitFramesHex
		c.InitFirstPage = prev.InitFirstPage
		c.InitFramesHost = prev.InitFramesHost
	}
	initFramesMu.Unlock()
	batchFrameMu.Lock()
	if len(batchFrame) > 0 {
		c.BatchFrameHex = hex.EncodeToString(batchFrame)
		c.BatchFrameHost = batchFrameHost
	} else {
		c.BatchFrameHex = prev.BatchFrameHex
		c.BatchFrameHost = prev.BatchFrameHost
	}
	batchFrameMu.Unlock()
	detailFrameMu.Lock()
	if len(detailFrame) > 0 {
		c.DetailFrameHex = hex.EncodeToString(detailFrame)
		c.DetailFrameHost = detailFrameHost
	} else {
		c.DetailFrameHex = prev.DetailFrameHex
		c.DetailFrameHost = prev.DetailFrameHost
	}
	detailFrameMu.Unlock()
	openPanelMu.Lock()
	if len(openPanel) > 0 {
		c.OpenPanelHex = hex.EncodeToString(openPanel)
		c.OpenPanelHost = openPanelHost
	} else {
		c.OpenPanelHex = prev.OpenPanelHex
		c.OpenPanelHost = prev.OpenPanelHost
	}
	openPanelMu.Unlock()
	fetchTplMu.Lock()
	if fetchTpl.GotData {
		c.TplServerAddr = fetchTpl.ServerAddr
		c.TplPlayerID = fetchTpl.PlayerID
		c.TplPage = fetchTpl.Page
		c.TplKey = fetchTpl.Key
		c.TplCounter = fetchTpl.Counter
		c.TplSub3 = fetchTpl.Sub3
		c.TplPayload = fetchTpl.Payload
		c.TplFrameHex = hex.EncodeToString(fetchTpl.Frame)
		c.TplGotHost = fetchTpl.GotHost
		c.TplGotData = fetchTpl.GotData
	}
	fetchTplMu.Unlock()

	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	os.WriteFile(fetchCacheFile, data, 0644)
	fetchDebug("save: cwd=%v login(%d)=%d prevLogin=%d init=%d batch=%d tpl=%v",
		cwd(), len(loginFrame), len(c.LoginFrameHex), len(prev.LoginFrameHex), len(c.InitFramesHex), len(c.BatchFrameHex), c.TplGotData)
}

func cwd() string {
	wd, _ := os.Getwd()
	return wd
}

func loadFetchCache() {
	wd, _ := os.Getwd()
	data, err := os.ReadFile(fetchCacheFile)
	if err != nil {
		fetchDebug("load: 读取失败 cwd=%v err=%v", wd, err)
		return
	}
	var c fetchCache
	if err := json.Unmarshal(stripBOM(data), &c); err != nil {
		fetchDebug("load: JSON解析失败 cwd=%v err=%v", wd, err)
		return
	}
	if c.LoginFrameHex != "" {
		if f, err := hex.DecodeString(c.LoginFrameHex); err == nil && len(f) >= 60 {
			loginFrameMu.Lock()
			loginFrame = f
			loginFrameHost = c.LoginFrameHost
			loginFrameMu.Unlock()
		}
	}
	initFramesMu.Lock()
	for _, h := range c.InitFramesHex {
		if f, err := hex.DecodeString(h); err == nil && len(f) > 0 {
			initFrames = append(initFrames, f)
		}
	}
	if c.InitFirstPage != "" {
		if f, err := hex.DecodeString(c.InitFirstPage); err == nil {
			initFirstPage = f
		}
	}
	initFramesHost = c.InitFramesHost
	initFramesMu.Unlock()
	batchFrameMu.Lock()
	if c.BatchFrameHex != "" {
		if f, err := hex.DecodeString(c.BatchFrameHex); err == nil {
			batchFrame = f
			batchFrameHost = c.BatchFrameHost
		}
	}
	batchFrameMu.Unlock()
	detailFrameMu.Lock()
	if c.DetailFrameHex != "" {
		if f, err := hex.DecodeString(c.DetailFrameHex); err == nil {
			detailFrame = f
			detailFrameHost = c.DetailFrameHost
		}
	}
	detailFrameMu.Unlock()
	openPanelMu.Lock()
	if c.OpenPanelHex != "" {
		if f, err := hex.DecodeString(c.OpenPanelHex); err == nil && len(f) >= 57 {
			openPanel = f
			openPanelHost = c.OpenPanelHost
		}
	}
	openPanelMu.Unlock()
	if c.TplGotData && c.TplFrameHex != "" {
		if f, err := hex.DecodeString(c.TplFrameHex); err == nil {
			fetchTplMu.Lock()
			fetchTpl = FetchTemplate{
				ServerAddr: c.TplServerAddr,
				PlayerID:   c.TplPlayerID,
				Page:       c.TplPage,
				Key:        c.TplKey,
				Counter:    c.TplCounter,
				Sub3:       c.TplSub3,
				Payload:    c.TplPayload,
				Frame:      f,
				GotHost:    c.TplGotHost,
				GotData:    c.TplGotData,
			}
			fetchTplMu.Unlock()
		}
	}
	loginFrameMu.Lock()
	fetchDebug("load: cwd=%v login=%d init=%d batch=%d tpl=%v", wd, len(loginFrame), len(initFrames), len(batchFrame), fetchTpl.GotData)
	loginFrameMu.Unlock()
}

// stashFetchTemplate 保存最近一条捕获到的 44871 翻页请求模板（仅 mode=00015f99，防握手/初始化帧污染）
func stashFetchTemplate(buf []byte, serverAddr string) {
	if len(buf) < 57 {
		return
	}
	if int(binary.BigEndian.Uint32(buf[4:8])) != fetchCmdID {
		return
	}
	if binary.BigEndian.Uint32(buf[44:48]) != 0x00015f99 {
		return
	}
	key := buf[55]
	payload := make([]byte, len(buf)-57)
	for i := range payload {
		payload[i] = buf[57+i] ^ key
	}

	fetchTplMu.Lock()
	fetchTpl = FetchTemplate{
		ServerAddr: serverAddr,
		PlayerID:   string(buf[12:44]),
		Page:       buf[51],
		Key:        key,
		Counter:    binary.BigEndian.Uint32(buf[48:52]),
		Sub3:       hex.EncodeToString(buf[52:55]),
		Payload:    string(payload),
		Frame:      append([]byte(nil), buf...),
		GotHost:    serverAddr != "",
		GotData:    true,
	}
	fetchTplMu.Unlock()
	saveFetchCache()
}

// stashLoginFrame 缓存最新捕获的建连握手/登录帧(每服务器仅保留最新一条，token会随重新登录轮换)
func stashLoginFrame(buf []byte, serverAddr string) {
	if len(buf) < 60 {
		return
	}
	if int(binary.BigEndian.Uint32(buf[4:8])) != fetchCmdID {
		return
	}
	// 握手帧特征: 版本字段[8:12]=0 且长度明显大于普通翻页帧
	if binary.BigEndian.Uint32(buf[8:12]) != 0 || len(buf) <= 128 {
		return
	}
	loginFrameMu.Lock()
	if len(loginFrame) > 0 && loginFrameHost == serverAddr {
		// 已缓存同服务器握手帧，仅在新token(玩家ID字段不同)时更新
		if string(loginFrame[12:44]) == string(buf[12:44]) {
			loginFrameMu.Unlock()
			return
		}
	}
	loginFrame = append([]byte(nil), buf...)
	loginFrameHost = serverAddr
	loginFrameMu.Unlock()
	saveFetchCache()
}

// stashInitFrame 收集每个连接"登录后初始化阶段"的44871指令帧(打开战报/切换同盟战报等)。
// 规则：每个连接握手帧后的前4个业务请求视为初始化窗口，窗口内的非翻页帧(非00015f99/非心跳)收集为初始化帧序列。
// 注意：init帧携带的sub3是会话动态的(每次重登都变)，因此每次登录都刷新缓存(不做每服务器只缓存一次)。
func stashInitFrame(buf []byte, serverAddr string, streamKey string) {
	if len(buf) < 57 {
		return
	}
	if int(binary.BigEndian.Uint32(buf[4:8])) != fetchCmdID {
		return
	}
	ver := binary.BigEndian.Uint32(buf[8:12])
	mode := binary.BigEndian.Uint32(buf[44:48])

	// 打开战报列表序列帧(000010ea打开战报/000002b6重置/00015f96打开同盟列表)——会话动态，始终用最新会话的序列刷新缓存
	if mode == 0x000010ea || mode == 0x000002b6 || mode == 0x00015f96 {
		initFramesMu.Lock()
		if ver == 0 {
			delete(initOpenSeq, streamKey)
		}
		seq := initOpenSeq[streamKey]
		if mode == 0x000010ea {
			// 新的打开动作：重置序列从头收集
			seq = [][]byte{append([]byte(nil), buf...)}
		} else {
			seq = append(seq, append([]byte(nil), buf...))
		}
		initOpenSeq[streamKey] = seq
		shouldSave := len(seq) >= 2 && mode != 0x000010ea
		if shouldSave {
			initFrames = append([][]byte(nil), seq...)
			initFramesHost = serverAddr
		}
		initFramesMu.Unlock()
		if shouldSave {
			saveFetchCache()
		}
	}

	initFramesMu.Lock()

	// 握手帧(ver=0 大帧): 重置该连接的初始化窗口
	if ver == 0 {
		if len(buf) > 128 {
			initWinCount[streamKey] = 0
			initCollect[streamKey] = nil
			initWinPage[streamKey] = nil
		}
		initFramesMu.Unlock()
		return
	}

	// 窗口内: 统计请求数，收集非翻页帧
	if initWinCount[streamKey] < 4 {
		initWinCount[streamKey]++
		if mode == 0x00015f99 {
			// 记录窗口内最后一条翻页帧(切同盟后的滑动请求，游标有效)
			initWinPage[streamKey] = append([]byte(nil), buf...)
		} else {
			initCollect[streamKey] = append(initCollect[streamKey], append([]byte(nil), buf...))
		}
		if initWinCount[streamKey] >= 4 {
			// 窗口结束，缓存初始化序列+同连接首个翻页帧(每次登录都刷新)
			shouldSave := false
			if len(initCollect[streamKey]) > 0 {
				initFrames = initCollect[streamKey]
				initFramesHost = serverAddr
				initFirstPage = initWinPage[streamKey]
				shouldSave = true
			}
			delete(initCollect, streamKey)
			delete(initWinCount, streamKey)
			delete(initWinPage, streamKey)
			initFramesMu.Unlock()
			if shouldSave {
				saveFetchCache()
			}
			return
		}
	}
	initFramesMu.Unlock()
}

// sendInitFrames 重放初始化指令帧序列(打开战报列表/切换同盟战报等)
func sendInitFrames(conn net.Conn, serverAddr string) (int, error) {
	initFramesMu.Lock()
	defer initFramesMu.Unlock()
	if len(initFrames) == 0 || initFramesHost != serverAddr {
		return 0, nil
	}
	for _, f := range initFrames {
		if _, err := conn.Write(f); err != nil {
			return 0, err
		}
	}
	return len(initFrames), nil
}

// getInitFirstPage 返回与初始化帧同连接捕获的首个翻页帧
func getInitFirstPage() []byte {
	initFramesMu.Lock()
	defer initFramesMu.Unlock()
	return append([]byte(nil), initFirstPage...)
}

// stashBatchFrame 缓存 mode=0000000a 批量战报详情请求帧(每服务器最新一条)
func stashBatchFrame(buf []byte, serverAddr string) {
	if len(buf) < 57 {
		return
	}
	if int(binary.BigEndian.Uint32(buf[4:8])) != fetchCmdID {
		return
	}
	if binary.BigEndian.Uint32(buf[44:48]) != 0x0000000a {
		return
	}
	batchFrameMu.Lock()
	batchFrame = append([]byte(nil), buf...)
	batchFrameHost = serverAddr
	batchFrameMu.Unlock()
	saveFetchCache()
}

// stashDetailFrame 缓存最近捕获的 mode=0000005c 战报详情请求帧
// 载荷格式: [[1, 2], [1, 3], [-1], <battle_id>, 0]，第4个元素即战报ID
func stashDetailFrame(buf []byte, serverAddr string) {
	if len(buf) < 57 {
		return
	}
	if int(binary.BigEndian.Uint32(buf[4:8])) != fetchCmdID {
		return
	}
	if binary.BigEndian.Uint32(buf[44:48]) != 0x0000005c {
		return
	}
	detailFrameMu.Lock()
	detailFrame = append([]byte(nil), buf...)
	detailFrameHost = serverAddr
	detailFrameMu.Unlock()
	saveFetchCache()
}

// getDetailFrameParams 返回最近捕获的 0000005c 详情帧的 版本/玩家ID/counter/sub3（供构造详情请求）
func getDetailFrameParams(serverAddr string) (ver, playerID []byte, counter uint32, sub3 []byte, ok bool) {
	detailFrameMu.Lock()
	defer detailFrameMu.Unlock()
	if len(detailFrame) == 0 {
		return nil, nil, 0, nil, false
	}
	f := detailFrame
	ver = append([]byte(nil), f[8:12]...)
	playerID = append([]byte(nil), f[12:44]...)
	counter = binary.BigEndian.Uint32(f[48:52])
	sub3 = append([]byte(nil), f[52:55]...)
	return ver, playerID, counter, sub3, true
}

// stashOpenPanelFrame 缓存最近捕获的 mode=000010e9 打开战报面板请求帧
func stashOpenPanelFrame(buf []byte, serverAddr string) {
	if len(buf) < 57 {
		return
	}
	if int(binary.BigEndian.Uint32(buf[4:8])) != fetchCmdID {
		return
	}
	if binary.BigEndian.Uint32(buf[44:48]) != 0x000010e9 {
		return
	}
	openPanelMu.Lock()
	openPanel = append([]byte(nil), buf...)
	openPanelHost = serverAddr
	openPanelMu.Unlock()
}

// getOpenPanelParams 返回最近捕获的 000010e9 打开战报面板帧的 玩家ID/sub3/主城ID
func getOpenPanelParams(serverAddr string) (playerID []byte, sub3 []byte, cityID int64, ok bool) {
	openPanelMu.Lock()
	defer openPanelMu.Unlock()
	if len(openPanel) == 0 || openPanelHost != serverAddr {
		return nil, nil, 0, false
	}
	f := openPanel
	playerID = append([]byte(nil), f[12:44]...)
	sub3 = append([]byte(nil), f[52:55]...)
	// 载荷: [55]XOR密钥 [56]05 [57:] 载荷^密钥 -> "[5841032, 0]"
	if len(f) > 57 && f[56] == 5 {
		key := f[55]
		body := xorbuf(f[57:], key)
		var arr []int64
		if json.Unmarshal(body, &arr) == nil && len(arr) > 0 {
			cityID = arr[0]
		}
	}
	return playerID, sub3, cityID, true
}

// buildOpenPanelRequest 构造 mode=000010e9 打开战报面板请求，载荷 [<主城ID>, 0]
func buildOpenPanelRequest(playerID string, counter uint32, sub3 []byte, cityID int64) []byte {
	key := byte(time.Now().UnixNano())
	if key == 0 {
		key = 0x5A
	}
	if len(sub3) != 3 {
		sub3 = []byte{0x09, 0x45, 0xc6} // 兜底: 打开面板子参数
	}
	if cityID <= 0 {
		cityID = 5841032
	}
	payload := "[" + strconv.FormatInt(cityID, 10) + ", 0]"
	body := []byte{}
	body = append(body, 0x00, 0x00, 0xAF, 0x47) // cmdId 44871
	body = append(body, fetchVersion()...)      // version
	body = append(body, []byte(playerID)...)    // player id 32
	body = append(body, 0x00, 0x00, 0x10, 0xE9) // mode 000010e9
	body = append(body, byte(counter>>24), byte(counter>>16), byte(counter>>8), byte(counter)) // 计数器
	body = append(body, sub3...)                // 3
	body = append(body, key)                    // XOR密钥
	body = append(body, 0x05)                   // 固定
	body = append(body, xorbuf([]byte(payload), key)...)

	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(body)))
	copy(frame[4:], body)
	return frame
}

// buildListResetRequest 构造 mode=000002b6 战报列表重置请求，载荷 []
func buildListResetRequest(playerID string, counter uint32, sub3 []byte) []byte {
	key := byte(time.Now().UnixNano())
	if key == 0 {
		key = 0x5A
	}
	if len(sub3) != 3 {
		sub3 = []byte{0x09, 0x45, 0xd5} // 兜底: 列表重置子参数
	}
	body := []byte{}
	body = append(body, 0x00, 0x00, 0xAF, 0x47) // cmdId 44871
	body = append(body, fetchVersion()...)      // version
	body = append(body, []byte(playerID)...)    // player id 32
	body = append(body, 0x00, 0x00, 0x02, 0xB6) // mode 000002b6
	body = append(body, byte(counter>>24), byte(counter>>16), byte(counter>>8), byte(counter)) // 计数器
	body = append(body, sub3...)                // 3
	body = append(body, key)                    // XOR密钥
	body = append(body, 0x05)                   // 固定
	body = append(body, xorbuf([]byte("[]"), key)...)

	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(body)))
	copy(frame[4:], body)
	return frame
}

// getBatchFrame 返回最近捕获的 mode=0000000a 批量详情请求帧
func getBatchFrame(serverAddr string) []byte {
	batchFrameMu.Lock()
	defer batchFrameMu.Unlock()
	if batchFrameHost != serverAddr {
		return nil
	}
	return append([]byte(nil), batchFrame...)
}

// GetFetchTemplate 返回模板信息
func GetFetchTemplate() string {
	fetchTplMu.Lock()
	defer fetchTplMu.Unlock()
	return global.Response{Data: fetchTpl}.Success()
}

func xorbuf(b []byte, k byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[i] = b[i] ^ k
	}
	return out
}

// fetchVersion 返回当前捕获帧的协议版本字段(模板帧存在时取帧内版本，否则用最近逆向到的版本)
// 直连会话绑定(98888解析)后优先用新会话版本
func fetchVersion() []byte {
	activeSessionMu.Lock()
	if activeSessionOK && len(activeSessionVer) == 4 {
		v := append([]byte(nil), activeSessionVer...)
		activeSessionMu.Unlock()
		return v
	}
	activeSessionMu.Unlock()
	fetchTplMu.Lock()
	defer fetchTplMu.Unlock()
	if fetchTpl.GotData && len(fetchTpl.Frame) >= 12 {
		return append([]byte(nil), fetchTpl.Frame[8:12]...)
	}
	return []byte{0x00, 0x00, 0x32, 0xDB} // 当前游戏版本 000032DB
}

// parseLoginAck 解析 98888 登录确认帧，提取连接级会话身份。
// 帧布局(真实样本 len=56): [4:8]=cmd 98888 [12:16]=version [16]=0x00 [17:20]=counter seed(3字节大端) [20:52]=playerID(32字节ASCII hex) [52:56]=version重复
// 实测: 每条新连接/每次重登 98888 都会下发新的 playerID 和 seed，首业务帧 counter=seed+4。
func parseLoginAck(f []byte) (ver []byte, pid []byte, seed uint32, ok bool) {
	if len(f) < 56 {
		return nil, nil, 0, false
	}
	if binary.BigEndian.Uint32(f[4:8]) != 98888 {
		return nil, nil, 0, false
	}
	v1 := f[12:16]
	v2 := f[52:56]
	if !bytes.Equal(v1, v2) {
		return nil, nil, 0, false
	}
	seed = uint32(f[17])<<16 | uint32(f[18])<<8 | uint32(f[19])
	pid = append([]byte(nil), f[20:52]...)
	for _, b := range pid {
		if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
			return nil, nil, 0, false
		}
	}
	return append([]byte(nil), v1...), pid, seed, true
}

// bindActiveSession 绑定直连连接的会话身份(来自98888)，之后所有业务帧用新身份构造
func bindActiveSession(ver []byte, pid []byte, seed uint32) {
	activeSessionMu.Lock()
	activeSessionPID = string(pid)
	activeSessionVer = append([]byte(nil), ver...)
	activeSessionSeed = seed
	activeSessionOK = true
	activeSessionMu.Unlock()
}

// buildPageRequest 构造 44871 翻页请求。
// 布局(逆向自抓包): [4:8]cmdId [8:12]版本 [12:44]玩家ID [44:48]模式 [48:52]计数器 [52:55]子参数 [55]XOR密钥 [56]05 [57:]载荷
// mode: 1=首次打开战报列表(空列表重置,载荷"[]") 0=常规游标翻页
func buildPageRequest(playerID string, counter uint32, mode int, cursor string, sub3 []byte) []byte {
	key := byte(time.Now().UnixNano())
	if key == 0 {
		key = 0x5A
	}

	var mode44 []byte
	if mode == 1 {
		mode44 = []byte{0x00, 0x00, 0x02, 0xB6} // 重置/打开列表
	} else {
		mode44 = []byte{0x00, 0x01, 0x5F, 0x99} // 常规翻页
	}
	if len(sub3) != 3 {
		sub3 = []byte{0x09, 0xb9, 0x1b} // 兜底: 翻页子参数
	}

	body := []byte{}
	body = append(body, 0x00, 0x00, 0xAF, 0x47)         // cmdId 44871
	body = append(body, fetchVersion()...)               // version(取当前捕获帧的版本)
	body = append(body, []byte(playerID)...)            // player id 32
	body = append(body, mode44...)                      // 4
	body = append(body, byte(counter>>24), byte(counter>>16), byte(counter>>8), byte(counter)) // 计数器 4
	body = append(body, sub3...)                        // 3
	body = append(body, key)                            // XOR密钥
	body = append(body, 0x05)                           // 固定
	body = append(body, xorbuf([]byte(cursor), key)...) // 载荷^密钥

	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(body)))
	copy(frame[4:], body)
	return frame
}

// buildDetailRequest 构造 mode=0000005c 战报详情请求。
// 载荷: [[1, 2], [1, 3], [-1], <battle_id>, 0]，响应为 cmd=92 完整战报(zlib JSON)
func buildDetailRequest(playerID string, counter uint32, sub3 []byte, battleID int64) []byte {
	key := byte(time.Now().UnixNano())
	if key == 0 {
		key = 0x5A
	}
	if len(sub3) != 3 {
		sub3 = []byte{0x09, 0xb8, 0x47} // 兜底: 详情子参数
	}

	payload := "[[1, 2], [1, 3], [-1], " + strconv.FormatInt(battleID, 10) + ", 0]"
	body := []byte{}
	body = append(body, 0x00, 0x00, 0xAF, 0x47) // cmdId 44871
	body = append(body, fetchVersion()...)      // version
	body = append(body, []byte(playerID)...)    // player id 32
	body = append(body, 0x00, 0x00, 0x00, 0x5C) // mode 0000005c
	body = append(body, byte(counter>>24), byte(counter>>16), byte(counter>>8), byte(counter)) // 计数器
	body = append(body, sub3...)                // 3
	body = append(body, key)                    // XOR密钥
	body = append(body, 0x05)                   // 固定
	body = append(body, xorbuf([]byte(payload), key)...)

	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(body)))
	copy(frame[4:], body)
	return frame
}

// buildBatchRequest 构造 mode=0000000a 批量战报详情请求(详情0000005c的前置，游戏成功模式: 0000000a->cmd=10, 0000005c->cmd=92)。
// payload(地图坐标组)与XOR密钥直接复用最近捕获的批量帧，只改写 玩家ID/counter/sub3/版本。
func buildBatchRequest(playerID string, counter uint32, sub3 []byte, tpl []byte) []byte {
	var key byte = 0x5A
	var payload []byte
	if len(tpl) > 57 && tpl[56] == 5 {
		key = tpl[55]
		payload = tpl[57:]
	}
	if len(sub3) != 3 {
		sub3 = []byte{0x09, 0xb8, 0x47} // 兜底: 批量子参数
	}

	body := []byte{}
	body = append(body, 0x00, 0x00, 0xAF, 0x47) // cmdId 44871
	body = append(body, fetchVersion()...)      // version
	body = append(body, []byte(playerID)...)    // player id 32
	body = append(body, 0x00, 0x00, 0x00, 0x0A) // mode 0000000a
	body = append(body, byte(counter>>24), byte(counter>>16), byte(counter>>8), byte(counter)) // 计数器
	body = append(body, sub3...)                // 3
	body = append(body, key)                    // XOR密钥(复用原帧，payload已按此密钥加密)
	body = append(body, 0x05)                   // 固定
	body = append(body, payload...)

	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(body)))
	copy(frame[4:], body)
	return frame
}

// getDetailBattleID 从最近捕获的 0000005c 详情请求帧中提取 battle_id(游戏真实查看的战报，保证有效)
func getDetailBattleID() int64 {
	detailFrameMu.Lock()
	defer detailFrameMu.Unlock()
	if len(detailFrame) < 57 || detailFrame[56] != 5 {
		return 0
	}
	key := detailFrame[55]
	body := xorbuf(detailFrame[57:], key)
	// 载荷: [[1,2],[1,3],[-1],<battle_id>,0]
	var arr []any
	if json.Unmarshal(body, &arr) != nil || len(arr) < 4 {
		return 0
	}
	if id, ok := arr[3].(float64); ok && id > 0 {
		return int64(id)
	}
	return 0
}

// parseList2100ID 从 cmd=2100 响应(XOR 0x98 解密后)中提取首字段ID。
// 注意: 2100 是"盟聊/世界频道"消息列表(实测数据为聊天内容+玩家名+时间戳)，
// 首字段是消息ID，不是战报ID! 战报ID只能从 90005 的 Tb_battle_report 或 cmd=92 详情中获取。
func parseList2100ID(body []byte) int64 {
	var rec []any
	if json.Unmarshal(body, &rec) != nil || len(rec) == 0 {
		return 0
	}
	if id, ok := rec[0].(float64); ok && id > 0 {
		return int64(id)
	}
	return 0
}

// parseReportListIDs 从 90005 表同步响应体(XOR 0x98 解密后)中提取战报记录ID列表。
// 格式: [[op,"Tb_battle_report_xxx",[id,...]],...]，首个字段即战报ID
func parseReportListIDs(body []byte) []int64 {
	var records [][]any
	if err := json.Unmarshal(body, &records); err != nil {
		return nil
	}
	var ids []int64
	for _, rec := range records {
		if len(rec) < 3 {
			continue
		}
		name, _ := rec[1].(string)
		if !strings.HasPrefix(name, "Tb_battle_report_") {
			continue
		}
		fields, _ := rec[2].([]any)
		if len(fields) > 0 {
			if id, ok := fields[0].(float64); ok {
				ids = append(ids, int64(id))
			}
		}
	}
	return ids
}

// sendLoginFrame 若已捕获到握手帧则先重放(直连必须带登录token)
func sendLoginFrame(conn net.Conn, serverAddr string) (bool, error) {
	loginFrameMu.Lock()
	defer loginFrameMu.Unlock()
	if len(loginFrame) == 0 || loginFrameHost != serverAddr {
		return false, nil
	}
	if _, err := conn.Write(loginFrame); err != nil {
		return false, err
	}
	return true, nil
}

// drainResponses 读取并丢弃一段时间的响应帧（用于握手后等待登录流程完成），返回收到的帧摘要
func drainResponses(r *bufio.Reader, conn net.Conn, dur time.Duration) []map[string]interface{} {
	conn.SetReadDeadline(time.Now().Add(dur))
	var frames []map[string]interface{}
	for {
		f, err := readFrame(r)
		if err != nil {
			break
		}
		cmd := int(binary.BigEndian.Uint32(f[4:8]))
		info := map[string]interface{}{"cmdId": cmd, "len": len(f)}
		if len(f) > 12 {
			info["type"] = f[12]
		}
		if cmd == 98888 {
			info["raw"] = hex.EncodeToString(f)
		}
		frames = append(frames, info)
	}
	return frames
}

// loginSeqToCmdMap 登录响应帧序列 -> cmdId计数map
func loginSeqToCmdMap(seq []map[string]interface{}) map[int]int {
	m := map[int]int{}
	for _, f := range seq {
		if cmd, ok := f["cmdId"].(int); ok {
			m[cmd]++
		}
	}
	return m
}

// readFrame 读取一帧 [4字节长度][长度字节]
func readFrame(r *bufio.Reader) ([]byte, error) {
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}
	sz := int(binary.BigEndian.Uint32(hdr))
	if sz < 4 || sz > 4*1024*1024 {
		return nil, errors.New("bad frame size " + strconv.Itoa(sz))
	}
	frame := make([]byte, sz)
	if _, err := io.ReadFull(r, frame); err != nil {
		return nil, err
	}
	return append(hdr, frame...), nil
}

func emitDirectFetch(ev string, data map[string]interface{}) {
	if global.AppCtx != nil {
		runtime.EventsEmit(global.AppCtx, ev, data)
	}
}

// DirectFetchTest 直连服务器重放最近一条请求，验证协议是否可复现（返回响应帧摘要）
func DirectFetchTest() (result string) {
	defer func() {
		if r := recover(); r != nil {
			fetchDebug("test: PANIC %v", r)
			result = global.Response{Message: "直连测试内部错误: " + fmt.Sprint(r)}.Error()
		}
	}()
	fetchTplMu.Lock()
	tpl := fetchTpl
	fetchTplMu.Unlock()
	if !tpl.GotHost || !tpl.GotData {
		return global.Response{Message: "未捕获到请求模板/服务器地址，请先在游戏内滚动一次战报列表"}.Error()
	}

	fetchDebug("test: begin server=%v tplFrame=%d", tpl.ServerAddr, len(tpl.Frame))
	conn, err := net.DialTimeout("tcp", tpl.ServerAddr, 5*time.Second)
	if err != nil {
		return global.Response{Message: "连接失败: " + err.Error()}.Error()
	}
	defer conn.Close()
	conn.SetDeadline(time.Time{})
	r := bufio.NewReader(conn)

	// 先重放握手/登录帧
	handshook, err := sendLoginFrame(conn, tpl.ServerAddr)
	if err != nil {
		return global.Response{Message: "握手帧发送失败: " + err.Error()}.Error()
	}
	fetchDebug("test: handshake sent=%v", handshook)
	handshakeMsg := ""
	if handshook {
		handshakeMsg = "已发送握手帧(登录token)"
	} else {
		handshakeMsg = "未捕获到握手帧，仅原样重放翻页请求"
	}

	var frames []map[string]interface{}
	appendSeq := func(seq []map[string]interface{}, phase string) {
		for _, lf := range seq {
			info := map[string]interface{}{"cmdId": lf["cmdId"], "len": lf["len"]}
			if t, ok := lf["type"]; ok {
				info["type"] = t
			}
			info["phase"] = phase
			frames = append(frames, info)
		}
	}

	// ---- S1: 重放登录帧(无握手帧时连接可能直接被关，仍继续尝试读响应) ----
	// 登录同步(98888/99991/90005 Tb_user_res...)等待"空闲窗口≥1200ms"才算结束，最长10s
	loginSeq := drainResponses(r, conn, 1200*time.Millisecond)
	syncStart := time.Now()
	for time.Since(syncStart) < 10*time.Second {
		more := drainResponses(r, conn, 1200*time.Millisecond)
		if len(more) == 0 {
			break // 服务器方向连续空闲1200ms，登录同步结束
		}
		loginSeq = append(loginSeq, more...)
	}
	appendSeq(loginSeq, "login")
	fetchDebug("test: login响应 %d 帧", len(loginSeq))

	// ---- S2: 解析 98888 并绑定连接级会话身份 ----
	// 实测: playerID 和 counter seed 每次登录都会变，必须用本连接的 98888 值构造业务帧，
	// 复用旧模板身份会被服务器静默忽略(业务帧无响应)。
	var ackVer, ackPID []byte
	var ackSeed uint32
	sessionBound := false
	for _, lf := range loginSeq {
		if lf["cmdId"] == 98888 {
			if raw, ok := lf["raw"].(string); ok {
				fb, err := hex.DecodeString(raw)
				if err == nil {
					v, p, s, ok := parseLoginAck(fb)
					if ok {
						ackVer, ackPID, ackSeed = v, p, s
						sessionBound = true
					}
				}
			}
		}
	}
	fetchDebug("test: 98888解析 session_bound=%v seed=%d pid=%s", sessionBound, ackSeed, string(ackPID))
	if !sessionBound {
		return global.Response{Message: "未收到/解析失败 98888 登录确认帧，不发业务请求(防旧身份复用)"}.Error()
	}
	bindActiveSession(ackVer, ackPID, ackSeed)
	playerID := string(ackPID)
	// 首业务帧 counter = seed+4 (实测: 13:41:59 seed=0xef31bf, 13:42:00 首业务帧=0xef31c3)
	// 但若游戏连接仍在运行且 counter 已超过 seed(实测 13:10:00 seed=0x763002 < 游戏 0x8c7f87)，
	// 服务器可能按账号校验 counter 必须 ≥ 最新值，此时应接续游戏 counter 而非 seed。
	counter := ackSeed + 3 // 发送前会 ++，首个实际发送帧 = seed+4
	if tpl.Counter > counter {
		counter = tpl.Counter // 接续游戏最新 counter，避免被服务器判定为 counter 回退
	}
	fetchDebug("test: counter起点 seed=%d tpl.Counter=%d 实际起点=%d", ackSeed, tpl.Counter, counter)

	// ---- 不再重放旧连接的 init 帧(原始帧带旧连接身份，重放有害)；成功模式无需init ----

	// 收集 90005 表同步中的战报ID
	var battleIDs []int64
	seenIDs := map[int64]bool{}
	collectIDs := func(body []byte) {
		for _, id := range parseReportListIDs(body) {
			if !seenIDs[id] {
				seenIDs[id] = true
				battleIDs = append(battleIDs, id)
			}
		}
	}

	sawCmd := map[int]bool{}
	gotCmd10 := false
	gotCmd92 := 0
	readWindow := func(dur time.Duration, phase string) {
		conn.SetReadDeadline(time.Now().Add(dur))
		for {
			f, err := readFrame(r)
			if err != nil {
				return
			}
			cmdId := int(binary.BigEndian.Uint32(f[4:8]))
			sawCmd[cmdId] = true
			if cmdId == 10 {
				gotCmd10 = true
			}
			if cmdId == 92 {
				gotCmd92++
			}
			if cmdId == 90005 && len(f) > 13 && f[12] == 5 {
				collectIDs(xorbuf(f[13:], 152))
			}
			if cmdId == 2100 || cmdId == 2200 {
				continue // 聊天消息帧(盟聊/世界频道)，测试输出过滤
			}
			info := describeResponseFrame(f)
			info["phase"] = phase
			frames = append(frames, info)
			if len(frames) >= 80 {
				return
			}
		}
	}
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

	detailSub3 := []byte{0x01, 0xeb, 0x18} // 详情类(0000000a/0000005c)mode专属子参数，兜底
	if _, _, _, ds3, ok := getDetailFrameParams(tpl.ServerAddr); ok && len(ds3) == 3 {
		detailSub3 = ds3
	}
	batchMsg := "未捕获到0000000a批量帧，跳过批量前置"
	if bf := getBatchFrame(tpl.ServerAddr); len(bf) > 0 {
		// sub3 是会话级动态字段(每次登录都变: 15:57=01eb18, 13:01=02f5xx)，
		// 必须用批量帧自己捕获时的 sub3(只有缓存帧来自当前会话时才有效)
		bfSub3 := []byte{0x01, 0xeb, 0x18}
		if len(bf) > 55 {
			bfSub3 = bf[52:55]
		}
		counter++
		br := buildBatchRequest(playerID, counter, bfSub3, bf)
		conn.Write(br)
		fetchDebug("test: 批量前置(0000000a) counter=%d sub3=%x", counter, bfSub3)
		before := len(frames)
		readWindow(3*time.Second, "batch")
		if gotCmd10 {
			batchMsg = "批量前置(0000000a)已接受，收到cmd=10"
		} else {
			batchMsg = "批量前置(0000000a)未收到cmd=10，停止后续请求"
			fetchDebug("test: 批量前置失败 停止 sawCmd=%v", sawCmd)
			return global.Response{Data: frames, Message: "0000000A 未收到 cmd=10: " + batchMsg + "（" + handshakeMsg + "，session_bound=" + strconv.FormatBool(sessionBound) + "）"}.Success()
		}
		_ = before
	} else {
		fetchDebug("test: 未捕获到0000000a批量帧，跳过批量前置")
	}

	// ---- S4: 详情阶段: 0000005C(0) 零锚点 -> 0000005C(真实锚点) ----
	// 成功模式(第三轮/15:57抓包): 0000000A -> 0000005C(0) -> 0000005C(real anchor) -> cmd=92
	detailGot92 := 0
	realAnchor := int64(0)
	if bid := getDetailBattleID(); bid > 0 {
		realAnchor = bid // 游戏真实查看的战报ID(保证有效)
	}
	anchors := []int64{0, realAnchor}
	for _, a := range anchors {
		if a < 0 {
			continue
		}
		counter++
		dr := buildDetailRequest(playerID, counter, detailSub3, a)
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		conn.Write(dr)
		fetchDebug("test: 详情(0000005c) anchor=%d counter=%d", a, counter)
		before := len(frames)
		readWindow(3*time.Second, "detail")
		for _, fi := range frames[before:] {
			if fi["cmdId"] == 92 {
				detailGot92++
			}
		}
		if detailGot92 > 0 {
			break // 已拿到战报详情
		}
	}
	fetchDebug("test: 详情完成 收到92=%d sawCmd=%v battleIDs=%v", detailGot92, sawCmd, battleIDs)
	fetchDebug("test: 完成 共 %d 帧", len(frames))

	msg := "收到 " + strconv.Itoa(len(frames)) + " 个响应帧，cmd=92 " + strconv.Itoa(detailGot92) + " 条（" + handshakeMsg + "，session_bound=" + strconv.FormatBool(sessionBound) + "，seed=" + strconv.FormatUint(uint64(ackSeed), 10) + "，batch=" + batchMsg + "，本次为纯测试，未入库）"
	if len(frames) == 0 {
		msg = "已连接但未收到任何响应帧（" + handshakeMsg + "）"
		return global.Response{Message: msg}.Error()
	}
	return global.Response{Data: frames, Message: msg}.Success()
}

// describeResponseFrame 生成响应帧的可读摘要(解码type3 zlib/type5 XOR152内容)
func describeResponseFrame(f []byte) map[string]interface{} {
	cmdId := int(binary.BigEndian.Uint32(f[4:8]))
	info := map[string]interface{}{"cmdId": cmdId, "len": len(f)}
	if len(f) > 12 {
		info["type"] = f[12]
	}
	if len(f) > 17 && f[12] == 3 {
		body := parseZlibData(f[17:])
		if len(body) > 0 {
			var jsondata [][]any
			if json.Unmarshal(body, &jsondata) == nil {
				var times []int64
				var bids []int64
				for _, v := range jsondata {
					if len(v) == 0 {
						continue
					}
					bj, _ := json.Marshal(v[0])
					var rep model.Report
					json.Unmarshal(bj, &rep)
					times = append(times, int64(rep.Time))
					bids = append(bids, int64(rep.BattleID))
				}
				info["report_count"] = len(times)
				if len(times) > 0 {
					info["first_time"] = times[0]
					info["last_time"] = times[len(times)-1]
					info["first_battle_id"] = bids[0]
				}
			}
			if len(body) > 800 {
				info["body_head"] = string(body[:800])
			} else {
				info["body"] = string(body)
			}
		}
	}
	if len(f) > 13 && f[12] == 5 {
		body := xorbuf(f[13:], 152)
		if len(body) > 300 {
			info["body_head"] = string(body[:300])
		} else {
			info["body"] = string(body)
		}
	}
	return info
}

// DirectFetchStop 停止直连拉取
func DirectFetchStop() string {
	if !fetchRunning {
		return global.Response{Message: "没有正在运行的拉取任务"}.Success()
	}
	close(fetchStop)
	return global.Response{Message: "已请求停止拉取"}.Success()
}

// DirectFetchLoop 免翻页直连拉取战报
func DirectFetchLoop(targetTime int64) string {
	if fetchRunning {
		return global.Response{Message: "已有一个拉取任务在运行"}.Error()
	}
	fetchTplMu.Lock()
	tpl := fetchTpl
	fetchTplMu.Unlock()
	if !tpl.GotHost || !tpl.GotData {
		return global.Response{Message: "未捕获到请求模板，请先在游戏内滚动一次战报列表"}.Error()
	}

	fetchRunning = true
	fetchStop = make(chan struct{})
	defer func() { fetchRunning = false }()

	go func() {
		var reportCount int64
	counter := tpl.Counter + 10000 // 大偏移避开与游戏并发会话的counter撞车(游戏约27/s递增)
		first := true
		var sub3 []byte
		if tpl.Sub3 != "" {
			sub3, _ = hex.DecodeString(tpl.Sub3)
		}
		detailSub3 := sub3
		if _, _, _, ds3, ok := getDetailFrameParams(tpl.ServerAddr); ok && len(ds3) == 3 {
			detailSub3 = ds3
		}

		conn, err := net.DialTimeout("tcp", tpl.ServerAddr, 5*time.Second)
		if err != nil {
			emitDirectFetch("directFetchError", map[string]interface{}{"msg": "连接失败: " + err.Error()})
			return
		}
		defer conn.Close()

		// 先重放握手/登录帧
		handshook, err := sendLoginFrame(conn, tpl.ServerAddr)
		if err != nil {
			emitDirectFetch("directFetchError", map[string]interface{}{"msg": "握手帧发送失败: " + err.Error()})
			return
		}
		if handshook {
			// 等登录响应序列(90007/98888/99991/90005)完成后再开始翻页
			r := bufio.NewReader(conn)
			loginSeq := drainResponses(r, conn, 1500*time.Millisecond)
			if len(loginSeq) < 2 {
				loginSeq = append(loginSeq, drainResponses(r, conn, 1500*time.Millisecond)...)
			}
			emitDirectFetch("directFetchProgress", map[string]interface{}{
				"page":      0,
				"msg":       "登录流程完成(收到 " + strconv.Itoa(len(loginSeq)) + " 个登录响应帧)，开始拉取战报",
				"cmdSeen":   loginSeqToCmdMap(loginSeq),
				"targetTime": targetTime,
			})
		}

		// 重放初始化指令帧(打开战报列表/切换同盟战报)并等待其处理完成
		r := bufio.NewReader(conn)
		initSent, err := sendInitFrames(conn, tpl.ServerAddr)
		if err != nil {
			emitDirectFetch("directFetchError", map[string]interface{}{"msg": "初始化帧发送失败: " + err.Error()})
			return
		}
		if initSent > 0 {
			initSeq := drainResponses(r, conn, 4000*time.Millisecond)
			emitDirectFetch("directFetchProgress", map[string]interface{}{
				"page":      0,
				"msg":       "已重放 " + strconv.Itoa(initSent) + " 个初始化指令帧(打开战报/切同盟，收到 " + strconv.Itoa(len(initSeq)) + " 个响应帧)，开始翻页",
				"cmdSeen":   loginSeqToCmdMap(initSeq),
				"targetTime": targetTime,
			})
		}

		seenIDs := map[int64]bool{}
		minTime := int64(0)
		emptyRounds := 0

		// 打开战报面板: 000010e9 [主城ID,0] -> 90008, 000002b6 [] -> 694。
		// 详情请求(0000005c)能返回 cmd=92 的前置条件。
		if opPID, _, cityID, hasOpen := getOpenPanelParams(tpl.ServerAddr); hasOpen && string(opPID) == tpl.PlayerID {
			counter++
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			conn.Write(buildOpenPanelRequest(tpl.PlayerID, counter, []byte{0x09, 0x45, 0xc6}, cityID))
			drainResponses(r, conn, 1500*time.Millisecond)
			counter++
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			conn.Write(buildListResetRequest(tpl.PlayerID, counter, []byte{0x09, 0x45, 0xd5}))
			drainResponses(r, conn, 1500*time.Millisecond)
		}

		// 锚点翻页状态: anchor=下一个要请求的battle_id，0=取最新(与游戏一致)
		anchor := int64(0)
		if bid := getDetailBattleID(); bid > 0 {
			// 优先用游戏真实查看的战报ID(实测必须用真实ID，9999999999会被90007拒绝)
			anchor = bid
		}
		anchorOK := true
		anchorRounds := 0

		for {
			select {
			case <-fetchStop:
				emitDirectFetch("directFetchStopped", map[string]interface{}{"reportCount": reportCount})
				return
			default:
			}

			var req []byte
			if first && anchorOK {
				// 首次: 直接发超大锚点详情请求取最新战报(不再重放过期帧)
				first = false
			}
			if len(req) == 0 && anchorOK {
				counter++
				req = buildDetailRequest(tpl.PlayerID, counter, detailSub3, anchor)
			}
			if len(req) == 0 {
				// 锚点翻页已到头，用翻页心跳保持会话(无实际数据)
				counter++
				req = buildPageRequest(tpl.PlayerID, counter, 0, strconv.FormatUint(uint64(rand.Uint32()), 10), sub3)
			}

			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := conn.Write(req); err != nil {
				emitDirectFetch("directFetchError", map[string]interface{}{"msg": "发送失败: " + err.Error()})
				return
			}

			// 读响应直到超时(2.5s)：收集 cmd=92 战报 与 90005 表同步中的战报ID
			conn.SetReadDeadline(time.Now().Add(2500 * time.Millisecond))
			reachedTarget := false
			roundNew := 0
			seenCmd := map[int]int{}
			for {
				f, err := readFrame(r)
				if err != nil {
					break // 超时或EOF
				}
				cmd := int(binary.BigEndian.Uint32(f[4:8]))
				seenCmd[cmd]++
				if cmd == 92 && len(f) > 17 && f[12] == 3 {
					body := parseZlibData(f[17:])
					if len(body) == 0 {
						continue
					}
					// 入库（解析为战报）
					parseBattleData(body)
					roundNew += countReportsInBody(body)
					mt := minReportTime(body)
					if mt > 0 && (minTime == 0 || mt < minTime) {
						minTime = mt
					}
					if targetTime > 0 && minTime > 0 && minTime < targetTime {
						reachedTarget = true
					}
					newBid := reportMinBattleID(body)
					if newBid > 0 && newBid < anchor {
						anchor = newBid
						anchorRounds = 0
					}
				} else if cmd == 90005 && len(f) > 13 && f[12] == 5 {
					body := xorbuf(f[13:], 152)
					for _, id := range parseReportListIDs(body) {
						if !seenIDs[id] {
							seenIDs[id] = true
						}
					}
				}
			}

			// 锚点推进统计: 本轮无新战报则计数
			if roundNew == 0 {
				anchorRounds++
				if anchorRounds >= 3 {
					anchorOK = false
				}
			}

			reportCount += int64(roundNew)

			emitDirectFetch("directFetchProgress", map[string]interface{}{
				"page":        counter,
				"reportCount": reportCount,
				"cursor":      strconv.FormatInt(minTime, 10),
				"cmdSeen":     seenCmd,
				"newReports":  roundNew,
				"newIDs":      len(seenIDs),
				"targetTime":  targetTime,
			})

			if reachedTarget {
				emitDirectFetch("directFetchDone", map[string]interface{}{
					"reason":      "timeReached",
					"reportCount": reportCount,
				})
				return
			}
			if roundNew == 0 {
				emptyRounds++
				if emptyRounds >= 3 {
					emitDirectFetch("directFetchDone", map[string]interface{}{
						"reason":      "noMoreData",
						"reportCount": reportCount,
					})
					return
				}
			} else {
				emptyRounds = 0
			}
			time.Sleep(300 * time.Millisecond)
		}
	}()
	return global.Response{Message: "已启动免翻页拉取，请观察进度"}.Success()
}

// countReportsInBody 统计 cmd=92 战报体中的战报条数
func countReportsInBody(body []byte) int {
	var jsondata [][]any
	if json.Unmarshal(body, &jsondata) != nil {
		return 0
	}
	return len(jsondata)
}

// minReportTime 返回 cmd=92 战报体中最小的战报时间(用于边界判断)
func minReportTime(body []byte) int64 {
	var jsondata [][]any
	if json.Unmarshal(body, &jsondata) != nil {
		return 0
	}
	var minTime int64
	for _, v := range jsondata {
		if len(v) == 0 {
			continue
		}
		bj, _ := json.Marshal(v[0])
		var rep model.Report
		json.Unmarshal(bj, &rep)
		if minTime == 0 || int64(rep.Time) < minTime {
			minTime = int64(rep.Time)
		}
	}
	return minTime
}

// reportMaxBattleID 返回 cmd=92 战报体中的最小battle_id(用于锚点翻页取下一页)
func reportMinBattleID(body []byte) int64 {
	var jsondata [][]any
	if json.Unmarshal(body, &jsondata) != nil {
		return 0
	}
	var minID int64
	for _, v := range jsondata {
		if len(v) == 0 {
			continue
		}
		bj, _ := json.Marshal(v[0])
		var rep model.Report
		json.Unmarshal(bj, &rep)
		if minID == 0 || int64(rep.BattleID) < minID {
			minID = int64(rep.BattleID)
		}
	}
	return minID
}