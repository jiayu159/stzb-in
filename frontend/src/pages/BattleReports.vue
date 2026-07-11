<script setup>
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { NCard, NButton, NInput, NInputNumber, NEmpty, NSpin, NTag, NPagination, NSpace, NAlert, NDatePicker, NProgress, NStatistic, NModal, NCountdown, useMessage } from 'naive-ui'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import { GetBattleReports, StartAutoScroll, StopAutoScroll } from '../../wailsjs/go/main/App'
import { Search, Swords, Download, Play, Square, Timer, RefreshCw, HelpCircle } from 'lucide-vue-next'
import { formatTimestamp } from '@/utils/format'
import * as XLSX from 'xlsx'

const nmessage = useMessage()
const loading = ref(false)
const reports = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(50)

const searchName = ref('')
const minHp = ref(0)

const hasSearched = ref(false)
const showHelp = ref(false)

// 自动翻阅
const isScrolling = ref(false)
const isDetecting = ref(false)
const showGuide = ref(false)
const guideCountdown = ref(5)
const scrollTargetTime = ref(null)
const scrollProgress = ref({})
const scrollLatestTime = ref(0)
const scrollReportCount = ref(0)
const scrollCount = ref(0)
const guidePos = ref({ centerX: 0, centerY: 0, width: 1280, height: 720 })

const doSearch = (newPage) => {
    if (typeof newPage === 'number') page.value = newPage
    else page.value = 1
    loading.value = true
    reports.value = []
    hasSearched.value = true
    GetBattleReports(searchName.value, minHp.value, page.value, pageSize.value).then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            reports.value = resp.data.list || []
            total.value = resp.data.total || 0
        } else {
            nmessage.error(resp.msg)
        }
    }).catch(e => {
        nmessage.error('查询失败: ' + e)
    }).finally(() => {
        loading.value = false
    })
}

const startScroll = () => {
    if (!scrollTargetTime.value) {
        nmessage.warning('请先选择截止时间')
        return
    }
    const targetTs = Math.floor(scrollTargetTime.value / 1000)
    showGuide.value = true
    isDetecting.value = false
    isScrolling.value = false
    guideCountdown.value = 5
    scrollCount.value = 0
    scrollLatestTime.value = 0
    scrollReportCount.value = 0
    scrollProgress.value = {}
    StartAutoScroll(targetTs).then(v => {
        let resp = JSON.parse(v)
        if (resp.code != 200) {
            nmessage.error(resp.msg)
            showGuide.value = false
        }
    }).catch(e => {
        nmessage.error('启动自动翻阅失败: ' + e)
        showGuide.value = false
    })
}

const stopScroll = () => {
    isScrolling.value = false
    isDetecting.value = false
    showGuide.value = false
    StopAutoScroll().then(v => {
        nmessage.success('已停止自动翻阅')
        doSearch(1)
    }).catch(e => {
        nmessage.error('停止失败: ' + e)
    })
}

const scrollProgressPct = ref(0)

const formatScrollTime = (ts) => {
    if (!ts || ts <= 0) return '--'
    return new Date(ts * 1000).toLocaleString()
}

let unlistenProgress = null
let unlistenStopped = null
let unlistenError = null
let unlistenWarning = null
let unlistenDetecting = null
let unlistenStarted = null

onMounted(() => {
    doSearch(1)
    unlistenDetecting = EventsOn('autoScrollGuide', (data) => {
        showGuide.value = true
        guidePos.value = data
        guideCountdown.value = 5
        isDetecting.value = false
        isScrolling.value = false
    })
    unlistenStarted = EventsOn('autoScrollStarted', (data) => {
        showGuide.value = false
        isDetecting.value = false
        isScrolling.value = true
    })
    unlistenProgress = EventsOn('autoScrollProgress', (data) => {
        scrollProgress.value = data
        scrollReportCount.value = data.reportCount || 0
        scrollCount.value = data.scrolls || scrollCount.value
        scrollProgressPct.value = data.percent || 0
        if (data.latestTime && data.latestTime > scrollLatestTime.value) {
            scrollLatestTime.value = data.latestTime
        }
        if (isScrolling.value) {
            doSearch(1)
        }
    })
    unlistenStopped = EventsOn('autoScrollStopped', (data) => {
        isScrolling.value = false
        isDetecting.value = false
        const reasonMap = {
            timeReached: '已翻阅到截止时间',
            noMoreData: '已翻阅到底部，没有更多战报',
            userCancel: '用户手动停止'
        }
        const reason = reasonMap[data.reason] || data.reason
        nmessage.success(`自动翻阅已停止(${reason})，共翻页 ${data.scrolls || 0} 次`)
        doSearch(1)
    })
    unlistenWarning = EventsOn('autoScrollWarning', (msg) => {
        nmessage.warning(msg)
    })
    unlistenError = EventsOn('autoScrollError', (msg) => {
        isScrolling.value = false
        isDetecting.value = false
        nmessage.error(msg)
    })
})

onUnmounted(() => {
    if (unlistenDetecting) EventsOff('autoScrollDetecting')
    if (unlistenStarted) EventsOff('autoScrollStarted')
    if (unlistenProgress) EventsOff('autoScrollProgress')
    if (unlistenStopped) EventsOff('autoScrollStopped')
    if (unlistenWarning) EventsOff('autoScrollWarning')
    if (unlistenError) EventsOff('autoScrollError')
    if (isScrolling.value || isDetecting.value) StopAutoScroll()
})

const resultMap = {
    0: { label: '守方胜', type: 'warning' },
    1: { label: '攻方胜', type: 'success' },
    2: { label: '攻方胜', type: 'success' },
    3: { label: '攻方胜', type: 'success' },
    4: { label: '攻方胜', type: 'success' },
    6: { label: '平局', type: 'info' },
    7: { label: '平局', type: 'info' },
    8: { label: '平局', type: 'info' },
    10: { label: '攻方胜', type: 'success' },
    13: { label: '平局', type: 'info' },
    18: { label: '攻方胜', type: 'success' },
    19: { label: '攻方胜', type: 'success' },
}

const getResult = (result, role) => {
    const m = resultMap[result]
    if (!m) return { label: '未知', type: 'default' }
    if (role === 'attack') {
        if ([1, 2, 3, 4, 10, 18, 19].includes(result)) return { label: '胜', type: 'success' }
        if (result === 0) return { label: '负', type: 'error' }
        return { label: '平', type: 'info' }
    }
    if (role === 'defend') {
        if (result === 0) return { label: '胜', type: 'success' }
        if ([1, 2, 3, 4, 10, 18, 19].includes(result)) return { label: '负', type: 'error' }
        return { label: '平', type: 'info' }
    }
    return m
}

const formatHp = (hp) => {
    if (!hp) return '0'
    if (hp >= 10000) return (hp / 10000).toFixed(1) + '万'
    return String(hp)
}

const exportExcel = () => {
    if (reports.value.length === 0) {
        nmessage.warning('没有数据可导出')
        return
    }
    let data = []
    data.push(['时间', '进攻方', '进攻方同盟', '进攻方兵力', '防守方', '防守方同盟', '防守方兵力', '地点', '结果'])
    reports.value.forEach(r => {
        const atkResult = getResult(r.result, 'attack')
        data.push([
            formatTimestamp(r.time),
            r.attack_name,
            r.attack_union_name,
            r.attack_hp,
            r.defend_name,
            r.defend_union_name,
            r.defend_hp,
            r.wid_name || '未知',
            atkResult.label,
        ])
    })
    const ws = XLSX.utils.aoa_to_sheet(data)
    const wb = XLSX.utils.book_new()
    XLSX.utils.book_append_sheet(wb, ws, '同盟战报')
    XLSX.writeFile(wb, `同盟战报_${new Date().toLocaleDateString().replace(/\//g, '-')}.xlsx`)
    nmessage.success('导出成功')
}

onMounted(() => {
    doSearch(1)
})
</script>

<template>
    <div class="page-battle-reports">
        <n-card class="page-card" embedded>
            <div class="page-header">
                <div class="page-header-info">
                    <h2 class="page-title">同盟战报</h2>
                    <p class="page-desc">查询所有已抓取的战斗记录</p>
                </div>
                <n-space align="center">
                    <n-button type="primary" @click="doSearch()">
                        <template #icon><RefreshCw :size="16" /></template>
                        刷新
                    </n-button>
                    <n-button type="primary" @click="exportExcel" :disabled="reports.length === 0">
                        <template #icon><Download :size="16" /></template>
                        导出表格
                    </n-button>
                </n-space>
            </div>

            <!-- 自动翻阅控制栏 -->
            <n-alert v-if="!isScrolling && !showGuide" type="info" :bordered="false" style="margin-bottom:16px;">
                <template #header>
                    <div style="display:flex;align-items:center;gap:8px;justify-content:space-between;width:100%;">
                        <span style="display:flex;align-items:center;gap:8px;">
                            <Timer :size="16" />
                            自动翻阅（鼠标模式）
                        </span>
                        <n-button text size="small" @click="showHelp=true" style="opacity:0.7;">
                            <template #icon><HelpCircle :size="16" /></template>
                            使用说明
                        </n-button>
                    </div>
                </template>
                <div style="font-size:13px;margin-bottom:10px;">
                    程序将在屏幕中央显示一个<b>白色引导框</b>，请将<b>模拟器窗口移到框内</b>。5秒后鼠标会自动移到框内滚轮翻页。
                </div>
                <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap;">
                    <n-date-picker v-model:value="scrollTargetTime" type="datetime"
                        placeholder="请选择截止时间" clearable style="min-width:260px;" />
                    <n-button type="success" @click="startScroll" icon-placement="right">
                        <template #icon><Play :size="16" /></template>
                        开始翻阅
                    </n-button>
                </div>
            </n-alert>

            <!-- 引导框 + 倒计时 -->
            <div v-if="showGuide" class="guide-overlay">
                <div class="guide-box" :style="{
                    left: guidePos.centerX - guidePos.width/2 + 'px',
                    top: guidePos.centerY - guidePos.height/2 + 'px',
                    width: guidePos.width + 'px',
                    height: guidePos.height + 'px',
                }">
                    <div class="guide-label">
                        <Timer :size="20" />
                        <span>将模拟器窗口移到此框内</span>
                        <div class="guide-countdown">{{ guideCountdown }}s</div>
                    </div>
                    <div class="guide-sub">屏幕中央 1280x720 区域</div>
                </div>
            </div>

            <!-- 自动翻阅中 -->
            <n-alert v-if="isScrolling" type="warning" :bordered="false" style="margin-bottom:16px;">
                <template #header>
                    <div style="display:flex;align-items:center;gap:8px;justify-content:space-between;width:100%;">
                        <span style="display:flex;align-items:center;gap:6px;">
                            <Play :size="16" style="color:#18a058;" />
                            自动翻阅进行中
                        </span>
                        <n-button size="small" type="error" @click="stopScroll">
                            <template #icon><Square :size="14" /></template>
                            停止翻阅
                        </n-button>
                    </div>
                </template>
                <div style="margin-top:8px;">
                    <div v-if="scrollTargetTime" style="margin-bottom:8px;">
                        <div style="display:flex;justify-content:space-between;font-size:12px;margin-bottom:4px;">
                            <span>翻阅进度</span>
                            <span>{{ scrollProgressPct }}%（截止: {{ formatScrollTime(Math.floor(scrollTargetTime / 1000)) }}）</span>
                        </div>
                        <n-progress :value="scrollProgressPct" :height="8" :border-radius="4" color="#18a058" />
                    </div>
                    <div style="display:grid;grid-template-columns:repeat(3,1fr);gap:12px;margin-top:12px;">
                        <n-statistic label="已捕获战报" :value="scrollReportCount" />
                        <n-statistic label="翻阅次数" :value="scrollCount" />
                        <n-statistic label="最新战报时间" :value="formatScrollTime(scrollLatestTime)" />
                    </div>
                </div>
            </n-alert>

            <div class="search-bar">
                <div class="search-item">
                    <span class="search-label">玩家/地点</span>
                    <n-input v-model:value="searchName" placeholder="输入玩家名或地点" clearable :style="{ width: '200px' }" @keyup.enter="doSearch()" />
                </div>
                <div class="search-item">
                    <span class="search-label">最小兵力</span>
                    <n-input-number v-model:value="minHp" :min="0" :step="1000" placeholder="兵力过滤" clearable :style="{ width: '150px' }" />
                </div>
                <n-button type="primary" @click="doSearch()" :loading="loading">
                    <template #icon><Search :size="16" /></template>
                    查询
                </n-button>
            </div>

            <n-spin :show="loading">
                <n-empty v-if="!loading && hasSearched && reports.length === 0" description="暂无战斗记录" />
                <div v-else-if="reports.length > 0" class="report-list">
                    <div v-for="r in reports" :key="r.id" class="report-card">
                        <div class="report-header">
                            <span class="report-time">{{ formatTimestamp(r.time) }}</span>
                            <span class="report-location">
                                <Swords :size="14" />
                                {{ r.wid_name || '未知地点' }}
                            </span>
                        </div>
                        <div class="report-body">
                            <div class="side attack-side">
                                <div class="side-label">攻</div>
                                <div class="side-name">{{ r.attack_name }}</div>
                                <div class="side-union">{{ r.attack_union_name }}</div>
                                <div class="side-hp">兵力: {{ formatHp(r.attack_hp) }}</div>
                                <n-tag :type="getResult(r.result, 'attack').type" size="small">
                                    {{ getResult(r.result, 'attack').label }}
                                </n-tag>
                            </div>
                            <div class="vs">VS</div>
                            <div class="side defend-side">
                                <div class="side-label">守</div>
                                <div class="side-name">{{ r.defend_name }}</div>
                                <div class="side-union">{{ r.defend_union_name }}</div>
                                <div class="side-hp">兵力: {{ formatHp(r.defend_hp) }}</div>
                                <n-tag :type="getResult(r.result, 'defend').type" size="small">
                                    {{ getResult(r.result, 'defend').label }}
                                </n-tag>
                            </div>
                        </div>
                    </div>
                </div>
            </n-spin>

            <div v-if="total > pageSize" class="pagination-wrap">
                <n-pagination
                    :page="page"
                    :page-size="pageSize"
                    :item-count="total"
                    @update:page="doSearch"
                />
            </div>
        </n-card>
    </div>

    <!-- 使用说明弹窗 -->
    <n-modal v-model:show="showHelp" preset="card" title="自动翻阅使用说明" size="huge"
        style="max-width:640px" :bordered="false" to="body">
        <div style="font-size:14px;line-height:1.8;">
            <h4 style="margin:0 0 8px;">功能介绍</h4>
            <p>程序会在屏幕中央显示一个 1280x720 的白色引导框，将模拟器窗口移到框内。5秒后鼠标自动滚轮翻页，抓取战报数据入库。</p>

            <h4 style="margin:16px 0 8px;">操作步骤</h4>
            <ol style="padding-left:20px;margin:0;">
                <li>启动模拟器，打开率土之滨，进入游戏</li>
                <li>在游戏内打开 <strong>同盟战报</strong> 页面（确保战报列表已加载出来）</li>
                <li>在页面左上区域选择一个截止时间（可选），如要翻到 <strong>7月11日12:00 之前</strong> 的战报，就选 <code>2026-07-11 12:00</code></li>
                <li>点击 <strong>"开始翻阅"</strong> 按钮</li>
                <li>屏幕中央出现白色引导框，将 <strong>模拟器窗口拖到框内</strong></li>
                <li>5秒倒计时结束后鼠标自动滚轮翻页</li>
                <li>翻阅到截止时间后自动停止，或点 <strong>"停止翻阅"</strong> 手动结束</li>
            </ol>

            <h4 style="margin:16px 0 8px;">注意事项</h4>
            <ul style="padding-left:20px;margin:0;">
                <li>建议将模拟器窗口设为 1280x720 分辨率</li>
                <li>翻页过程中请勿动鼠标或键盘</li>
                <li>翻阅速度约 0.8 秒/次，1000 页约 13 分钟</li>
                <li>连续多次无新战报自动停止</li>
                <li>新战报自动入库并刷新列表</li>
            </ul>
        </div>
        <template #footer>
            <n-button type="primary" @click="showHelp=false">知道了</n-button>
        </template>
    </n-modal>
</template>

<style scoped>
.page-battle-reports {
    max-width: 1100px;
    margin: 0 auto;
}

.page-card {
    margin-bottom: 16px;
}

.page-header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    margin-bottom: 20px;
}

.page-title {
    font-size: 20px;
    font-weight: 600;
    margin: 0;
}

.page-desc {
    margin: 4px 0 0 0;
    opacity: 0.6;
    font-size: 13px;
}

.search-bar {
    display: flex;
    align-items: center;
    gap: 16px;
    margin-bottom: 20px;
    flex-wrap: wrap;
}

.search-item {
    display: flex;
    align-items: center;
    gap: 8px;
}

.search-label {
    font-size: 13px;
    white-space: nowrap;
    opacity: 0.8;
}

.report-list {
    display: flex;
    flex-direction: column;
    gap: 12px;
}

.report-card {
    border: 1px solid var(--border-color, #e5e5e5);
    border-radius: 10px;
    padding: 16px;
    transition: box-shadow 0.2s;
}

.report-card:hover {
    box-shadow: 0 2px 12px rgba(0, 0, 0, 0.06);
}

.report-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 12px;
    font-size: 12px;
    opacity: 0.6;
}

.report-location {
    display: flex;
    align-items: center;
    gap: 4px;
}

.report-body {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 24px;
}

.side {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    text-align: center;
}

.side-label {
    font-size: 11px;
    font-weight: 700;
    padding: 2px 10px;
    border-radius: 4px;
    margin-bottom: 4px;
}

.attack-side .side-label {
    background: rgba(59, 130, 246, 0.12);
    color: #3b82f6;
}

.defend-side .side-label {
    background: rgba(239, 68, 68, 0.12);
    color: #ef4444;
}

.side-name {
    font-size: 15px;
    font-weight: 600;
}

.side-union {
    font-size: 12px;
    opacity: 0.6;
}

.side-hp {
    font-size: 13px;
    opacity: 0.7;
}

.vs {
    font-size: 13px;
    font-weight: 700;
    opacity: 0.3;
    flex-shrink: 0;
}

.pagination-wrap {
    display: flex;
    justify-content: center;
    margin-top: 20px;
}

@keyframes pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.4; }
}

.guide-overlay {
    position: fixed;
    top: 0;
    left: 0;
    width: 100vw;
    height: 100vh;
    background: rgba(0,0,0,0.3);
    z-index: 9999;
    pointer-events: none;
}

.guide-box {
    position: absolute;
    border: 3px dashed rgba(255,255,255,0.8);
    border-radius: 8px;
    background: rgba(255,255,255,0.08);
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
}

.guide-label {
    display: flex;
    align-items: center;
    gap: 10px;
    color: #fff;
    font-size: 18px;
    font-weight: 600;
    text-shadow: 0 2px 8px rgba(0,0,0,0.6);
}

.guide-countdown {
    font-size: 24px;
    font-weight: 700;
    color: #f0ad4e;
}

.guide-sub {
    color: rgba(255,255,255,0.7);
    font-size: 13px;
    margin-top: 8px;
    text-shadow: 0 1px 4px rgba(0,0,0,0.6);
}
</style>
