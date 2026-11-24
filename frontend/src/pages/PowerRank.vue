<script setup>
import { ref, computed, h, onMounted } from 'vue'
import { NCard, NButton, NSpace, NInput, NEmpty, NDataTable, useMessage, useDialog, NTag } from 'naive-ui'
import { GetPowerRank } from '../../wailsjs/go/main/App'
import * as XLSX from 'xlsx'
import { Search, RefreshCw, Download, Trophy } from 'lucide-vue-next'

const dialog = useDialog()
const nmessage = useMessage()
const ranks = ref([])
const ranksNum = ref(0)
const searchText = ref('')
const loading = ref(false)

const filteredRanks = computed(() => {
    if (!searchText.value) return ranks.value
    const keyword = searchText.value.toLowerCase()
    return ranks.value.filter(r =>
        String(r.name || '').toLowerCase().includes(keyword)
    )
})

const columns = [
    {
        title: '排名', key: 'rank', width: 80,
        render: (row) => h('div', { class: 'rank-cell' }, [
            row.rank <= 3
                ? h(NTag, { size: 'small', bordered: false, type: row.rank === 1 ? 'error' : row.rank === 2 ? 'warning' : 'info' }, { default: () => `第${row.rank}名` })
                : `#${row.rank}`
        ])
    },
    { title: '玩家名称', key: 'name', minWidth: 160 },
    { title: '势力值', key: 'power', width: 110, sorter: (a, b) => a.power - b.power },
    { title: '分城', key: 'branch_city_count', width: 80 },
    { title: '戌城', key: 'shu_cheng_count', width: 80 },
    { title: '领地数量', key: 'land_count', width: 100, sorter: (a, b) => a.land_count - b.land_count },
]

const syncrank = () => {
    dialog.info({
        title: '信息',
        content: '请前往游戏中，打开排行榜（势力榜）即可自动同步，每次最多300条',
        positiveText: '确认',
        transformOrigin: 'center',
    })
}

function getRankList() {
    loading.value = true
    ranks.value = []
    ranksNum.value = 0
    GetPowerRank().then(v => {
        let data = JSON.parse(v)
        ranks.value = data.data
        ranksNum.value = data.data.length
    }).catch(() => {}).finally(() => {
        loading.value = false
    })
}

const exportExcel = () => {
    if (ranks.value.length === 0) {
        nmessage.warning('暂无数据可导出')
        return
    }
    let data = []
    data.push(['排名', '玩家名称', '势力值', '分城', '戌城', '领地数量'])
    ranks.value.forEach(v => {
        data.push([v.rank, v.name, v.power, v.branch_city_count, v.shu_cheng_count, v.land_count])
    })
    const ws = XLSX.utils.aoa_to_sheet(data)
    const wb = XLSX.utils.book_new()
    XLSX.utils.book_append_sheet(wb, ws, '势力榜')
    XLSX.writeFile(wb, `势力榜_${new Date().toLocaleDateString().replace(/\//g, '-')}.xlsx`)
    nmessage.success(`导出成功，共 ${ranks.value.length} 条`)
}

onMounted(() => {
    getRankList()
})
</script>

<template>
    <div class="page-powerrank">
        <n-card class="page-card" embedded>
            <div class="page-header">
                <div class="page-header-info">
                    <h2 class="page-title">势力榜 <n-tag size="small" type="warning" :bordered="false"><template #icon><Trophy :size="13" /></template>排名</n-tag></h2>
                    <p class="page-desc">共 {{ ranksNum }} 名玩家 · 数据来自游戏排行榜抓取（每次300条）</p>
                </div>
                <n-space>
                    <n-button @click="getRankList" :loading="loading">
                        <template #icon><RefreshCw :size="16" /></template>
                        刷新
                    </n-button>
                    <n-button @click="syncrank">
                        <template #icon><Trophy :size="16" /></template>
                        同步排行
                    </n-button>
                    <n-button type="primary" @click="exportExcel">
                        <template #icon><Download :size="16" /></template>
                        导出表格
                    </n-button>
                </n-space>
            </div>

            <n-input
                v-model:value="searchText"
                placeholder="搜索玩家名称..."
                clearable
                class="search-input"
            >
                <template #prefix>
                    <Search :size="16" />
                </template>
            </n-input>

            <n-data-table
                v-if="filteredRanks.length > 0"
                :columns="columns"
                :data="filteredRanks"
                :bordered="true"
                :single-line="false"
                :loading="loading"
                :max-height="560"
                :row-key="(row) => row.rank"
                :pagination="{ pageSize: 20 }"
            />
            <n-empty v-else description="暂无势力榜数据，请先同步" style="padding: 60px 0;" />
        </n-card>
    </div>
</template>

<style scoped lang="scss">
.page-powerrank {
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

.search-input {
    margin-bottom: 20px;
}

.rank-cell {
    display: flex;
    align-items: center;
}
</style>