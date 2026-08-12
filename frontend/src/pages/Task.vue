<script setup>
import { ref, onMounted, computed, watch, onUnmounted } from 'vue'
import {
    NCard, NButton, NSpace, NTag, NEmpty,
    NInput, NFormItem, NSelect, NDatePicker, NPopconfirm, NModal,
    NDataTable, NStatistic, NSpin, NProgress, NAlert, NTabs, NTabPane,
    NCheckbox, NCheckboxGroup,
    useMessage, useDialog
} from 'naive-ui'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import {
    GetTeamGroup, CreateTask, GetTaskList, DeleteTask, GetTeamUser,
    EnableGetReport, DisableGetReport, GetReportNumByTaskId, StatisticsReport,
    GetTask, DeleteTaskReport, ExportTaskReport, GetSiegeSummary
} from '../../wailsjs/go/main/App'
import { formatTimestampMs, splitwid } from '@/utils/format'
import * as XLSX from 'xlsx'
import { Plus, RefreshCw, Eye, Play, Trash2, Eraser, Timer, Download } from 'lucide-vue-next'

const nmessage = useMessage()
const addtaskshow = ref(false)
const targetgroup = ref([])
const grouplist = ref([])
const tasktime = ref(new Date().getTime())
const taskname = ref('')
const taskpos = ref()
const createing = ref(false)
const tasks = ref([])
const taskNum = ref(0)
const loading = ref(false)

// 攻城任务 / 攻城战报汇总 子版块切换
const activeTab = ref('tasks')
const siegeRows = ref([])
const siegeCities = ref([])
const siegeLoading = ref(false)
// 四个子类是否参与导出（默认全部勾选）
const siegeExportSubs = ref(['present', 'kill', 'demolish', 'reports'])
const siegeExportSubOptions = [
    { label: '出勤', value: 'present' },
    { label: '灭敌', value: 'kill' },
    { label: '拆迁', value: 'demolish' },
    { label: '战报数', value: 'reports' },
]
// 汇总列头筛选状态：分组（单选）与城池（多选，与分组同为候选列表过滤）
const siegeGroupFilter = ref(null)
const siegeCityFilter = ref(null)
const siegeGroupOptions = computed(() => {
    const groups = new Set()
    siegeRows.value.forEach(r => { if (r.group) groups.add(r.group) })
    return [...groups].map(g => ({ label: g, value: g }))
})
const siegeCityOptions = computed(() => siegeCities.value.map(c => ({ label: c, value: c })))
// 扁平化：每行 = 成员 × 城池
const siegeFlatRows = computed(() => {
    const rows = []
    siegeRows.value.forEach(r => {
        siegeCities.value.forEach(c => {
            const s = (r.cities && r.cities[c]) || {}
            rows.push({
                role_id: r.role_id,
                name: r.name,
                group: r.group,
                city: c,
                present: !!s.present,
                kill: s.kill || 0,
                demolish: s.demolish || 0,
                reports: s.reports || 0,
            })
        })
    })
    return rows
})
const filteredSiegeRows = computed(() => {
    let rows = siegeFlatRows.value
    if (siegeGroupFilter.value) rows = rows.filter(r => r.group === siegeGroupFilter.value)
    if (siegeCityFilter.value && siegeCityFilter.value.length > 0) rows = rows.filter(r => siegeCityFilter.value.includes(r.city))
    return rows
})
const onSiegeFilters = (filters) => {
    siegeGroupFilter.value = (filters && filters.group) || null
    siegeCityFilter.value = (filters && filters.city) || null
}

// 扁平表格列：固定列 + 城池列（列头候选过滤，与分组一致）
const siegeColumns = computed(() => [
    { title: '角色ID', key: 'role_id', width: 90, fixed: 'left' },
    { title: '名字', key: 'name', minWidth: 100, fixed: 'left' },
    {
        title: '分组',
        key: 'group',
        width: 110,
        fixed: 'left',
        filterMultiple: false,
        filterOptions: siegeGroupOptions.value,
        filter: (value, row) => row.group === value,
        render: (row) => row.group || '-'
    },
    {
        title: '城池',
        key: 'city',
        width: 120,
        filterMultiple: true,
        filterOptions: siegeCityOptions.value,
        filter: (value, row) => row.city === value,
        render: (row) => row.city || '-'
    },
    {
        title: '出勤',
        key: 'present',
        width: 90,
        render: (row) => row.present ? '是' : '-'
    },
    {
        title: '灭敌',
        key: 'kill',
        width: 90,
        render: (row) => String(row.kill || 0)
    },
    {
        title: '拆迁',
        key: 'demolish',
        width: 90,
        render: (row) => String(row.demolish || 0)
    },
    {
        title: '战报数',
        key: 'reports',
        width: 90,
        render: (row) => String(row.reports || 0)
    },
])

const siegeScrollX = computed(() => 800)

const loadSiegeSummary = () => {
    siegeLoading.value = true
    GetSiegeSummary().then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            siegeCities.value = resp.data.cities || []
            siegeRows.value = resp.data.rows || []
        } else {
            nmessage.error(resp.msg)
        }
    }).catch(e => nmessage.error('获取攻城战报汇总失败: ' + e)).finally(() => {
        siegeLoading.value = false
    })
}

// 根据勾选的子类字段构建导出表头/数据行（宽表格式：每城一行四列）
const siegeSubMeta = { present: '出勤', kill: '灭敌', demolish: '拆迁', reports: '战报数' }
const exportSiegeBody = (cities) => {
    const subs = siegeExportSubs.value
    const header = ['角色ID', '名字', '分组']
    cities.forEach(c => {
        subs.forEach(s => header.push(`${c}-${siegeSubMeta[s]}`))
    })
    const body = siegeRows.value.map(r => {
        const line = [r.role_id, r.name, r.group]
        cities.forEach(c => {
            const st = (r.cities && r.cities[c]) || {}
            subs.forEach(s => {
                if (s === 'present') line.push(st.present ? '是' : '-')
                else line.push(st[s] || 0)
            })
        })
        return line
    })
    return { header, body }
}
const writeSiegeXlsx = (cities, sheetName, filename) => {
    if (cities.length === 0) {
        nmessage.warning('请先勾选要导出的城池')
        return false
    }
    if (siegeExportSubs.value.length === 0) {
        nmessage.warning('请至少勾选一个要导出的子类')
        return false
    }
    const { header, body } = exportSiegeBody(cities)
    const ws = XLSX.utils.aoa_to_sheet([header, ...body])
    ws['!cols'] = header.map(() => ({ wch: 14 }))
    const wb = XLSX.utils.book_new()
    XLSX.utils.book_append_sheet(wb, ws, sheetName)
    XLSX.writeFile(wb, filename)
    nmessage.success(`导出成功，共 ${body.length} 条`)
    return true
}

// 导出当前城：仅导出列头「城池」过滤勾选中的城池
const exportSiegeChecked = () => {
    if (filteredSiegeRows.value.length === 0) {
        nmessage.warning('暂无数据可导出')
        return
    }
    const cities = siegeCityFilter.value && siegeCityFilter.value.length > 0
        ? [...siegeCityFilter.value]
        : [...siegeCities.value]
    writeSiegeXlsx(cities, '攻城战报汇总-当前城', '攻城战报汇总_当前城.xlsx')
}

// 导出所有城池（保持原格式）
const exportSiegeAll = () => {
    if (filteredSiegeRows.value.length === 0) {
        nmessage.warning('暂无数据可导出')
        return
    }
    writeSiegeXlsx([...siegeCities.value], '攻城战报汇总', '攻城战报汇总.xlsx')
}

// 城池分配助手：对每个目标分组，从用户填写的候选城池中选出离组内所有成员距离总和最近的城
const assignCities = ref('')
const assignLoading = ref(false)
const assignResults = ref([])

// pos 为 x*10000+y 格式（x 三位数，y 补足四位数）
const parsePos = (pos) => {
    const p = Math.abs(pos)
    return { x: Math.floor(p / 10000), y: p % 10000 }
}

const calcAssign = () => {
    const lines = assignCities.value.split('\n').map(l => l.trim()).filter(Boolean)
    const cities = []
    lines.forEach(l => {
        const parts = l.split(/[,，\s]+/)
        const x = parseInt(parts[0], 10)
        const y = parseInt(parts[1], 10)
        if (!isNaN(x) && !isNaN(y)) cities.push({ x, y })
    })
    if (cities.length === 0) {
        nmessage.warning('请至少填写一个候选城池坐标（每行一个，格式：x,y）')
        return
    }
    if (targetgroup.value.length === 0) {
        nmessage.warning('请先选择目标分组')
        return
    }
    assignLoading.value = true
    GetTeamUser("").then(v => {
        let resp = JSON.parse(v)
        if (resp.code != 200) {
            nmessage.error(resp.msg)
            return
        }
        const users = resp.data || []
        assignResults.value = targetgroup.value.map(g => {
            const members = users.filter(u => u.group === g && u.pos > 0)
            if (members.length === 0) {
                return { group: g, memberCount: 0, city: null, totalDist: 0, empty: true }
            }
            let best = null
            cities.forEach(c => {
                let dist = 0
                members.forEach(m => {
                    const mp = parsePos(m.pos)
                    dist += Math.hypot(c.x - mp.x, c.y - mp.y)
                })
                if (!best || dist < best.dist) best = { city: c, dist }
            })
            return { group: g, memberCount: members.length, city: best.city, totalDist: Math.round(best.dist * 100) / 100 }
        })
        if (assignResults.value.every(r => r.empty)) {
            nmessage.warning('所选分组内没有成员的坐标数据')
        }
    }).catch(e => {
        nmessage.error('获取成员坐标失败:' + e)
    }).finally(() => {
        assignLoading.value = false
    })
}

const fillAssignPos = (r) => {
    if (!r.city) return
    taskpos.value = [String(r.city.x), String(r.city.y)]
    nmessage.success(`已填入任务坐标：${r.city.x},${r.city.y}`)
}

const createTask = () => {
    createing.value = true
    CreateTask(taskname.value, tasktime.value, targetgroup.value, taskpos.value).then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            nmessage.success(resp.msg)
            taskname.value = ''
            targetgroup.value = []
            taskpos.value = []
            getTaskList()
        } else {
            nmessage.error(resp.msg)
        }
        createing.value = false
    }).catch(e => {
        createing.value = false
        nmessage.error(e)
    })
}

const delTask = (id) => {
    DeleteTask(id).then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            nmessage.success(resp.msg)
            getTaskList()
        } else {
            nmessage.error(resp.msg)
        }
    })
}

const delTaskReport = (id) => {
    DeleteTaskReport(id).then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            nmessage.success(resp.msg)
            getTaskList()
        } else {
            nmessage.error(resp.msg)
        }
    })
}

function getTaskList() {
    loading.value = true
    tasks.value = []
    taskNum.value = 0
    GetTaskList().then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            tasks.value = resp.data
            taskNum.value = resp.data.length
        } else {
            nmessage.error(resp.msg)
        }
    }).finally(() => {
        loading.value = false
    })
}

onMounted(() => {
    GetTeamGroup().then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            let data = resp.data
            grouplist.value = []
            data.forEach(e => {
                grouplist.value.push({ label: e, value: e })
            })
        }
    })
    getTaskList()
})

const showModal = ref(false)
const getReporting = ref(false)
const reportNum = ref(0)
const getReportNumTimer = ref(null)
const inStatistics = ref(false)
const curtaskid = ref(0)

// 时间定位相关
const endTimeTarget = ref(null) // 用户设定的截止时间 (ms)
const latestReportTime = ref(0) // 最近一条战报的时间
const reportProgressPct = ref(0) // 进度百分比
const timeReached = ref(false) // 是否已到达截止时间
const showEndTimePicker = ref(false)

const enableGetReport = (id, pos) => {
    showModal.value = true
    endTimeTarget.value = null
    latestReportTime.value = 0
    reportProgressPct.value = 0
    timeReached.value = false
    showEndTimePicker.value = true
    reportNum.value = 0
    getReporting.value = false
    inStatistics.value = false
    curtaskid.value = id
}

const startCapture = () => {
    const endTime = endTimeTarget.value ? Math.floor(endTimeTarget.value / 1000) : 0
    EnableGetReport(curtaskid.value, endTime)
    getReporting.value = true
    showEndTimePicker.value = false

    getReportNumTimer.value = setInterval(() => {
        GetReportNumByTaskId(curtaskid.value).then(v => {
            let resp = JSON.parse(v)
            if (resp.code == 200) {
                reportNum.value = resp.data.count
            }
        })
    }, 1000)
}

const stopReport = () => {
    clearInterval(getReportNumTimer.value)
    getReportNumTimer.value = null
    getReporting.value = false
    inStatistics.value = false
    timeReached.value = false
    DisableGetReport()
}

watch(activeTab, (val) => {
    if (val === 'siege' && siegeRows.value.length === 0) {
        loadSiegeSummary()
    }
})

watch(showModal, (val) => {
    if (!val) stopReport()
})

// 监听后端推送的进度
let unlistenProgress = null
let unlistenTimeReached = null

onMounted(() => {
    unlistenProgress = EventsOn('reportProgress', (data) => {
        if (data.latestTime && data.latestTime > 0) {
            latestReportTime.value = data.latestTime
        }
        reportNum.value = data.count || reportNum.value
        if (endTimeTarget.value && data.latestTime > 0) {
            const endSec = Math.floor(endTimeTarget.value / 1000)
            const nowSec = data.latestTime
            if (nowSec >= endSec) {
                reportProgressPct.value = 100
            } else {
                reportProgressPct.value = Math.min(95, Math.round((nowSec / endSec) * 100))
            }
        }
    })
    unlistenTimeReached = EventsOn('reportTimeReached', () => {
        timeReached.value = true
    })
})

onUnmounted(() => {
    if (unlistenProgress) EventsOff('reportProgress')
    if (unlistenTimeReached) EventsOff('reportTimeReached')
})

// 格式化时间戳(秒)
const formatSecTimestamp = (ts) => {
    if (!ts) return '--'
    return new Date(ts * 1000).toLocaleString()
}

const startCaptureText = computed(() => {
    if (endTimeTarget.value) {
        return `开始获取(截止: ${new Date(endTimeTarget.value).toLocaleString()})`
    }
    return '开始获取战报(不限时间)'
})

const statistics = () => {
    clearInterval(getReportNumTimer.value)
    getReportNumTimer.value = null
    getReporting.value = false
    inStatistics.value = true
    StatisticsReport(curtaskid.value).then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            nmessage.success(resp.msg)
            curtaskid.value = 0
            getTaskList()
        } else {
            nmessage.error(resp.msg)
        }
        inStatistics.value = false
        showModal.value = false
    }).catch(e => {
        inStatistics.value = false
        nmessage.error('统计考勤数据失败:' + e)
    })
}

const showModal2 = ref(false)
const taskDetail = ref({})
const getTaskDetail = (id) => {
    taskDetail.value = {}
    showModal2.value = true
    GetTask(id).then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            taskDetail.value = resp.data
        } else {
            nmessage.error(resp.msg)
        }
    }).catch(e => {
        nmessage.error('获取考勤数据失败:' + e)
    })
}

const exportExcel = () => {
    let data = []
    data.push(['名字', '分组', '主力', '拆迁', '主力次数', '拆迁次数'])
    Object.values(taskDetail.value.user_list).forEach(v => {
        data.push([v.name, v.group, v.atk_team_num, v.dis_team_num, v.atk_num, v.dis_num])
    })
    const ws = XLSX.utils.aoa_to_sheet(data)
    const wb = XLSX.utils.book_new()
    XLSX.utils.book_append_sheet(wb, ws, 'Sheet1')
    XLSX.writeFile(wb, `${taskDetail.value.name}考勤表.xlsx`)
}

const exportEnhanced = (id) => {
    ExportTaskReport(id).then(v => {
        let r = JSON.parse(v)
        if (r.code == 200) {
            const d = r.data
            const rows = d.rows || []
            const sheetData = [
                [`考勤报告: ${d.task_name || ''}`],
                [`坐标: ${d.pos || ''}`, `目标分组: ${(d.target || []).join(',')}`],
                [`目标人数: ${d.total}`, `实到: ${d.present}`, `缺席: ${d.absent}`, `出勤率: ${d.rate}%`],
                [],
                ['名字', '分组', '是否到位', '主力队伍', '拆迁队伍', '主力次数', '拆迁次数', '总次数']
            ]
            rows.forEach(r => {
                sheetData.push([r.name, r.group, r.present, r.atk_teams, r.dis_teams, r.atk_num, r.dis_num, r.total])
            })

            const ws = XLSX.utils.aoa_to_sheet(sheetData)
            ws['!merges'] = [
                { s: { r: 0, c: 0 }, e: { r: 0, c: 7 } },
                { s: { r: 1, c: 0 }, e: { r: 1, c: 7 } },
                { s: { r: 2, c: 0 }, e: { r: 2, c: 7 } },
            ]
            const wb = XLSX.utils.book_new()
            XLSX.utils.book_append_sheet(wb, ws, '考勤报告')
            XLSX.writeFile(wb, `${d.task_name || '考勤'}_报告.xlsx`)
            nmessage.success('导出成功')
        } else {
            nmessage.error(r.msg)
        }
    }).catch(e => nmessage.error('导出失败:' + e))
}

const detailColumns = [
    { title: '名称', key: 'name', sorter: 'default', defaultSortOrder: false },
    { title: '分组', key: 'group', sorter: 'default', defaultSortOrder: false },
    { title: '主力', key: 'atk_team_num', sorter: (a, b) => a.atk_team_num - b.atk_team_num, defaultSortOrder: false },
    { title: '拆迁', key: 'dis_team_num', sorter: (a, b) => a.dis_team_num - b.dis_team_num, defaultSortOrder: false },
    { title: '主力次数', key: 'atk_num', sorter: (a, b) => a.atk_num - b.atk_num, defaultSortOrder: 'descend' },
    { title: '拆迁次数', key: 'dis_num', sorter: (a, b) => a.dis_num - b.dis_num, defaultSortOrder: false },
]

const detailData = computed(() => {
    if (!taskDetail.value.user_list) return []
    return Object.values(taskDetail.value.user_list)
})
</script>

<template>
    <n-modal v-model:show="addtaskshow" preset="card" title="新增任务" size="huge" :bordered="false"
        style="width: 640px" :mask-closable="false" to="body">
        <div class="modal-form">
            <n-form-item label="任务名称">
                <n-input v-model:value="taskname" placeholder="例如：内黄LV5 或者你也可以随意填写" />
            </n-form-item>
            <n-form-item label="任务坐标">
                <n-input pair separator="，" :placeholder="['X坐标', 'Y坐标']" v-model:value="taskpos" clearable />
            </n-form-item>
            <n-form-item label="任务时间">
                <n-date-picker v-model:value="tasktime" type="datetime" style="width: 100%;" />
            </n-form-item>
            <n-form-item label="目标分组">
                <n-select v-model:value="targetgroup" multiple :options="grouplist" placeholder="请选择分组" />
            </n-form-item>

            <!-- 城池分配助手：对每个分组自动分配离组内成员总距离最近的城 -->
            <n-alert type="info" :bordered="false" style="margin-bottom:8px;">
                <template #header>
                    <div style="display:flex;align-items:center;gap:8px;">
                        <Timer :size="15" />
                        城池分配助手
                    </div>
                </template>
                <div style="display:flex;flex-direction:column;gap:8px;margin-top:8px;">
                    <n-input v-model:value="assignCities" type="textarea" :rows="3"
                        placeholder="候选城池坐标，每行一个，格式：x,y（例如 580,1032）" />
                    <div style="display:flex;align-items:center;gap:8px;">
                        <n-button size="small" type="primary" @click="calcAssign" :loading="assignLoading">
                            按分组自动分配
                        </n-button>
                        <span style="font-size:12px;opacity:0.7;">
                            为每个目标分组选出到组内所有成员坐标距离总和最小的城池
                        </span>
                    </div>
                    <div v-if="assignResults.length > 0" class="assign-results">
                        <div class="assign-row" v-for="r in assignResults" :key="r.group">
                            <div class="assign-info">
                                <span class="assign-group">{{ r.group }}</span>
                                <span v-if="r.empty">组内暂无成员坐标</span>
                                <span v-else>推荐城：{{ r.city.x }},{{ r.city.y }} · 距离和：{{ r.totalDist }} · 成员 {{ r.memberCount }} 人</span>
                            </div>
                            <n-button size="tiny" type="success" :disabled="r.empty" @click="fillAssignPos(r)">
                                填入
                            </n-button>
                        </div>
                    </div>
                </div>
            </n-alert>
        </div>
        <template #footer>
            <n-space justify="end">
                <n-button strong secondary type="primary" :loading="createing" @click="createTask">
                    添加
                </n-button>
                <n-button strong secondary type="error" @click="addtaskshow = false">
                    关闭
                </n-button>
            </n-space>
        </template>
    </n-modal>

    <n-modal v-model:show="showModal" preset="card" title="攻城考勤" size="huge" :bordered="false"
        style="width: 680px" :mask-closable="false" to="body">
        <div class="report-modal">
            <p class="report-tip">请前往游戏中，到攻城任务坐标位置查看同盟战报，并勾选守城军士（否则获取不了拆迁战报）。然后一直往下滑直到没有战报为止</p>

            <!-- 时间定位设置 -->
            <n-alert v-if="showEndTimePicker" type="info" :bordered="false" style="margin-bottom:16px;text-align:left;">
                <template #header>
                    <div style="display:flex;align-items:center;gap:8px;">
                        <Timer :size="16" />
                        智能翻阅
                    </div>
                </template>
                <div style="font-size:13px;margin-bottom:10px;">
                    设定截止时间后，翻阅时会自动跟踪进度：翻到该时间点之后存为的战报会被自动跳过。翻越早的战报，进度越接近100%。
                </div>
                <div style="display:flex;align-items:center;gap:12px;">
                    <n-date-picker v-model:value="endTimeTarget" type="datetime"
                        placeholder="选择截止时间（可选）" clearable style="flex:1;" />
                    <n-button type="primary" :disabled="!getReporting && false"
                        @click="startCapture">
                        {{ startCaptureText }}
                    </n-button>
                </div>
            </n-alert>

            <!-- 实时进度 -->
            <div v-if="getReporting" style="margin-bottom:16px;text-align:left;">
                <n-alert v-if="timeReached" type="warning" :bordered="false" style="margin-bottom:12px;">
                    检测到战报时间已早于截止时间，战报已自动跳过！如已翻阅到底部可点击"统计考勤数据"
                </n-alert>
                <div v-if="endTimeTarget" style="margin-bottom:8px;">
                    <div style="display:flex;justify-content:space-between;font-size:12px;margin-bottom:4px;">
                        <span>翻阅进度</span>
                        <span>{{ reportProgressPct }}%</span>
                    </div>
                    <n-progress :value="reportProgressPct" :height="8" :border-radius="4"
                        :color="timeReached ? '#f0ad4e' : '#18a058'" />
                </div>
                <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px;">
                    <n-statistic label="已获取战报" :value="reportNum">
                        <template #suffix>
                            <span style="font-size:14px;">封</span>
                        </template>
                    </n-statistic>
                    <n-statistic label="最新战报时间" :value="formatSecTimestamp(latestReportTime)">
                    </n-statistic>
                </div>
            </div>

            <div v-if="!getReporting && !showEndTimePicker" class="report-counter">
                <n-statistic label="已获取战报" :value="reportNum">
                    <template #suffix>
                        <span style="font-size:14px;">封</span>
                    </template>
                </n-statistic>
            </div>
        </div>
        <template #footer>
            <n-space>
                <n-button strong secondary type="info" :loading="true" v-if="getReporting">
                    获取战报中
                </n-button>
                <n-button v-if="getReporting || reportNum > 0" strong secondary type="success"
                    @click="statistics" :loading="inStatistics">
                    {{ inStatistics ? '统计考勤数据中' : '已获取完战报，开始统计考勤数据' }}
                </n-button>
                <n-button strong secondary @click="showModal = false">
                    {{ getReporting ? '取消并停止' : '关闭' }}
                </n-button>
            </n-space>
        </template>
    </n-modal>

    <n-modal v-model:show="showModal2" preset="card" title="考勤详情" size="huge" :bordered="false"
        style="width: 1024px" :mask-closable="false" to="body">
        <div class="detail-modal">
            <n-space style="margin-bottom: 16px;">
                <n-button type="primary" @click="exportExcel">
                    导出为表格
                </n-button>
                <n-button type="success" @click="exportEnhanced(taskDetail.value.id)">
                    导出增强版(含汇总)
                </n-button>
            </n-space>
            <n-data-table :columns="detailColumns" :data="detailData" :bordered="true" :single-line="false"
                :max-height="500" />
        </div>
    </n-modal>

    <div class="page-task">
        <n-card class="page-card" embedded>
            <n-tabs v-model:value="activeTab" type="line" animated>
                <n-tab-pane name="tasks" tab="攻城任务">
                    <div class="page-header">
                        <div class="page-header-info">
                            <h2 class="page-title">攻城任务</h2>
                            <p class="page-desc">任务数量：{{ taskNum }}</p>
                        </div>
                        <n-space>
                            <n-button @click="getTaskList" :loading="loading">
                                <template #icon><RefreshCw :size="16" /></template>
                                刷新
                            </n-button>
                            <n-button type="primary" @click="addtaskshow = true">
                                <template #icon><Plus :size="16" /></template>
                                新增任务
                            </n-button>
                        </n-space>
                    </div>

                    <div class="task-list" v-if="tasks.length > 0">
                        <div class="task-card" v-for="task in tasks" :key="task.id">
                            <div class="task-header">
                                <div class="task-title-row">
                                    <span class="task-name">{{ task.name }}</span>
                                    <span class="task-coords">({{ splitwid(task.pos) }})</span>
                                </div>
                            </div>

                            <div class="task-stats">
                                <div class="task-stat-item">
                                    <span class="task-stat-label">目标分组</span>
                                    <div class="task-stat-tags">
                                        <n-tag :bordered="false" type="info" size="small" v-for="g in task.target" :key="g">
                                            {{ g }}
                                        </n-tag>
                                    </div>
                                </div>
                                <div class="task-stat-item">
                                    <span class="task-stat-label">目标人数</span>
                                    <span class="task-stat-value">{{ task.target_user_num }}</span>
                                </div>
                                <div class="task-stat-item">
                                    <span class="task-stat-label">实到人数</span>
                                    <span class="task-stat-value highlight">{{ task.complete_user_num }}</span>
                                </div>
                                <div class="task-stat-item">
                                    <span class="task-stat-label">任务时间</span>
                                    <span class="task-stat-value">{{ formatTimestampMs(task.time) }}</span>
                                </div>
                            </div>

                            <div class="task-actions">
                                <n-button size="small" @click="getTaskDetail(task.id)">
                                    <template #icon><Eye :size="14" /></template>
                                    考勤详情
                                </n-button>
                                <n-button type="info" size="small" @click="enableGetReport(task.id, task.pos)">
                                    <template #icon><Play :size="14" /></template>
                                    开始考勤
                                </n-button>
                                <n-popconfirm @positive-click="delTaskReport(task.id)" :show-icon="false">
                                    <template #trigger>
                                        <n-button type="warning" size="small">
                                            <template #icon><Eraser :size="14" /></template>
                                            清理战报
                                        </n-button>
                                    </template>
                                    确认清理战报吗？数据删除后无法恢复。<br>清理战报可以减少统计考勤的耗时
                                </n-popconfirm>
                                <n-popconfirm @positive-click="delTask(task.id)" :show-icon="false">
                                    <template #trigger>
                                        <n-button type="error" size="small">
                                            <template #icon><Trash2 :size="14" /></template>
                                            删除任务
                                        </n-button>
                                    </template>
                                    确认删除该任务吗？
                                </n-popconfirm>
                            </div>
                        </div>
                    </div>
                    <n-empty v-else description="暂无攻城任务" style="padding: 60px 0;" />
                </n-tab-pane>

                <n-tab-pane name="siege" tab="攻城战报汇总">
                    <div class="siege-toolbar">
                        <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;">
                            <span style="font-size:13px;opacity:0.7;line-height:1.6;">
                                统计当前同盟成员的攻城战报：每个成员（角色ID/分组）在每座城池的
                                <b>出勤</b>（是否攻击该城）、<b>灭敌数</b>（防守方兵力变化：战前−战后）、<b>拆迁值</b>（耐久下降累加）、<b>战报数</b>（该城战报总条数）。
                                <br/>点击表头<b>分组</b>或<b>城池</b>列可通过候选列表勾选过滤；导出时分<b>当前城</b>（过滤勾选的城）与<b>所有城</b>。
                            </span>
                        </div>
                        <div class="siege-check-row">
                            <span class="siege-check-label">导出子类：</span>
                            <n-checkbox-group v-model:value="siegeExportSubs" size="small">
                                <n-checkbox v-for="opt in siegeExportSubOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
                            </n-checkbox-group>
                        </div>
                        <div class="siege-check-row">
                            <n-button size="small" @click="loadSiegeSummary" :loading="siegeLoading">
                                <template #icon><RefreshCw :size="14" /></template>
                                刷新汇总
                            </n-button>
                            <n-button size="small" type="success" @click="exportSiegeChecked" :disabled="filteredSiegeRows.length === 0">
                                <template #icon><Download :size="14" /></template>
                                导出当前城
                            </n-button>
                            <n-button size="small" type="success" @click="exportSiegeAll" :disabled="filteredSiegeRows.length === 0">
                                <template #icon><Download :size="14" /></template>
                                导出所有城
                            </n-button>
                        </div>
                    </div>
                    <n-data-table
                        :columns="siegeColumns"
                        :data="filteredSiegeRows"
                        :bordered="true"
                        :single-line="false"
                        :loading="siegeLoading"
                        :max-height="560"
                        :scroll-x="siegeScrollX"
                        @update:filters="onSiegeFilters"
                    />
                    <n-empty v-if="!siegeLoading && siegeRows.length === 0" description="暂无攻城战报数据（需先在游戏内抓取攻城战报）" style="padding: 40px 0;" />
                </n-tab-pane>
            </n-tabs>
        </n-card>
    </div>
</template>

<style scoped lang="scss">
.page-task {
    display: flex;
    flex-direction: column;
}

.page-card {
    border-radius: 12px;
}

.page-header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    margin-bottom: 20px;
}

.siege-toolbar {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 12px;
    flex-wrap: wrap;
}

.siege-check-row {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    width: 100%;
    padding: 6px 10px;
    background: var(--color-bg);
    border-radius: 8px;
}

.siege-check-label {
    font-size: 13px;
    font-weight: 600;
    color: var(--color-text-secondary);
    white-space: nowrap;
}

.page-title {
    font-size: 20px;
    font-weight: 600;
    color: var(--color-text);
    margin-bottom: 4px;
}

.page-desc {
    font-size: 13px;
    color: var(--color-text-secondary);
}

.task-list {
    display: flex;
    flex-direction: column;
    gap: 12px;
}

.task-card {
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: 10px;
    padding: 20px;
    transition: box-shadow 0.2s, transform 0.2s;

    &:hover {
        box-shadow: 0 4px 12px rgba(0, 0, 0, 0.08);
        transform: translateY(-1px);
    }
}

.task-header {
    margin-bottom: 16px;
}

.task-title-row {
    display: flex;
    align-items: baseline;
    gap: 8px;
}

.task-name {
    font-size: 16px;
    font-weight: 600;
    color: var(--color-text);
}

.task-coords {
    font-size: 13px;
    color: var(--color-text-secondary);
}

.task-stats {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px 24px;
    margin-bottom: 16px;
    padding: 16px;
    background: var(--color-bg);
    border-radius: 8px;
}

.task-stat-item {
    display: flex;
    flex-direction: column;
    gap: 4px;
}

.task-stat-label {
    font-size: 12px;
    color: var(--color-text-secondary);
}

.task-stat-value {
    font-size: 14px;
    color: var(--color-text);
    font-weight: 500;

    &.highlight {
        color: var(--color-accent);
    }
}

.task-stat-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
}

.task-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
}

.modal-form {
    display: flex;
    flex-direction: column;
    gap: 8px;
}

.assign-results {
    display: flex;
    flex-direction: column;
    gap: 6px;
}

.assign-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    padding: 8px 10px;
    background: var(--color-bg);
    border-radius: 6px;
}

.assign-info {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    flex-wrap: wrap;
}

.assign-group {
    font-weight: 600;
    color: var(--color-primary);
}

.report-modal {
    text-align: center;
}

.report-tip {
    font-size: 14px;
    color: var(--color-text-secondary);
    margin-bottom: 24px;
    line-height: 1.6;
}

.report-counter {
    display: flex;
    justify-content: center;
    padding: 20px 0;
}

.detail-modal {
    overflow: auto;

    :deep(.n-data-table-sorter) {
        opacity: 0;
        color: var(--color-primary);
        transition: opacity 0.15s;
    }

    :deep(th:hover .n-data-table-sorter) {
        opacity: 1;
    }

    :deep(.n-data-table-th--sorting .n-data-table-sorter) {
        opacity: 1;
        color: var(--color-primary);
    }
}
</style>
