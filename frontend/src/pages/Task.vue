<script setup>
import { ref, onMounted, computed, h } from 'vue'
import {
    NCard, NButton, NSpace, NTag, NEmpty, NSelect, NDataTable,
    NCheckbox, NCheckboxGroup, useMessage
} from 'naive-ui'
import { GetSiegeSummary } from '../../wailsjs/go/main/App'
import * as XLSX from 'xlsx'
import { RefreshCw, Download, Upload } from 'lucide-vue-next'

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
            dataSource.value = 'auto'
            importedStats.value = null
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

// ---------- 手动导入表格并解析（自动识别不理想时人工纠正/替代数据源） ----------
const dataSource = ref('auto')          // 'auto' = 数据库自动汇总；'import' = 手动导入数据
const importedStats = ref(null)         // { members, locations } 仅用于界面提示
const importing = ref(false)
const siegeFileInput = ref(null)        // 隐藏 file input
const HAN_RE = /[\u4e00-\u9fff]/

const pickSiegeFile = () => siegeFileInput.value && siegeFileInput.value.click()

const normalizeHeader = (s) => String(s ?? '').trim().toLowerCase()
    .replace(/[\s\u3000]+/g, '')
    .replace(/[（(]其他[)）]/g, '')

// 表头角色识别规则（顺序即优先级）；contains 用子串匹配，exact 用全等匹配（已归一化）
const HEADER_RULES = [
    { role: 'roleId', contains: ['角色id', '角色编号', '角色', 'roleid', 'role_id', 'role'], exact: ['id', 'uid'] },
    { role: 'name', contains: ['名字', '姓名', '昵称', '名称'], exact: ['name', '进攻方', '攻击方', '攻方', '交战方', '进攻', '攻打方', 'attacker', 'attack'] },
    { role: 'group', contains: ['分组', '联盟', '同盟', '组别'], exact: ['group'] },
    { role: 'reports', contains: ['战报', '次数', '数量'], exact: ['reports', 'count', 'times'] },
    { role: 'kill', contains: ['灭敌', '杀敌', '击杀', '消灭'], exact: ['kill'] },
    { role: 'demolish', contains: ['拆迁', '耐久'], exact: ['demolish', 'durability'] },
    { role: 'present', contains: ['出勤', '是否'], exact: ['present'] },
    { role: 'location', contains: ['地点', '城池', '位置', '坐标', '索引', '地名', '城市', '城址', '城名'], exact: ['城', 'location', 'city', 'cityname', 'loc', 'place'] },
]
const SUB_ALIAS = { '出勤': 'present', '灭敌': 'kill', '杀敌': 'kill', '拆迁': 'demolish', '耐久': 'demolish', '战报': 'reports', '战报数': 'reports', '次数': 'reports' }

const detectHeaderRole = (h) => {
    for (const rule of HEADER_RULES) {
        if (rule.exact.includes(h) || rule.contains.some(k => h.includes(k))) return rule.role
    }
    return null
}
const cellText = (v) => String(v ?? '').trim()
const cellInt = (v) => { const n = parseInt(String(v ?? '').replace(/[,，％%\s]/g, ''), 10); return isNaN(n) ? 0 : n }
const cellNoish = (v) => /^(x|×|—|－|-|否|无|0|false|null|undefined)$/i.test(String(v ?? '').trim())
const sortCitiesCn = (cities) => {
    const arr = [...cities]
    arr.sort((a, b) => {
        if (a.name === '其他') return 1
        if (b.name === '其他') return -1
        if (a.category !== b.category) return a.category === 'other' ? 1 : -1
        return a.name.localeCompare(b.name, 'zh-CN')
    })
    return arr
}

const parseSiegeImport = (sheetRows) => {
    const MATRIX_RE = /^(.*?)[-－–—](出勤|灭敌|杀敌|拆迁|耐久|战报数?|次数)$/
    const isMatrixHeader = (h) => { const m = h.match(MATRIX_RE); return !!(m && SUB_ALIAS[m[2]]) }
    // 定位表头行：跳过标题/空行，取第一个含关键列（成员/地点/矩阵列）的非空行
    let hi = -1
    for (let i = 0; i < sheetRows.length && hi < 0; i++) {
        const r = sheetRows[i]
        if (!Array.isArray(r) || !r.some(c => cellText(c) !== '')) continue
        const critical = (r || []).some(c => {
            const h = normalizeHeader(cellText(c))
            if (!h) return false
            if (isMatrixHeader(h)) return true
            const role = detectHeaderRole(h)
            return role === 'roleId' || role === 'name' || role === 'location'
        })
        if (critical) hi = i
    }
    if (hi < 0) throw new Error('未找到表头行；表格需包含「成员（角色ID/名字/进攻方）+ 地点」列，或「地点-出勤/灭敌/拆迁/战报」矩阵表头')
    const header = (sheetRows[hi] || []).map(cellText)
    const roleByCol = {}
    const matrixCols = []
    for (let c = 0; c < header.length; c++) {
        const h = normalizeHeader(header[c])
        if (!h) continue
        const m = h.match(MATRIX_RE)
        if (m && SUB_ALIAS[m[2]]) {
            matrixCols.push({ col: c, index: (m[1] || '').trim() || '其他', sub: SUB_ALIAS[m[2]] })
        } else {
            roleByCol[c] = detectHeaderRole(h)
        }
    }
    const colOf = (role) => Number(Object.keys(roleByCol).find(c => roleByCol[c] === role))
    const idCol = colOf('roleId'), nameCol = colOf('name'), groupCol = colOf('group')
    const locCol = colOf('location'), killCol = colOf('kill'), demolishCol = colOf('demolish'), reportsCol = colOf('reports')
    const hasMemberCol = !isNaN(idCol) || !isNaN(nameCol)
    if (!hasMemberCol) throw new Error('未识别到成员列（表头需包含「角色ID」或「名字/昵称」）')
    const dataRows = sheetRows.slice(hi + 1)

    if (matrixCols.length > 0) {
        // 矩阵模式：列 = 地点-子类（与导出的 攻城汇总.xlsx 一致），行 = 成员
        const cities = []
        for (const mc of matrixCols) if (!cities.find(c => c.name === mc.index)) cities.push({ name: mc.index, category: HAN_RE.test(mc.index) ? 'city' : 'other' })
        const rowsMap = new Map()
        for (const r of dataRows) {
            if (!Array.isArray(r)) continue
            const roleId = !isNaN(idCol) ? cellText(r[idCol]) : ''
            const name = !isNaN(nameCol) ? cellText(r[nameCol]) : (roleId || '')
            if (!roleId && !name) continue
            const key = roleId ? 'id:' + roleId : 'name:' + name
            let row = rowsMap.get(key)
            if (!row) { row = { role_id: roleId, name, group: !isNaN(groupCol) ? cellText(r[groupCol]) : '', cities: {} }; rowsMap.set(key, row) }
            let hasAny = false
            for (const mc of matrixCols) {
                const v = cellText(r[mc.col])
                if (!v) continue
                hasAny = true
                const st = row.cities[mc.index] || (row.cities[mc.index] = { present: false, kill: 0, demolish: 0, reports: 0 })
                if (mc.sub === 'present') st.present = !cellNoish(v)
                else if (mc.sub === 'kill') st.kill += cellInt(v)
                else if (mc.sub === 'demolish') st.demolish += cellInt(v)
                else st.reports += cellInt(v)
            }
            if (!hasAny) rowsMap.delete(key)
        }
        // 只有出勤列是「否」、但其他子类有数值时仍算到场
        for (const row of rowsMap.values()) {
            Object.values(row.cities).forEach(st => { if (!st.present && (st.kill || st.demolish || st.reports)) st.present = true })
        }
        return { cities: sortCitiesCn(cities), rows: [...rowsMap.values()] }
    }

    if (!isNaN(locCol)) {
        // 明细模式：一行 = 一条攻城记录，按「成员 + 地点」聚合
        const cities = []
        const cityOf = (raw) => {
            const t = cellText(raw)
            const n = t && HAN_RE.test(t) ? t : '其他'
            if (!cities.find(c => c.name === n)) cities.push({ name: n, category: HAN_RE.test(n) ? 'city' : 'other' })
            return n
        }
        const rowEntries = []
        for (const r of dataRows) {
            if (!Array.isArray(r) || !cellText(r[locCol])) continue
            const roleId = !isNaN(idCol) ? cellText(r[idCol]) : ''
            const name = !isNaN(nameCol) ? cellText(r[nameCol]) : (roleId || '')
            if (!roleId && !name) continue
            const loc = cityOf(r[locCol])
            rowEntries.push({
                key: (roleId ? 'id:' + roleId : 'name:' + name) + '|' + loc,
                role_id: roleId, name, group: !isNaN(groupCol) ? cellText(r[groupCol]) : '',
                loc,
                kill: !isNaN(killCol) ? cellInt(r[killCol]) : 0,
                demolish: !isNaN(demolishCol) ? cellInt(r[demolishCol]) : 0,
                reports: !isNaN(reportsCol) ? Math.max(cellInt(r[reportsCol]), 1) : 1,
            })
        }
        const memberMap = new Map()
        for (const it of rowEntries) {
            const key = it.role_id ? 'id:' + it.role_id : 'name:' + it.name
            let row = memberMap.get(key)
            if (!row) { row = { role_id: it.role_id, name: it.name, group: it.group, cities: {} }; memberMap.set(key, row) }
            const st = row.cities[it.loc] || (row.cities[it.loc] = { present: true, kill: 0, demolish: 0, reports: 0 })
            st.present = true
            st.kill += it.kill
            st.demolish += it.demolish
            st.reports += it.reports
        }
        return { cities: sortCitiesCn(cities), rows: [...memberMap.values()] }
    }

    throw new Error('未识别到「地点」列；明细表需含「地点/城池/位置」列，矩阵表需形如「内黄-灭敌」的表头')
}

const readSiegeFile = async (file) => {
    const lower = (file.name || '').toLowerCase()
    if (lower.endsWith('.csv')) {
        const buf = await file.arrayBuffer()
        let text = new TextDecoder('utf-8').decode(buf)
        if (text.includes('\uFFFD')) {
            try { text = new TextDecoder('gbk').decode(buf) } catch (e) { /* 保持原解码结果 */ }
        }
        const wb = XLSX.read(text, { type: 'string' })
        return XLSX.utils.sheet_to_json(wb.Sheets[wb.SheetNames[0]], { header: 1, defval: '' })
    }
    const wb = XLSX.read(await file.arrayBuffer(), { type: 'array' })
    return XLSX.utils.sheet_to_json(wb.Sheets[wb.SheetNames[0]], { header: 1, defval: '' })
}

const onSiegeFileChange = async (e) => {
    const file = e.target.files && e.target.files[0]
    e.target.value = ''
    if (!file) return
    importing.value = true
    try {
        const sheetRows = await readSiegeFile(file)
        const { cities, rows } = parseSiegeImport(sheetRows)
        if (rows.length === 0) throw new Error('解析后没有成员数据，请检查表格内容')
        siegeCities.value = cities
        siegeRows.value = rows
        dataSource.value = 'import'
        importedStats.value = { members: rows.length, locations: cities.length }
        nmessage.success(`导入成功：${rows.length} 名成员、${cities.length} 个地点索引`)
    } catch (err) {
        nmessage.error('导入解析失败：' + (err && err.message ? err.message : err))
    } finally {
        importing.value = false
    }
}

const onRefreshSiege = () => {
    if (dataSource.value === 'import' && !window.confirm('当前为手动导入数据，刷新将丢弃导入内容并还原为数据库自动汇总，确定继续？')) return
    loadSiegeSummary()
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
                        <br/>自动识别不理想时，可点「<b>导入表格</b>」手动导入 Excel/CSV 解析：支持「成员+地点+灭敌/拆迁/战报」明细表、导出的「地点-出勤/灭敌/拆迁/战报」矩阵表，以及游戏「同盟战报」导出（进攻方/地点/结果，按成员+地点统计到场与战报数）。
                    </p>
                    <div v-if="dataSource === 'import'" class="import-banner">
                        <span>
                            <b>当前显示：手动导入表格</b>
                            （{{ importedStats ? importedStats.members : siegeRows.length }} 名成员 / {{ siegeCities.length }} 个地点索引）—— 刷新或还原后将丢弃
                        </span>
                        <n-button size="tiny" type="primary" @click="loadSiegeSummary">还原自动汇总</n-button>
                    </div>
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
                    <n-button size="small" @click="onRefreshSiege" :loading="siegeLoading">
                        <template #icon><RefreshCw :size="14" /></template>
                        刷新
                    </n-button>
                    <n-button size="small" type="warning" ghost @click="pickSiegeFile" :loading="importing">
                        <template #icon><Upload :size="14" /></template>
                        导入表格
                    </n-button>
                    <input ref="siegeFileInput" type="file" accept=".xlsx,.xls,.csv" style="display:none" @change="onSiegeFileChange" />
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

.import-banner {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
    padding: 8px 12px;
    border-radius: 8px;
    background: rgba(240, 173, 78, 0.10);
    border: 1px solid rgba(240, 173, 78, 0.35);
    font-size: 13px;
    color: var(--color-text);
    margin-bottom: 10px;
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