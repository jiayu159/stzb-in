<script setup>
import { ref, onMounted } from 'vue'
import { NCard, NButton, NSpin, NEmpty, NTag, NDataTable, NPagination, NGi, NGrid, NStatistic, NSpace, useMessage } from 'naive-ui'
import { GetMemberActivity } from '../../wailsjs/go/main/App'
import { RefreshCw, Download, Users, Activity, Clock } from 'lucide-vue-next'
import * as XLSX from 'xlsx'

const nmessage = useMessage()
const loading = ref(false)
const members = ref([])
const page = ref(1)
const pageSize = ref(50)
const total = ref(0)

const loadData = () => {
    loading.value = true
    GetMemberActivity().then(v => {
        let r = JSON.parse(v)
        if (r.code == 200) {
            members.value = r.data || []
            total.value = members.value.length
        } else nmessage.error(r.msg)
    }).catch(e => nmessage.error(e)).finally(() => { loading.value = false })
}

onMounted(loadData)

const exportExcel = () => {
    if (members.value.length === 0) {
        nmessage.warning('没有数据可导出')
        return
    }
    let data = []
    data.push(['排名', '名称', '分组', '武勋', '势力', '进攻场次', '防守场次', '总场次', '翻地次数', '加入天数', '最近参战', '24h在线', '活跃度', '活跃等级'])
    members.value.forEach((m, i) => {
        const s = m.score || 0
        data.push([
            i + 1,
            m.name,
            m.group || '-',
            m.wu || 0,
            m.power || 0,
            m.atk_count || 0,
            m.def_count || 0,
            m.total_bat || 0,
            m.land_count || 0,
            m.join_days || 0,
            formatTime(m.last_time),
            m.active_24h ? '在线' : '离线',
            s.toFixed(1),
            scoreLabel(s),
        ])
    })
    const ws = XLSX.utils.aoa_to_sheet(data)
    const wb = XLSX.utils.book_new()
    XLSX.utils.book_append_sheet(wb, ws, '活跃度')
    XLSX.writeFile(wb, `活跃度分析_${new Date().toLocaleDateString().replace(/\//g, '-')}.xlsx`)
    nmessage.success('导出成功')
}

const formatTime = (ts) => {
    if (!ts) return '从未参战'
    const d = new Date(ts * 1000)
    const pad = (n) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${pad(d.getMonth()+1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const scoreColor = (s) => {
    if (s >= 100) return '#22c55e'
    if (s >= 50) return '#3b82f6'
    if (s >= 20) return '#f59e0b'
    return '#ef4444'
}

const scoreLabel = (s) => {
    if (s >= 100) return '核心成员'
    if (s >= 50) return '活跃成员'
    if (s >= 20) return '普通成员'
    return '不活跃'
}

const tableData = computed(() => {
    const start = (page.value - 1) * pageSize.value
    return members.value.slice(start, start + pageSize.value)
})

import { computed } from 'vue'

const columns = [
    { title: '排名', key: 'idx', width: 60, align: 'center',
        render: (_, i) => (page.value - 1) * pageSize.value + i + 1
    },
    { title: '名称', key: 'name', width: 120, ellipsis: { tooltip: true } },
    { title: '分组', key: 'group', width: 100,
        render: (row) => row.group ? row.group : '-'
    },
    { title: '武勋', key: 'wu', width: 80, align: 'center', sorter: (a, b) => a.wu - b.wu },
    { title: '势力', key: 'power', width: 70, align: 'center', sorter: (a, b) => a.power - b.power },
    { title: '进攻场次', key: 'atk_count', width: 90, align: 'center', sorter: (a, b) => a.atk_count - b.atk_count },
    { title: '防守场次', key: 'def_count', width: 90, align: 'center', sorter: (a, b) => a.def_count - b.def_count },
    { title: '总场次', key: 'total_bat', width: 80, align: 'center', sorter: (a, b) => a.total_bat - b.total_bat },
    { title: '翻地次数', key: 'land_count', width: 90, align: 'center', sorter: (a, b) => a.land_count - b.land_count,
        render: (row) => (row.land_count || 0) > 0
            ? h(NTag, { type: 'success', size: 'tiny', bordered: false }, () => row.land_count)
            : '0'
    },
    { title: '加入天数', key: 'join_days', width: 80, align: 'center' },
    { title: '最近参战', key: 'last_time', width: 140,
        render: (row) => formatTime(row.last_time)
    },
    {
        title: '24h在线', key: 'active_24h', width: 80, align: 'center',
        render: (row) => row.active_24h
            ? h(NTag, { type: 'success', size: 'tiny', bordered: false }, () => '在线')
            : h(NTag, { type: 'default', size: 'tiny', bordered: false }, () => '离线')
    },
    {
        title: '活跃度', key: 'score', width: 120, align: 'center',
        sorter: (a, b) => a.score - b.score, defaultSortOrder: 'descend',
        render: (row) => {
            const s = row.score || 0
            return h('div', { style: 'display:flex; align-items:center; gap:6px; justify-content:center;' }, [
                h('div', { style: `width:60px; height:6px; background:#e5e7eb; border-radius:3px; overflow:hidden;` }, [
                    h('div', { style: `width:${Math.min(s, 100)}%; height:100%; background:${scoreColor(s)}; border-radius:3px; transition:width 0.3s;` })
                ]),
                h('span', { style: `font-weight:600; color:${scoreColor(s)}; font-size:12px;` }, s.toFixed(0))
            ])
        }
    },
]

import { h } from 'vue'
</script>

<template>
    <div class="page-activity">
        <n-card class="page-card" embedded>
            <div class="page-header">
                <div class="page-header-info">
                    <h2 class="page-title">成员活跃度分析</h2>
                    <p class="page-desc">基于战报数据评估成员活跃度</p>
                </div>
                <n-space align="center">
                    <n-button @click="loadData" :loading="loading">
                        <template #icon><RefreshCw :size="16" /></template>
                        刷新
                    </n-button>
                    <n-button type="primary" @click="exportExcel" :disabled="members.length === 0">
                        <template #icon><Download :size="16" /></template>
                        导出表格
                    </n-button>
                </n-space>
            </div>

            <n-alert type="info" :show-icon="true" closable style="border-radius: 8px; margin-bottom: 20px; font-size: 13px;">
                <template #header>使用说明</template>
                活跃度评分基于以下数据综合计算：<br>
                 <strong>战报数 × 0.4 + 武勋/1000 × 0.3 + 24h在线 +20 + 日均战报 × 5</strong><br><br>
                 评分等级：<strong style="color:#22c55e">≥100 核心成员</strong> ｜ <strong style="color:#3b82f6">≥50 活跃</strong> ｜ <strong style="color:#f59e0b">≥20 普通</strong> ｜ <strong style="color:#ef4444">&lt;20 不活跃</strong><br>
                 翻地次数：本盟成员占领其他同盟领地（土地/要塞，不含沃土）的次数，依据战报中的"占领了/拆除"描述统计（不计入活跃度评分）<br>
                 数据需要先同步成员（同盟成员页面）并抓取战报后才会更新
            </n-alert>

            <n-grid :cols="4" :x-gap="16" :y-gap="16" style="margin-bottom: 20px;">
                <n-gi>
                    <n-card embedded size="small">
                        <n-statistic label="成员总数" :value="members.length" />
                    </n-card>
                </n-gi>
                <n-gi>
                    <n-card embedded size="small">
                        <n-statistic label="24h在线" :value="members.filter(m => m.active_24h).length" />
                    </n-card>
                </n-gi>
                <n-gi>
                    <n-card embedded size="small">
                        <n-statistic label="核心成员(>100分)" :value="members.filter(m => (m.score||0) >= 100).length" />
                    </n-card>
                </n-gi>
                <n-gi>
                    <n-card embedded size="small">
                        <n-statistic label="不活跃(<20分)" :value="members.filter(m => (m.score||0) < 20).length" />
                    </n-card>
                </n-gi>
            </n-grid>

            <div class="pagination-wrap" v-if="total > pageSize">
                <n-pagination v-model:page="page" :page-size="pageSize" :item-count="total" />
            </div>

            <div class="result-area" v-if="loading">
                <div class="loading-wrap"><n-spin size="medium" /><span>加载中...</span></div>
            </div>
            <div v-else-if="members.length === 0">
                <n-empty description="暂无成员数据" style="padding: 60px 0;" />
            </div>
            <div v-else>
                <n-data-table :columns="columns" :data="tableData" :bordered="false" size="small" :scroll-x="1200" />
            </div>
        </n-card>
    </div>
</template>

<style scoped lang="scss">
.page-activity { display: flex; flex-direction: column; }
.page-card { border-radius: 12px; }
.page-header {
    display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 20px;
}
.page-title { font-size: 20px; font-weight: 600; color: var(--color-text); margin-bottom: 4px; }
.page-desc { font-size: 13px; color: var(--color-text-secondary); }
.result-area { min-height: 200px; }
.loading-wrap {
    display: flex; align-items: center; justify-content: center; gap: 12px;
    padding: 60px 0; color: var(--color-text-secondary); font-size: 14px;
}
.pagination-wrap { display: flex; justify-content: center; margin-top: 20px; padding: 16px 0; }
</style>
