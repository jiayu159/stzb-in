<script setup>
import { ref, onMounted, computed, h } from 'vue'
import {
    NCard, NButton, NSpace, NTag, NEmpty, NSelect, NDataTable,
    NCheckbox, NCheckboxGroup, useMessage
} from 'naive-ui'
import { GetSiegeSummary } from '../../wailsjs/go/main/App'
import * as XLSX from 'xlsx'
import { RefreshCw, Download } from 'lucide-vue-next'

const nmessage = useMessage()

// 攻城战报汇总：按地点索引自动分类（中文汉字名称 / 其他-数字坐标），行=成员，列=地点索引
const siegeRows = ref([])
const siegeCities = ref([])
const siegeLoading = ref(false)

// 地点索引分类筛选：all=全部 / city=中文汉字名 / other=其他(数字坐标等)
const siegeIndexFilter = ref('all')
const indexFilterOptions = [
    { label: '全部地点', value: 'all' },
    { label: '中文地名', value: 'city' },
    { label: '其他(数字坐标)', value: 'other' },
]

// 地点多选筛选（不选=全部显示）；分组筛选（不选=全部显示）
const siegeCityFilter = ref(null)
const siegeGroupFilter = ref(null)

const siegeGroupOptions = computed(() => {
    const groups = new Set()
    siegeRows.value.forEach(r => { if (r.group) groups.add(r.group) })
    return [...groups].map(g => ({ label: g, value: g }))
})
const siegeCityOptions = computed(() => siegeCities.value.map(c => ({
    label: c.name + (c.category === 'other' ? ' (其他)' : ''),
    value: c.name
})))

// 可见地点索引列（分类筛选 + 地点多选共同决定）
const visibleCities = computed(() => {
    let list = siegeCities.value
    if (siegeIndexFilter.value !== 'all') {
        list = list.filter(c => c.category === siegeIndexFilter.value)
    }
    if (siegeCityFilter.value && siegeCityFilter.value.length > 0) {
        list = list.filter(c => siegeCityFilter.value.includes(c.name))
    }
    return list
})

// 可见成员行（分组筛选）
const visibleRows = computed(() => {
    if (!siegeGroupFilter.value) return siegeRows.value
    return siegeRows.value.filter(r => r.group === siegeGroupFilter.value)
})

// 单元格渲染：出勤/灭敌/拆迁/战报
const renderCityCell = (row, city) => {
    const s = (row.cities && row.cities[city.name]) || null
    if (!s) return h('div', { class: 'siege-cell empty' }, '—')
    return h('div', { class: 'siege-cell' }, [
        h('div', { class: 'cell-present' }, s.present ? '出勤' : '—'),
        h('div', {}, '灭敌 ' + (s.kill || 0)),
        h('div', {}, '拆迁 ' + (s.demolish || 0)),
        h('div', {}, '战报 ' + (s.reports || 0)),
    ])
}

const siegeColumns = computed(() => {
    const cols = [
        { title: '角色ID', key: 'role_id', width: 90, fixed: 'left' },
        { title: '名字', key: 'name', minWidth: 90, fixed: 'left' },
        {
            title: '分组', key: 'group', width: 110, fixed: 'left',
            render: (row) => row.group || '-'
        },
    ]
    visibleCities.value.forEach(c => {
        cols.push({
            title: c.name + (c.category === 'other' ? ' (其他)' : ''),
            key: 'loc_' + c.name,
            width: 110,
            render: (row) => renderCityCell(row, c),
            className: c.category === 'other' ? 'col-other' : 'col-city',
        })
    })
    return cols
})

// 导出子类勾选（默认全部）
const siegeExportSubs = ref(['present', 'kill', 'demolish', 'reports'])
const siegeExportSubOptions = [
    { label: '出勤', value: 'present' },
    { label: '灭敌', value: 'kill' },
    { label: '拆迁', value: 'demolish' },
    { label: '战报数', value: 'reports' },
]

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
    }).catch(e => nmessage.error('获取攻城汇总失败: ' + e)).finally(() => {
        siegeLoading.value = false
    })
}

const siegeSubMeta = { present: '出勤', kill: '灭敌', demolish: '拆迁', reports: '战报数' }

// 导出：cities=要导出的地点索引列表，rows=要导出的成员行
const writeSiegeXlsx = (cities, rows, sheetName, filename) => {
    if (cities.length === 0) {
        nmessage.warning('没有可导出的地点索引，请先确认筛选')
        return false
    }
    if (siegeExportSubs.value.length === 0) {
        nmessage.warning('请至少勾选一个要导出的子类')
        return false
    }
    const subs = siegeExportSubs.value
    const header = ['角色ID', '名字', '分组']
    cities.forEach(c => {
        subs.forEach(s => header.push(`${c.name}-${siegeSubMeta[s]}`))
    })
    const body = rows.map(r => {
        const line = [r.role_id, r.name, r.group]
        cities.forEach(c => {
            const st = (r.cities && r.cities[c.name]) || {}
            subs.forEach(s => {
                if (s === 'present') line.push(st.present ? '是' : '-')
                else line.push(st[s] || 0)
            })
        })
        return line
    })
    const ws = XLSX.utils.aoa_to_sheet([header, ...body])
    ws['!cols'] = header.map(() => ({ wch: 14 }))
    const wb = XLSX.utils.book_new()
    XLSX.utils.book_append_sheet(wb, ws, sheetName)
    XLSX.writeFile(wb, filename)
    nmessage.success(`导出成功，共 ${body.length} 条`)
    return true
}

// 导出当前筛选（当前可见地点索引 + 当前成员行）
const exportSiegeFiltered = () => {
    if (visibleRows.value.length === 0 || visibleCities.value.length === 0) {
        nmessage.warning('当前筛选下没有数据可导出')
        return
    }
    writeSiegeXlsx(visibleCities.value, visibleRows.value, '攻城汇总-当前筛选', '攻城汇总_当前筛选.xlsx')
}

// 导出全部（所有地点索引 + 所有成员）
const exportSiegeAll = () => {
    if (siegeRows.value.length === 0) {
        nmessage.warning('暂无数据可导出')
        return
    }
    writeSiegeXlsx(siegeCities.value, siegeRows.value, '攻城汇总', '攻城汇总.xlsx')
}

onMounted(() => {
    loadSiegeSummary()
})
</script>

<template>
    <div class="page-task">
        <n-card class="page-card" embedded>
            <div class="page-header">
                <div class="page-header-info">
                    <h2 class="page-title">攻城汇总</h2>
                    <p class="page-desc">
                        统计当前同盟成员的攻城战报：每个成员在每座城的
                        <b>出勤</b>（是否攻击该城）、<b>灭敌数</b>（防守方兵力变化：战前−战后）、<b>拆迁值</b>（耐久下降累加）、<b>战报数</b>。
                        <br/>地点索引自动分类：<b>中文汉字名称</b>（内黄/营陵等）作为独立列，其余（<b>数字坐标</b>之类）统一归入「其他」列；索引列标有「(其他)」后缀。
                    </p>
                </div>
            </div>

            <div class="siege-toolbar">
                <div style="display:flex;align-items:center;gap:8px;flex-wrap:wrap;">
                    <n-select v-model:value="siegeIndexFilter" :options="indexFilterOptions" size="small"
                        style="width:140px;" />
                    <n-select v-model:value="siegeGroupFilter" :options="siegeGroupOptions" size="small"
                        placeholder="筛选分组" clearable style="width:150px;" />
                    <n-select v-model:value="siegeCityFilter" :options="siegeCityOptions" size="small"
                        multiple placeholder="筛选地点索引（不选=全部）" clearable style="width:240px;" />
                    <n-button size="small" @click="loadSiegeSummary" :loading="siegeLoading">
                        <template #icon><RefreshCw :size="14" /></template>
                        刷新
                    </n-button>
                    <n-button size="small" type="success" @click="exportSiegeFiltered" :disabled="visibleRows.length === 0">
                        <template #icon><Download :size="14" /></template>
                        导出当前筛选
                    </n-button>
                    <n-button size="small" type="success" @click="exportSiegeAll" :disabled="siegeRows.length === 0">
                        <template #icon><Download :size="14" /></template>
                        导出全部
                    </n-button>
                </div>
                <div class="siege-check-row">
                    <span class="siege-check-label">导出子类：</span>
                    <n-checkbox-group v-model:value="siegeExportSubs" size="small">
                        <n-checkbox v-for="opt in siegeExportSubOptions" :key="opt.value" :value="opt.value" :label="opt.label" />
                    </n-checkbox-group>
                </div>
                <div v-if="visibleCities.length === 0" class="siege-empty-tip">
                    当前筛选下没有地点索引，请调整「地点索引分类」或「地点筛选（不选=全部）」
                </div>
            </div>

            <n-data-table
                :columns="siegeColumns"
                :data="visibleRows"
                :bordered="true"
                :single-line="false"
                :loading="siegeLoading"
                :max-height="600"
                :scroll-x="900"
                :scroll-to-top="false"
            />
            <n-empty v-if="!siegeLoading && siegeRows.length === 0" description="暂无攻城战报数据（需先在游戏内抓取攻城战报）" style="padding: 40px 0;" />
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
    margin-bottom: 16px;
}

.page-header-info {
    .page-title {
        font-size: 20px;
        font-weight: 600;
        color: var(--color-text);
        margin-bottom: 4px;
    }

    .page-desc {
        font-size: 13px;
        color: var(--color-text-secondary);
        line-height: 1.7;
    }
}

.siege-toolbar {
    display: flex;
    flex-direction: column;
    gap: 10px;
    margin-bottom: 12px;
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

.siege-empty-tip {
    font-size: 12px;
    color: var(--color-text-secondary);
}

.siege-cell {
    line-height: 1.55;
    font-size: 12px;
    color: var(--color-text);

    .cell-present {
        font-weight: 600;
        color: var(--color-accent);
    }

    &.empty {
        color: var(--color-text-disabled);
    }
}

:deep(.col-other) {
    background: rgba(240, 173, 78, 0.06);
}
</style>