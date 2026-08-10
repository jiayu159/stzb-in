import { defineStore } from 'pinia'
import { EventsOn } from '../../wailsjs/runtime/runtime'

export const useAutoScrollStore = defineStore('autoScroll', {
    state: () => ({
        isScrolling: false,
        isDetecting: false,
        showGuide: false,
        guideCountdown: 5,
        guidePos: { centerX: 0, centerY: 0, width: 1280, height: 720 },
        scrollProgress: {},
        scrollLatestTime: 0,
        scrollReportCount: 0,
        scrollCount: 0,
        scrollProgressPct: 0,
        directRunning: false,
        directProgress: {},
        directResult: '',
        lastStop: null,
        listenersReady: false
    }),
    actions: {
        ensureListeners() {
            if (this.listenersReady) return
            this.listenersReady = true
            EventsOn('autoScrollGuide', (data) => {
                this.showGuide = true
                this.guidePos = data
                this.guideCountdown = 5
                this.isDetecting = false
                this.isScrolling = false
            })
            EventsOn('autoScrollStarted', () => {
                this.showGuide = false
                this.isDetecting = false
                this.isScrolling = true
            })
            EventsOn('autoScrollProgress', (data) => {
                this.scrollProgress = data
                this.scrollReportCount = data.reportCount || 0
                this.scrollCount = data.scrolls || this.scrollCount
                this.scrollProgressPct = data.percent || 0
                if (data.latestTime && data.latestTime > this.scrollLatestTime) {
                    this.scrollLatestTime = data.latestTime
                }
            })
            EventsOn('autoScrollStopped', (data) => {
                this.isScrolling = false
                this.isDetecting = false
                this.showGuide = false
                this.lastStop = { reason: data.reason || '', scrolls: data.scrolls || 0 }
            })
            EventsOn('autoScrollError', () => {
                this.isScrolling = false
                this.isDetecting = false
                this.showGuide = false
            })
            EventsOn('directFetchProgress', (data) => {
                this.directRunning = true
                this.directProgress = data
            })
            EventsOn('directFetchDone', (data) => {
                this.directRunning = false
                this.lastStop = { mode: 'direct', reason: data.reason || '', reportCount: data.reportCount || 0 }
            })
            EventsOn('directFetchError', () => {
                this.directRunning = false
            })
            EventsOn('directFetchStopped', () => {
                this.directRunning = false
            })
        }
    }
})