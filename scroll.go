package main

import (
	"log"
	"stzbHelper/global"
	"stzbHelper/model"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	user32                    = syscall.NewLazyDLL("user32.dll")
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procSendInput             = user32.NewProc("SendInput")
	procSetCursorPos          = user32.NewProc("SetCursorPos")
	procGetSystemMetrics      = user32.NewProc("GetSystemMetrics")
	procShowWindow            = user32.NewProc("ShowWindow")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procSwitchToThisWindow    = user32.NewProc("SwitchToThisWindow")
	procSetWindowPos          = user32.NewProc("SetWindowPos")
	procIsIconic              = user32.NewProc("IsIconic")
	procFindWindowW           = user32.NewProc("FindWindowW")
	procEnumWindows           = user32.NewProc("EnumWindows")
	procGetWindowTextW        = user32.NewProc("GetWindowTextW")
	procGetClassNameW         = user32.NewProc("GetClassNameW")
	procIsWindowVisible       = user32.NewProc("IsWindowVisible")
	procCreateToolhelp32Snapshot = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32First        = kernel32.NewProc("Process32FirstW")
	procProcess32Next         = kernel32.NewProc("Process32NextW")
	procCloseHandle           = kernel32.NewProc("CloseHandle")
	procGetWindowRect         = user32.NewProc("GetWindowRect")
	autoScrollCancelChan      chan struct{}
	autoScrollStopChanMutex   sync.Mutex
)

const (
	SM_CXSCREEN      = 0
	SM_CYSCREEN      = 1
	TH32CS_SNAPPROCESS = 0x00000002
	INPUT_KEYBOARD   = 1
	INPUT_MOUSE      = 0
	KEYEVENTF_KEYUP  = 0x0002
	MOUSEEVENTF_WHEEL = 0x0800
	WHEEL_DELTA      = 120
	VK_NEXT          = 0x22
	SW_RESTORE       = 9
	SW_SHOW          = 5
	SWP_SHOWWINDOW   = 0x0040
)

type keyboardInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uint64
}

type mouseInput struct {
	dx          int32
	dy          int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type genericInput struct {
	typ     uint32
	ki      keyboardInput
	padding [4]byte
}

type PROCESSENTRY32 struct {
	dwSize              uint32
	cntUsage            uint32
	th32ProcessID       uint32
	th32DefaultHeapID   uintptr
	th32ModuleID        uint32
	cntThreads          uint32
	th32ParentProcessID uint32
	pcPriClassBase      int32
	dwFlags             uint32
	szExeFile           [260]uint16
}

type RECT struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

var targetProcessNames = []string{
	"stzb.exe", "率土之滨.exe",
	"NemuPlayer.exe", "MuMuPlayer.exe", "MuMuVMMPlayer.exe",
	"dnplayer.exe", "ldplayer.exe", "ldbox.exe",
	"HD-Player.exe", "BstkSVC.exe",
	"AndroidEmulator.exe", "TGame.exe",
	"Nox.exe", "NoxPlayer.exe",
	"MEmu.exe", "MEmuHeadless.exe",
}

var targetWindowClasses = []string{
	"NemuPlayer", "Qt5QWindowIcon", "Qt5152QWindowIcon",
	"LDPlayerMainFrame", "dnplayer",
	"BlueStacksApp", "HwndWrapper",
	"MainWindow", "NoxPlayer",
}

var targetWindowNames = []string{
	"率土之滨", "率土", "stzb", "Stzb",
	"MuMu模拟器", "MuMu", "雷电模拟器", "雷电",
	"BlueStacks", "夜神模拟器", "逍遥模拟器",
}

// 鼠标滚轮滚动（正数=向上，负数=向下）
func sendMouseWheel(clicks int32) {
	type mi struct {
		dx          int32
		dy          int32
		mouseData   uint32
		dwFlags     uint32
		time        uint32
		dwExtraInfo uintptr
	}
	var inputs [1]struct {
		typ uint32
		mi  mi
	}
	inputs[0].typ = INPUT_MOUSE
	inputs[0].mi.dwFlags = MOUSEEVENTF_WHEEL
	inputs[0].mi.mouseData = uint32(clicks * WHEEL_DELTA)
	procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs)),
		uintptr(unsafe.Sizeof(inputs[0])),
	)
}

// 获取屏幕分辨率
func getScreenSize() (int32, int32) {
	w, _, _ := procGetSystemMetrics.Call(SM_CXSCREEN)
	h, _, _ := procGetSystemMetrics.Call(SM_CYSCREEN)
	return int32(w), int32(h)
}

// 屏幕中央白色引导框的参数
const (
	GuideWidth  = 1280
	GuideHeight = 720
)

// 获取引导框左上角和中心坐标
func getGuideRect() (left, top, right, bottom, centerX, centerY int32) {
	sw, sh := getScreenSize()
	left = (sw - GuideWidth) / 2
	top = (sh - GuideHeight) / 2
	if left < 0 {
		left = 0
	}
	if top < 0 {
		top = 0
	}
	right = left + GuideWidth
	bottom = top + GuideHeight
	centerX = left + GuideWidth/2
	centerY = top + GuideHeight/2
	return
}

// sendKeyPress 发送按键
func sendKeyPress(vk uint16) {
	var inputs [2]genericInput
	inputs[0].typ = INPUT_KEYBOARD
	inputs[0].ki.wVk = vk
	inputs[1].typ = INPUT_KEYBOARD
	inputs[1].ki.wVk = vk
	inputs[1].ki.dwFlags = KEYEVENTF_KEYUP
	procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs)),
		uintptr(unsafe.Sizeof(inputs[0])),
	)
}

// findGameProcess 检查是否有模拟器/游戏进程
func findGameProcess() bool {
	snapshot, _, _ := procCreateToolhelp32Snapshot.Call(TH32CS_SNAPPROCESS, 0)
	if snapshot == uintptr(syscall.InvalidHandle) {
		return false
	}
	defer procCloseHandle.Call(snapshot)
	var pe PROCESSENTRY32
	pe.dwSize = uint32(unsafe.Sizeof(pe))
	ret, _, _ := procProcess32First.Call(snapshot, uintptr(unsafe.Pointer(&pe)))
	for ret != 0 {
		name := syscall.UTF16ToString(pe.szExeFile[:])
		for _, target := range targetProcessNames {
			if name == target {
				return true
			}
		}
		ret, _, _ = procProcess32Next.Call(snapshot, uintptr(unsafe.Pointer(&pe)))
	}
	return false
}

// contains 子串判断（不区分大小写）
func contains(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	lower := func(b byte) byte {
		if b >= 'A' && b <= 'Z' {
			return b + 32
		}
		return b
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if lower(s[i+j]) != lower(substr[j]) {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// findGameWindow 通过窗口类名/标题查找模拟器/游戏窗口句柄
func findGameWindow() uintptr {
	var hwnd uintptr
	cb := syscall.NewCallback(func(h syscall.Handle, lparam uintptr) uintptr {
		var cls [256]uint16
		var title [256]uint16
		procGetClassNameW.Call(uintptr(h), uintptr(unsafe.Pointer(&cls)), 256)
		procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&title)), 256)
		c := syscall.UTF16ToString(cls[:])
		t := syscall.UTF16ToString(title[:])
		for _, wc := range targetWindowClasses {
			if contains(c, wc) {
				for _, wn := range targetWindowNames {
					if contains(t, wn) {
						*(*uintptr)(unsafe.Pointer(lparam)) = uintptr(h)
						return 0
					}
				}
			}
		}
		// 也直接匹配窗口标题
		for _, wn := range targetWindowNames {
			if contains(t, wn) {
				*(*uintptr)(unsafe.Pointer(lparam)) = uintptr(h)
				return 0
			}
		}
		return 1
	})
	procEnumWindows.Call(cb, uintptr(unsafe.Pointer(&hwnd)))
	return hwnd
}

// StopAutoScrollByBackend 停止所有翻阅操作
func StopAutoScrollByBackend() {
	global.ExVar.NeedAutoScroll = false
	global.ExVar.NeedAutoScrollDetect = false
	autoScrollStopChanMutex.Lock()
	if autoScrollCancelChan != nil {
		select {
		case <-autoScrollCancelChan:
		default:
			close(autoScrollCancelChan)
		}
	}
	autoScrollStopChanMutex.Unlock()
	log.Println("自动翻阅已停止")
}

// StartMouseScroll 鼠标滚轮自动翻阅
// targetTime: 截止时间戳(秒), 0=不限
// 流程：检测进程 → 提示 → 用户将窗口放到屏幕中央引导框 → 鼠标移到框内开始滚轮翻页
func StartMouseScroll(targetTime int64) {
	if global.ExVar.NeedAutoScroll {
		log.Println("自动翻阅已在运行中")
		return
	}

	// 检测进程
	if !findGameProcess() {
		if global.AppCtx != nil {
			runtime.EventsEmit(global.AppCtx, "autoScrollError", "未检测到模拟器或游戏进程，请先启动模拟器并打开率土之滨")
		}
		return
	}

	// 计算引导框位置（屏幕中央1280x720）
	_, _, _, _, centerX, centerY := getGuideRect()

	global.ExVar.NeedAutoScrollDetect = true
	global.ExVar.AutoScrollDetected = false
	global.ExVar.NeedAutoScroll = true
	global.ExVar.AutoScrollTargetTime = targetTime
	global.ExVar.AutoScrollStopTime = 0

	cancelChan := make(chan struct{})
	autoScrollStopChanMutex.Lock()
	if autoScrollCancelChan != nil {
		close(autoScrollCancelChan)
	}
	autoScrollCancelChan = cancelChan
	autoScrollStopChanMutex.Unlock()

	go func() {
		defer func() {
			global.ExVar.NeedAutoScroll = false
			global.ExVar.NeedAutoScrollDetect = false
			log.Println("自动翻阅 goroutine 退出")
		}()

		// 通知前端: 显示引导UI + 5秒倒计时
		if global.AppCtx != nil {
			runtime.EventsEmit(global.AppCtx, "autoScrollGuide", map[string]interface{}{
				"centerX": centerX,
				"centerY": centerY,
				"width":   GuideWidth,
				"height":  GuideHeight,
			})
		}

		// 等待5秒让用户把窗口移到引导框位置
		log.Println("等待用户放置窗口(5秒)...")
		countdownTimer := time.NewTimer(5 * time.Second)

		select {
		case <-cancelChan:
			countdownTimer.Stop()
			return
		case <-countdownTimer.C:
			// 倒计时结束
		}

		// 查找模拟器窗口并获取其实际位置
		foundWindow := false
		if hwnd := findGameWindow(); hwnd != 0 {
			var rect RECT
			if ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); ret != 0 {
				wcx := (rect.Left + rect.Right) / 2
				wcy := (rect.Top + rect.Bottom) / 2
				if wcx > 0 && wcy > 0 {
					centerX = wcx
					centerY = wcy
					foundWindow = true
				}
			}
			// 尝试激活窗口
			procSwitchToThisWindow.Call(hwnd, 1)
			procShowWindow.Call(hwnd, SW_RESTORE)
		}

		// 将鼠标移到窗口中央（或引导框中央作为fallback）
		procSetCursorPos.Call(uintptr(centerX), uintptr(centerY))
		time.Sleep(200 * time.Millisecond)

		if foundWindow {
			log.Printf("已找到模拟器窗口，鼠标移到窗口中央 (%d,%d)，开始滚轮翻页\n", centerX, centerY)
		} else {
			log.Printf("未找到模拟器窗口，鼠标移到引导框中央 (%d,%d)，开始滚轮翻页\n", centerX, centerY)
		}

		if global.AppCtx != nil {
			runtime.EventsEmit(global.AppCtx, "autoScrollStarted", map[string]interface{}{
				"targetTime": targetTime,
			})
		}
		global.ExVar.NeedAutoScrollDetect = false

		scrollCount := 0
		noNewReportCount := 0
		lastReportCount := int64(0)
		dataCheckTick := 0
		initialTime := global.ExVar.AutoScrollStopTime
		if initialTime <= 0 {
			initialTime = time.Now().Unix()
		}

		for {
			select {
			case <-cancelChan:
				if global.AppCtx != nil {
					runtime.EventsEmit(global.AppCtx, "autoScrollStopped", map[string]interface{}{
						"reason":   "userCancel",
						"scrolls":  scrollCount,
						"stopTime": global.ExVar.AutoScrollStopTime,
					})
				}
				return
			case <-time.After(100 * time.Millisecond):
				if !global.ExVar.NeedAutoScroll {
					return
				}

				// 检查截止时间
				if targetTime > 0 && global.ExVar.AutoScrollStopTime > 0 {
					log.Printf("已翻阅到截止时间(目标: %d, 停止点: %d)", targetTime, global.ExVar.AutoScrollStopTime)
					if global.AppCtx != nil {
						runtime.EventsEmit(global.AppCtx, "autoScrollStopped", map[string]interface{}{
							"reason":     "timeReached",
							"scrolls":    scrollCount,
							"stopTime":   global.ExVar.AutoScrollStopTime,
							"targetTime": targetTime,
						})
					}
					global.ExVar.NeedAutoScroll = false
					return
				}

				// 每100次检测一次新战报
				dataCheckTick++
				if dataCheckTick >= 100 {
					dataCheckTick = 0
					var count int64
					if model.Conn != nil {
						model.Conn.Model(&model.BattleReport{}).Count(&count)
						if count > lastReportCount {
							noNewReportCount = 0
						} else {
							noNewReportCount++
						}
						lastReportCount = count
						if noNewReportCount >= 5 {
							log.Println("连续多次没有新战报,停止翻阅")
							if global.AppCtx != nil {
								runtime.EventsEmit(global.AppCtx, "autoScrollStopped", map[string]interface{}{
									"reason":   "noMoreData",
									"scrolls":  scrollCount,
									"stopTime": global.ExVar.AutoScrollStopTime,
								})
							}
							global.ExVar.NeedAutoScroll = false
							return
						}
					}
				}

				// 每次发5个-1格滚轮事件（绕过单次delta上限限制）
				sendMouseWheel(-1)
				sendMouseWheel(-1)
				sendMouseWheel(-1)
				sendMouseWheel(-1)
				sendMouseWheel(-1)
				scrollCount++

				if scrollCount%500 == 0 {
					log.Printf("滚轮翻阅: 已发 %d 次\n", scrollCount)
				}

				if dataCheckTick == 0 && global.AppCtx != nil {
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
					runtime.EventsEmit(global.AppCtx, "autoScrollProgress", map[string]interface{}{
						"scrolls":     scrollCount,
						"reportCount": lastReportCount,
						"latestTime":  now,
						"initialTime": initialTime,
						"targetTime":  targetTime,
						"percent":     percent,
					})
				}
			}
		}
	}()
}

// sendMouseDown 按下左键（不弹起）
func sendMouseDown() {
	type mi struct {
		dx          int32
		dy          int32
		mouseData   uint32
		dwFlags     uint32
		time        uint32
		dwExtraInfo uintptr
	}
	var input struct {
		typ uint32
		mi  mi
	}
	input.typ = INPUT_MOUSE
	input.mi.dwFlags = 0x0002 // MOUSEEVENTF_LEFTDOWN
	procSendInput.Call(1, uintptr(unsafe.Pointer(&input)), unsafe.Sizeof(input))
}

// sendMouseUp 弹起左键
func sendMouseUp() {
	type mi struct {
		dx          int32
		dy          int32
		mouseData   uint32
		dwFlags     uint32
		time        uint32
		dwExtraInfo uintptr
	}
	var input struct {
		typ uint32
		mi  mi
	}
	input.typ = INPUT_MOUSE
	input.mi.dwFlags = 0x0004 // MOUSEEVENTF_LEFTUP
	procSendInput.Call(1, uintptr(unsafe.Pointer(&input)), unsafe.Sizeof(input))
}
