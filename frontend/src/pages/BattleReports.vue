<script setup>
import { ref, onMounted, onUnmounted, watch, computed } from 'vue'
import { NCard, NButton, NInput, NInputNumber, NEmpty, NSpin, NTag, NPagination, NSpace, NAlert, NDatePicker, NProgress, NStatistic, NModal, NCollapse, NCollapseItem, NSelect, useMessage } from 'naive-ui'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import { GetBattleReports, ExportAllBattleReports, StartAutoScroll, StopAutoScroll, EnableCaptureRequests, DisableCaptureRequests, TestDirectFetch, DirectFetchLoop, DirectFetchStop, EnableGetBattleReport, DisableGetBattleReport, CheckAdb, GetMyUnionName } from '../../wailsjs/go/main/App'
import { Search, Swords, Download, Play, Square, Timer, RefreshCw, HelpCircle, ChevronUp, ChevronDown } from 'lucide-vue-next'
import { formatTimestamp } from '@/utils/format'
import { storeToRefs } from 'pinia'
import { useAutoScrollStore } from '../stores/autoScroll'
import * as XLSX from 'xlsx'

const nmessage = useMessage()
const scrollStore = useAutoScrollStore()
scrollStore.ensureListeners()
const {
    isScrolling,
    scrollProgress, scrollLatestTime, scrollReportCount, scrollCount, scrollProgressPct,
    directRunning, directProgress, directResult
} = storeToRefs(scrollStore)
const loading = ref(false)
const reports = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(50)

const searchName = ref('')
const minHp = ref(0)

// 战报分类筛选（可叠加）
const fightType = ref(0)
const fightTypeOptions = [
    { label: '全部', value: 0 },
    { label: '进攻(我盟进攻)', value: 1 },
    { label: '防守(我盟防守)', value: 2 },
]
const myUnion = ref('')

const hasSearched = ref(false)
const showHelp = ref(false)

// 自动翻阅（全局状态，切换页面不中断）
const scrollTargetTime = ref(null)

// 请求分析（方案B：逆向客户端->服务器请求）
const capturing = ref(false)
const capturedRequests = ref([])

const toggleCapture = () => {
    if (capturing.value) {
        DisableCaptureRequests().then(() => {
            capturing.value = false
            nmessage.info('已关闭请求分析')
        }).catch(e => nmessage.error('关闭失败: ' + e))
    } else {
        EnableCaptureRequests().then(v => {
            let resp = JSON.parse(v)
            if (resp.code != 200) {
                nmessage.error(resp.msg)
                return
            }
            capturing.value = true
            capturedRequests.value = []
            nmessage.success(resp.msg)
        }).catch(e => nmessage.error('启动失败: ' + e))
    }
}

const clearCapture = () => {
    capturedRequests.value = []
}

// 获取详细战报开关（由控制面板迁移至此）
const onEnableGetBattleReport = () => {
    EnableGetBattleReport().then(v => {
        let data = JSON.parse(v)
        if (data.code == 200) {
            nmessage.success('开启成功')
        } else {
            nmessage.error(data.msg)
        }
    }).catch(e => {
        nmessage.error('开启获取战报详情失败:' + e)
    })
}

const onDisableGetBattleReport = () => {
    DisableGetBattleReport().then(v => {
        let data = JSON.parse(v)
        if (data.code == 200) {
            nmessage.success('关闭成功')
        } else {
            nmessage.error(data.msg)
        }
    }).catch(e => {
        nmessage.error('关闭获取战报详情失败:' + e)
    })
}

// 直连重放（免翻页拉取，状态全局化）
const testDirect = () => {
    directResult.value = '测试中...'
    TestDirectFetch().then(v => {
        let resp = JSON.parse(v)
        if (resp.code != 200) {
            nmessage.error(resp.msg)
            directResult.value = resp.msg
            return
        }
        directResult.value = JSON.stringify(resp.data || resp.msg, null, 1)
        nmessage.success(resp.msg)
    }).catch(e => nmessage.error('测试失败: ' + e))
}

const startDirectFetch = () => {
    if (!directRunning.value) {
        DirectFetchLoop(0).then(v => {
            let resp = JSON.parse(v)
            if (resp.code != 200) {
                nmessage.error(resp.msg)
                return
            }
            directRunning.value = true
            directProgress.value = {}
            nmessage.success(resp.msg)
        }).catch(e => nmessage.error('启动失败: ' + e))
    } else {
        DirectFetchStop().then(v => {
            let resp = JSON.parse(v)
            directRunning.value = false
            nmessage.info(resp.msg)
        }).catch(e => nmessage.error('停止失败: ' + e))
    }
}

const doSearch = (newPage) => {
    if (typeof newPage === 'number') page.value = newPage
    else page.value = 1
    loading.value = true
    reports.value = []
    hasSearched.value = true
    localStorage.setItem('stzb_my_union', myUnion.value.trim())
    GetBattleReports(searchName.value, minHp.value, fightType.value, myUnion.value.trim(), page.value, pageSize.value).then(v => {
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
    const intervalMs = scrollInterval.value || 600
    scrollCount.value = 0
    scrollLatestTime.value = 0
    scrollReportCount.value = 0
    scrollProgress.value = {}
    StartAutoScroll(targetTs, intervalMs).then(v => {
        let resp = JSON.parse(v)
        if (resp.code != 200) {
            nmessage.error(resp.msg)
        }
    }).catch(e => {
        nmessage.error('启动自动翻阅失败: ' + e)
    })
}

const scrollInterval = ref(600)
const adbStatus = ref('')
const adbChecking = ref(false)
const adbConnected = ref(false)

const checkAdb = () => {
    adbChecking.value = true
    adbStatus.value = '检测中...'
    CheckAdb().then(v => {
        let resp = JSON.parse(v)
        adbConnected.value = resp.code == 200
        adbStatus.value = resp.msg
    }).catch(e => {
        adbConnected.value = false
        adbStatus.value = '检测失败: ' + e
    }).finally(() => {
        adbChecking.value = false
    })
}

const stopScroll = () => {
    scrollStore.isScrolling = false
    scrollPanelCollapsed.value = true
    StopAutoScroll().then(v => {
        nmessage.success('已停止自动翻阅')
        doSearch(1)
    }).catch(e => {
        nmessage.error('停止失败: ' + e)
    })
}

const scrollPanelCollapsed = ref(true)

let unlistenClientReq = null

const scrollDetailOpen = ref(false)

const formatScrollTime = (ts) => {
    if (!ts || ts <= 0) return '--'
    return new Date(ts * 1000).toLocaleString()
}

onMounted(() => {
    doSearch(1)
    // 恢复记住的同盟名，否则自动推断
    myUnion.value = localStorage.getItem('stzb_my_union') || ''
    if (!myUnion.value) {
        GetMyUnionName().then(v => {
            let resp = JSON.parse(v)
            if (resp.code == 200 && resp.data) {
                myUnion.value = resp.data
                localStorage.setItem('stzb_my_union', myUnion.value)
            }
        }).catch(() => {})
    }
    unlistenClientReq = EventsOn('clientRequest', (data) => {
        if (!capturing.value) return
        capturedRequests.value.unshift(data)
        if (capturedRequests.value.length > 200) capturedRequests.value.length = 200
    })
})

onUnmounted(() => {
    if (unlistenClientReq) EventsOff('clientRequest')
    if (capturing.value) DisableCaptureRequests()
})

// 自动翻阅/直连拉取结束（全局状态）时刷新列表并提示
watch(() => scrollStore.lastStop, (s) => {
    if (!s) return
    if (s.mode === 'direct') {
        const reasonMap = { timeReached: '已拉取到截止时间', noMoreData: '已到底，没有更多战报' }
        nmessage.success('免翻页拉取完成(' + (reasonMap[s.reason] || s.reason) + ')，累计 ' + (s.reportCount || 0) + ' 条')
    } else {
        const reasonMap = {
            timeReached: '已翻阅到截止时间',
            noMoreData: '已翻阅到底部，没有更多战报',
            userCancel: '用户手动停止'
        }
        nmessage.success('自动翻阅已停止(' + (reasonMap[s.reason] || s.reason) + ')，共翻页 ' + (s.scrolls || 0) + ' 次')
    }
    doSearch(1)
})

// 翻阅推进时实时刷新战报列表
watch(scrollProgress, () => {
    if (isScrolling.value) doSearch(1)
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
    ExportAllBattleReports().then(v => {
        let resp = JSON.parse(v)
        if (resp.code != 200) {
            nmessage.error(resp.msg)
            return
        }
        const allData = resp.data || []
        if (allData.length === 0) {
            nmessage.warning('没有数据可导出')
            return
        }
        let data = []
        data.push(['时间', '进攻方', '进攻方同盟', '进攻方兵力', '防守方', '防守方同盟', '防守方兵力', '地点', '结果'])
        allData.forEach(r => {
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
        nmessage.success(`导出成功，共 ${allData.length} 条`)
    }).catch(e => {
        nmessage.error('导出失败: ' + e)
    })
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

            <!-- 获取详细战报开关 -->
            <n-alert type="info" :bordered="false" style="margin-bottom:16px;">
                <template #header>
                    <div style="display:flex;align-items:center;gap:8px;justify-content:space-between;width:100%;">
                        <span style="display:flex;align-items:center;gap:8px;">
                            <Timer :size="16" />
                            获取详细战报
                        </span>
                    </div>
                </template>
                <div style="font-size:13px;margin-bottom:10px;">
                    用于查询队伍功能拉取战报使用，开启时无法获取攻城战报
                </div>
                <div style="display:flex;align-items:center;gap:12px;">
                    <n-button type="primary" @click="onEnableGetBattleReport">开启</n-button>
                    <n-button @click="onDisableGetBattleReport">关闭</n-button>
                </div>
            </n-alert>

            <!-- 自动翻阅控制栏 -->
            <n-alert v-if="!isScrolling" type="info" :bordered="false" style="margin-bottom:16px;">
                <template #header>
                    <div style="display:flex;align-items:center;gap:8px;justify-content:space-between;width:100%;">
                        <span style="display:flex;align-items:center;gap:8px;">
                            <Timer :size="16" />
                            自动翻阅（ADB模拟器模式）
                        </span>
                        <n-button text size="small" @click="showHelp=true" style="opacity:0.7;">
                            <template #icon><HelpCircle :size="16" /></template>
                            使用说明
                        </n-button>
                    </div>
                </template>
                <div style="display:flex;align-items:center;gap:16px;margin-bottom:10px;flex-wrap:wrap;">
                    <n-button size="small" :loading="adbChecking" @click="checkAdb">
                        <template #icon><Search :size="14" /></template>
                        检测设备
                    </n-button>
                    <n-tag v-if="adbStatus" :type="adbConnected ? 'success' : 'error'" size="small">{{ adbStatus }}</n-tag>
                </div>
                <div style="font-size:13px;margin-bottom:10px;">
                    通过 <b>adb 连接模拟器</b>（雷电/MuMu/夜神等）自动滑动翻阅，战报识别与截止时间判断基于抓包数据，无需占用鼠标，可后台运行。
                </div>
                <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap;">
                    <n-date-picker v-model:value="scrollTargetTime" type="datetime"
                        placeholder="请选择截止时间" clearable style="min-width:260px;" />
                    <div style="display:flex;align-items:center;gap:6px;">
                        <span style="font-size:12px;opacity:0.7;">翻页间隔(ms)</span>
                        <n-input-number v-model:value="scrollInterval" :min="200" :max="5000" :step="100" :style="{ width: '110px' }" />
                    </div>
                    <n-button type="success" @click="startScroll" icon-placement="right">
                        <template #icon><Play :size="16" /></template>
                        开始翻阅
                    </n-button>
                </div>
            </n-alert>

            <!-- 自动翻阅中（可折叠） -->
            <n-alert v-if="isScrolling" type="warning" :bordered="false" style="margin-bottom:16px;">
                <template #header>
                    <div style="display:flex;align-items:center;gap:8px;justify-content:space-between;width:100%;">
                        <span style="display:flex;align-items:center;gap:6px;flex:1;min-width:0;">
                            <Play :size="16" style="color:#18a058;flex-shrink:0;" />
                            <span style="white-space:nowrap;">自动翻阅进行中</span>
                            <span v-if="scrollPanelCollapsed" style="font-size:12px;opacity:0.7;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;">
                                {{ scrollProgressPct }}% · 已翻 {{ scrollCount }} 次 · {{ formatScrollTime(scrollLatestTime) }}
                            </span>
                        </span>
                        <div style="display:flex;align-items:center;gap:8px;flex-shrink:0;">
                            <n-button text size="small" @click="scrollPanelCollapsed = !scrollPanelCollapsed">
                                <template #icon>
                                    <ChevronDown v-if="scrollPanelCollapsed" :size="16" />
                                    <ChevronUp v-else :size="16" />
                                </template>
                                {{ scrollPanelCollapsed ? '展开' : '收起' }}
                            </n-button>
                            <n-button size="small" type="error" @click="stopScroll">
                                <template #icon><Square :size="14" /></template>
                                停止翻阅
                            </n-button>
                        </div>
                    </div>
                </template>
                <div v-if="!scrollPanelCollapsed" style="margin-top:8px;">
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

            <!-- 请求分析（方案B） -->
            <n-alert v-if="!isScrolling" type="info" :bordered="false" style="margin-bottom:16px;">
                <template #header>
                    <div style="display:flex;align-items:center;gap:8px;justify-content:space-between;width:100%;">
                        <span style="display:flex;align-items:center;gap:8px;">
                            <Timer :size="16" />
                            协议请求分析
                        </span>
                    </div>
                </template>
                <div style="font-size:13px;margin-bottom:10px;">
                    开启后在游戏内打开「同盟战报」并滚动一次，本面板会实时显示客户端发给服务器的请求包。
                </div>
                <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap;">
                    <n-button :type="capturing ? 'warning' : 'primary'" @click="toggleCapture">
                        <template #icon><Timer :size="16" /></template>
                        {{ capturing ? '停止分析' : '开始分析' }}
                    </n-button>
                    <n-button :disabled="capturedRequests.length === 0" @click="clearCapture">
                        清空
                    </n-button>
                    <n-tag v-if="capturing" type="success">监听中，共捕获 {{ capturedRequests.length }} 个请求</n-tag>
                </div>
                <div v-if="capturedRequests.length > 0" style="margin-top:12px;">
                    <n-collapse>
                        <n-collapse-item v-for="(req, idx) in capturedRequests" :key="idx"
                            :title="`#${capturedRequests.length - idx} cmdId=${req.cmdId} 长度=${req.len} 类型=${req.type}`">
                            <div class="req-body">{{ req.body || ('hex: ' + req.hex) }}</div>
                        </n-collapse-item>
                    </n-collapse>
                </div>
            </n-alert>

            <div class="search-bar">
                <div class="search-item">
                    <span class="search-label">战斗类型</span>
                    <n-select v-model:value="fightType" :options="fightTypeOptions" :style="{ width: '130px' }" />
                </div>
                <div class="search-item">
                    <span class="search-label">当前同盟</span>
                    <n-input v-model:value="myUnion" placeholder="自动推断,可修改" clearable :style="{ width: '180px' }" @keyup.enter="doSearch()" />
                </div>
                <div class="search-item">
                    <span class="search-label">玩家/地点</span>
                    <n-input v-model:value="searchName" placeholder="输入玩家名或地点" clearable :style="{ width: '200px' }" @keyup.enter="doSearch()" />
                </div>
                <div class="search-item">
                    <span class="search-label">主对象兵力≥</span>
                    <n-input-number v-model:value="minHp" :min="0" :step="1000" placeholder="兵力过滤" clearable :style="{ width: '150px' }" />
                </div>
                <n-button type="primary" @click="doSearch()" :loading="loading">
                    <template #icon><Search :size="16" /></template>
                    查询
                </n-button>
            </div>

            <!-- 免翻页直连拉取（还在开发中，功能未完成，仅作测试） -->
            <n-alert type="warning" :bordered="false" style="margin-bottom:16px;">
                <template #header>
                    <div style="display:flex;align-items:center;gap:8px;justify-content:space-between;width:100%;">
                        <span style="display:flex;align-items:center;gap:8px;">
                            <Timer :size="16" />
                            免翻页直连拉取（方案B）
                            <n-tag :bordered="false" type="error" size="small">开发中</n-tag>
                        </span>
                    </div>
                </template>
                <div style="font-size:13px;margin-bottom:10px;">
                    还在开发中：该功能尚未完成，直连协议尚未跑通，以下为测试区域。
                    需要先在「协议请求分析」里捕获过一次请求模板。先点 <b>测试直连</b> 验证服务器是否接受新连接。
                </div>
                <div style="display:flex;align-items:center;gap:12px;flex-wrap:wrap;">
                    <n-button type="primary" @click="testDirect">
                        测试直连
                    </n-button>
                    <n-button :type="directRunning ? 'error' : 'success'" @click="startDirectFetch">
                        {{ directRunning ? '停止拉取' : '开始免翻页拉取' }}
                    </n-button>
                    <n-tag v-if="directRunning" type="warning">拉取中: 页码 {{ directProgress.page }} 已捕获 {{ directProgress.reportCount || 0 }} 条</n-tag>
                </div>
                <div v-if="directResult" class="req-body" style="margin-top:10px;">{{ directResult }}</div>
            </n-alert>

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
                                <div class="side-settle">武勋: {{ r.attacker_gongxun || 0 }} 武策: {{ r.attacker_xwc || 0 }}</div>
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
                                <div class="side-settle">武勋: {{ r.defender_gongxun || 0 }} 武策: {{ r.defender_xwc || 0 }}</div>
                            </div>
                            <n-tag v-if="r.garrison === 1" size="small" type="warning" class="garrison-tag">拆迁</n-tag>
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
            <p><strong>ADB模式</strong>：通过 adb 连接模拟器（雷电/MuMu/夜神等，自动探测），程序直接向模拟器注入滑动翻页，战报识别、截止时间与到底判断均基于抓包数据，无需占用鼠标，可后台运行。</p>

            <h4 style="margin:16px 0 8px;">操作步骤</h4>
            <ol style="padding-left:20px;margin:0;">
                <li>启动模拟器，打开率土之滨，进入游戏并打开 <strong>同盟战报</strong> 页面（确保战报列表已加载出来）</li>
                <li>点 <strong>"检测设备"</strong> 确认已连接（自动匹配雷电 5555 / MuMu 16384 / 夜神 62001 等端口）</li>
                <li>选择一个截止时间（可选），如要翻到 <strong>7月11日12:00 之前</strong> 的战报，就选 <code>2026-07-11 12:00</code></li>
                <li>点击 <strong>"开始翻阅"</strong>，程序向模拟器注入滑动自动翻页，无需操作鼠标</li>
                <li>翻阅到截止时间或到底后自动停止，可随时点 <strong>"停止翻阅"</strong> 手动结束</li>
            </ol>

            <h4 style="margin:16px 0 8px;">注意事项</h4>
            <ul style="padding-left:20px;margin:0;">
                <li>ADB模式无法连接模拟器时，请先在模拟器设置中开启 adb 调试（雷电/夜神默认已开）</li>
                <li>翻阅速度约 0.9 秒/次，1000 页约 15 分钟</li>
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

.side-settle {
    font-size: 12px;
    opacity: 0.65;
}

.garrison-tag {
    align-self: flex-start;
    margin-top: 4px;
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

.req-body {
    font-size: 12px;
    line-height: 1.6;
    word-break: break-all;
    white-space: pre-wrap;
    max-height: 220px;
    overflow: auto;
    font-family: Consolas, Monaco, monospace;
}
</style>
