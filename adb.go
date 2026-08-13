package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"stzbHelper/global"
	"stzbHelper/model"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	adbMu           sync.Mutex
	adbCancelChan   chan struct{}
	adbStopMutex    sync.Mutex
	adbPathCache    string
	adbDeviceCache  string
	adbModeRunning  bool
)

var emulatorPorts = []int{5555, 5554, 5556, 16384, 16416, 7555, 62001, 62025, 21503}

// findAdbPath 查找 adb 可执行文件（PATH 或常见模拟器自带路径）
func findAdbPath() string {
	if adbPathCache != "" {
		return adbPathCache
	}
	if p, err := exec.LookPath("adb"); err == nil {
		adbPathCache = p
		return p
	}
	candidates := []string{
		`C:\leidian\LDPlayer9\adb.exe`,
		`C:\LDPlayer\LDPlayer9\adb.exe`,
		`D:\leidian\LDPlayer9\adb.exe`,
		`D:\LDPlayer\LDPlayer9\adb.exe`,
		`C:\Program Files\Netease\MuMu Player 12\shell\adb.exe`,
		`C:\Program Files\Netease\MuMuPlayer-12.0\shell\adb.exe`,
		`C:\Program Files\MuMuPlayer\shell\adb.exe`,
		`D:\Program Files\Netease\MuMu Player 12\shell\adb.exe`,
		`C:\Program Files\Nox\bin\adb.exe`,
		`D:\Program Files\Nox\bin\adb.exe`,
		`C:\Program Files (x86)\Microvirt\MEmu\adb.exe`,
		`C:\Program Files\BlueStacks_nxt\HD-Adb.exe`,
		`C:\Program Files\BlueStacks\HD-Adb.exe`,
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			adbPathCache = c
			return c
		}
	}
	return ""
}

func adbRunTimeout(timeout time.Duration, args ...string) (string, error) {
	adb := findAdbPath()
	if adb == "" {
		return "", fmt.Errorf("未找到 adb，请安装 adb 或使用自带 adb 的模拟器（雷电/MuMu/夜神）")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, adb, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW 隐藏adb.exe控制台窗口
	out, err := cmd.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), err
	}
	return strings.TrimSpace(string(out)), nil
}

func adbRun(args ...string) (string, error) {
	return adbRunTimeout(15*time.Second, args...)
}

// findEmulatorDevice 检测并连接模拟器设备
func findEmulatorDevice() string {
	if adbDeviceCache != "" {
		return adbDeviceCache
	}
	if listDevices := parseAdbDevices(); len(listDevices) > 0 {
		adbDeviceCache = listDevices[0]
		return adbDeviceCache
	}
	for _, port := range emulatorPorts {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		adbRunTimeout(3*time.Second, "connect", addr)
		for _, dev := range parseAdbDevices() {
			if strings.HasPrefix(dev, addr) {
				adbDeviceCache = dev
				return adbDeviceCache
			}
		}
	}
	return ""
}

// parseAdbDevices 解析 adb devices 输出，返回所有在线设备
func parseAdbDevices() []string {
	out, _ := adbRun("devices")
	var devices []string
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if i == 0 {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == "device" {
			devices = append(devices, fields[0])
		}
	}
	return devices
}

// adbScreenSize 获取当前显示方向下的屏幕尺寸（宽x高）
// 注意：wm size 返回物理尺寸，横屏游戏下与实际坐标系统不一致，
// 因此优先通过截图 PNG 头解析实际显示方向，wm size 仅作兜底
func adbScreenSize() (int, int) {
	if w, h, err := adbShotSize(); err == nil && w > 0 && h > 0 {
		return w, h
	}
	device := findEmulatorDevice()
	if device != "" {
		out, _ := adbRun("-s", device, "shell", "wm", "size")
		m := regexp.MustCompile(`(\d+)x(\d+)`).FindStringSubmatch(out)
		if len(m) == 3 {
			w, err1 := strconv.Atoi(m[1])
			h, err2 := strconv.Atoi(m[2])
			if err1 == nil && err2 == nil && w > 0 && h > 0 {
				return w, h
			}
		}
	}
	return 1280, 720
}

// adbShotSize 通过截图 PNG 头（IHDR）解析实际显示方向下的屏幕尺寸
func adbShotSize() (int, int, error) {
	device := findEmulatorDevice()
	if device == "" {
		return 0, 0, fmt.Errorf("未检测到模拟器")
	}
	cmd := exec.Command(findAdbPath(), "-s", device, "exec-out", "screencap", "-p")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, err
	}
	if len(out) < 24 {
		return 0, 0, fmt.Errorf("截图数据过短")
	}
	w := int(out[16])<<24 | int(out[17])<<16 | int(out[18])<<8 | int(out[19])
	h := int(out[20])<<24 | int(out[21])<<16 | int(out[22])<<8 | int(out[23])
	return w, h, nil
}

// adbSwipeUp 从下往上滑动一页
// durationMs: 滑动时长(ms)，不设限制；distance: 滑动距离(像素)
func adbSwipeUp(w, h int, durationMs float64, distance int) error {
	device := findEmulatorDevice()
	if device == "" {
		return fmt.Errorf("未检测到模拟器")
	}
	x := w / 2
	y1 := int(float64(h) * 0.80)
	y2 := y1 - distance
	if y2 < 0 {
		y2 = 0
	}
	dur := strconv.FormatFloat(durationMs, 'f', -1, 64)
	if durationMs < 100 {
		// 时长过短的滑动会被模拟器识别为点击，误入战报详情页，保底 100ms
		dur = "100"
	}
	_, err := adbRun("-s", device, "shell", "input", "swipe",
		strconv.Itoa(x), strconv.Itoa(y1), strconv.Itoa(x), strconv.Itoa(y2), dur)
	return err
}

func adbStopChan() chan struct{} {
	adbStopMutex.Lock()
	defer adbStopMutex.Unlock()
	if adbCancelChan != nil {
		select {
		case <-adbCancelChan:
		default:
			close(adbCancelChan)
		}
	}
	adbCancelChan = make(chan struct{})
	return adbCancelChan
}

// StopAdbScroll 停止 adb 自动翻阅
func StopAdbScroll() {	adbStopMutex.Lock()
	if adbCancelChan != nil {
		select {
		case <-adbCancelChan:
		default:
			close(adbCancelChan)
		}
	}
	adbStopMutex.Unlock()
	global.ExVar.NeedAdbScroll = false
}

// StartAdbScroll adb 模式自动翻阅
// targetTime: 截止时间戳(秒)，0=不限
// swipeDurationMs: 滑动时长毫秒；swipeDistance: 滑动距离像素；waitMs: 每次滑动后等待毫秒（三个参数均不设上下限）
// 与鼠标模式逻辑完全一致：翻页由 adb 注入滑动完成，战报判断/截止时间/进度均基于 npcap 抓包数据
func StartAdbScroll(targetTime int64, swipeDurationMs float64, swipeDistance int, waitMs int64) {
	if adbModeRunning {
		log.Println("adb 自动翻阅已在运行中")
		return
	}
	adbModeRunning = true
	defer func() {
		adbModeRunning = false
		global.ExVar.NeedAdbScroll = false
	}()

	if waitMs < 0 {
		waitMs = 0
	}
	interval := time.Duration(waitMs) * time.Millisecond

	emit := func(event string, data interface{}) {
		if global.AppCtx != nil {
			runtime.EventsEmit(global.AppCtx, event, data)
		}
	}

	cancelChan := adbStopChan()
	global.ExVar.NeedAdbScroll = true

	device := findEmulatorDevice()
	if device == "" {
		emit("autoScrollError", "未检测到模拟器，请先启动模拟器（雷电/MuMu/夜神等）并打开率土之滨，再点击开始")
		return
	}
	w, h := adbScreenSize()
	log.Printf("adb 自动翻阅: 设备=%s 分辨率=%dx%d\n", device, w, h)

	emit("autoScrollStarted", map[string]interface{}{
		"targetTime": targetTime,
		"mode":       "adb",
	})

	scrollCount := 0
	noNewReportCount := 0
	lastReportCount := int64(0)
	initialTime := global.ExVar.AutoScrollStopTime
	if initialTime <= 0 {
		initialTime = time.Now().Unix()
	}

	for {
		select {
		case <-cancelChan:
			emit("autoScrollStopped", map[string]interface{}{
				"reason":   "userCancel",
				"scrolls":  scrollCount,
				"stopTime": global.ExVar.AutoScrollStopTime,
			})
			return
		case <-time.After(100 * time.Millisecond):
			if !global.ExVar.NeedAdbScroll {
				return
			}

			// 检查截止时间（由抓包数据推进）
			if targetTime > 0 && global.ExVar.AutoScrollStopTime > 0 {
				log.Printf("已翻阅到截止时间(目标: %d, 停止点: %d)", targetTime, global.ExVar.AutoScrollStopTime)
				emit("autoScrollStopped", map[string]interface{}{
					"reason":     "timeReached",
					"scrolls":    scrollCount,
					"stopTime":   global.ExVar.AutoScrollStopTime,
					"targetTime": targetTime,
				})
				return
			}

			// adb 注入滑动（从下往上，参数可调）
			if err := adbSwipeUp(w, h, swipeDurationMs, swipeDistance); err != nil {
				log.Printf("adb 滑动失败: %v\n", err)
				emit("autoScrollError", "adb 滑动失败："+err.Error())
				return
			}
			scrollCount++

			// 每10次检测一次新战报（抓包入库数量）
			if scrollCount%10 == 0 && model.Conn != nil {
				var count int64
				model.Conn.Model(&model.BattleReport{}).Count(&count)
				if count > lastReportCount {
					noNewReportCount = 0
				} else {
					noNewReportCount++
				}
				lastReportCount = count
				if noNewReportCount >= 5 {
					log.Println("连续多次没有新战报,停止翻阅")
					emit("autoScrollStopped", map[string]interface{}{
						"reason":   "noMoreData",
						"scrolls":  scrollCount,
						"stopTime": global.ExVar.AutoScrollStopTime,
					})
					return
				}
			}

			// 进度
			if scrollCount%10 == 0 && global.AppCtx != nil {
				now := global.ExVar.AutoScrollStopTime
				percent := 0.0
				if targetTime > 0 && initialTime > targetTime && now > 0 {
					total := initialTime - targetTime
					elapsed := initialTime - now
					if elapsed < 0 {
						elapsed = 0
					}
					if elapsed > total {
						elapsed = total
					}
					percent = float64(elapsed) / float64(total) * 100
				}
				emit("autoScrollProgress", map[string]interface{}{
					"scrolls":     scrollCount,
					"reportCount": lastReportCount,
					"latestTime":  now,
					"initialTime": initialTime,
					"targetTime":  targetTime,
					"percent":     percent,
				})
			}

			if scrollCount%500 == 0 {
				log.Printf("adb 翻阅: 已翻 %d 次, 停止点=%d\n", scrollCount, global.ExVar.AutoScrollStopTime)
			}

			time.Sleep(interval)
		}
	}
}
