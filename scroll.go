package main

import (
	"log"
	"stzbHelper/global"
	"sync"
)

var (
	autoScrollCancelChan    chan struct{}
	autoScrollStopChanMutex sync.Mutex
)

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