<script setup>
import { ref, onMounted } from 'vue'
import { NCard, NButton, NInput, NInputNumber, NEmpty, NSpin, NPagination, useMessage } from 'naive-ui'
import { GetPlayerTeam, GetUnionMemberTopTeams, GetDefeatedEnemyTeams } from '../../wailsjs/go/main/App'
import { Search, Swords, Image, Download } from 'lucide-vue-next'
import TeamCard from '../components/TeamCard.vue'
import * as XLSX from 'xlsx'
import { herocfg } from '../cfg'

const heroMap = JSON.parse(herocfg)

const resolveHeroId = (id) => {
    if (!id) return id
    const num = Number(id)
    return num >= 130000 ? num - 30000 : num
}

const heroName = (id) => {
    if (!id) return ''
    const hero = heroMap[String(resolveHeroId(id))]
    return hero ? hero.name : `未知(${id})`
}

const nmessage = useMessage()
const loading = ref(false)
const results = ref([])

const searchName = ref('')
const searchUnion = ref('')

const hasSearched = ref(false)
const useBigImage = ref(true)
const page = ref(1)
const pageSize = ref(50)
const total = ref(0)

const doSearch = (newPage) => {
    if (typeof newPage === 'number') page.value = newPage
    else page.value = 1
    loading.value = true
    results.value = []
    hasSearched.value = true
    GetPlayerTeam(searchName.value, searchUnion.value, page.value, pageSize.value).then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            results.value = resp.data.list || []
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

// 同盟成员常用队伍
const memberMinHp = ref(0)
const memberLoading = ref(false)
const memberResults = ref([])
const memberPage = ref(1)
const memberPageSize = ref(20)
const memberTotal = ref(0)
const memberLoaded = ref(false)

const loadMemberTeams = (newPage) => {
    if (typeof newPage === 'number') memberPage.value = newPage
    else memberPage.value = 1
    memberLoading.value = true
    memberLoaded.value = true
    GetUnionMemberTopTeams(memberMinHp.value, memberPage.value, memberPageSize.value).then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            memberResults.value = resp.data.list || []
            memberTotal.value = resp.data.total || 0
        } else {
            nmessage.error(resp.msg)
        }
    }).catch(e => {
        nmessage.error('查询失败: ' + e)
    }).finally(() => {
        memberLoading.value = false
    })
}

// 战败的非己方同盟人员队伍
const enemyMinHp = ref(0)
const enemyLoading = ref(false)
const enemyResults = ref([])
const enemyPage = ref(1)
const enemyPageSize = ref(20)
const enemyTotal = ref(0)
const enemyLoaded = ref(false)

const loadEnemyTeams = (newPage) => {
    if (typeof newPage === 'number') enemyPage.value = newPage
    else enemyPage.value = 1
    enemyLoading.value = true
    enemyLoaded.value = true
    GetDefeatedEnemyTeams(enemyMinHp.value, enemyPage.value, enemyPageSize.value).then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            enemyResults.value = resp.data.list || []
            enemyTotal.value = resp.data.total || 0
        } else {
            nmessage.error(resp.msg)
        }
    }).catch(e => {
        nmessage.error('查询失败: ' + e)
    }).finally(() => {
        enemyLoading.value = false
    })
}

const formatTime = (ts) => {
    if (!ts) return ''
    const d = new Date(ts * 1000)
    const pad = (n) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const roleLabel = (role) => role === 'attack' ? '进攻' : '防守'

const exportMemberTeams = () => {
    if (memberResults.value.length === 0) {
        nmessage.warning('没有数据可导出')
        return
    }
    const data = [['成员', '使用次数', '队伍(武将)', '队伍(战法)', '红度', '兵力', '最近出战时间', '角色']]
    memberResults.value.forEach(r => {
        data.push([
            r.player_name,
            r.team_count,
            [heroName(r.hero1_id), heroName(r.hero2_id), heroName(r.hero3_id)].join(' / '),
            r.all_skill_info || '',
            r.total_star,
            r.hp,
            formatTime(r.last_time),
            roleLabel(r.role),
        ])
    })
    const ws = XLSX.utils.aoa_to_sheet(data)
    const wb = XLSX.utils.book_new()
    XLSX.utils.book_append_sheet(wb, ws, '同盟成员常用队伍')
    XLSX.writeFile(wb, `同盟成员常用队伍_${new Date().toLocaleDateString().replace(/\//g, '-')}.xlsx`)
    nmessage.success(`导出成功，共 ${memberResults.value.length} 条`)
}

const exportEnemyTeams = () => {
    if (enemyResults.value.length === 0) {
        nmessage.warning('没有数据可导出')
        return
    }
    const data = [['玩家', '战败次数', '队伍(武将)', '队伍(战法)', '红度', '兵力', '最近出战时间', '角色']]
    enemyResults.value.forEach(r => {
        data.push([
            r.player_name,
            r.loss_count,
            [heroName(r.hero1_id), heroName(r.hero2_id), heroName(r.hero3_id)].join(' / '),
            r.all_skill_info || '',
            r.total_star,
            r.hp,
            formatTime(r.last_time),
            roleLabel(r.role),
        ])
    })
    const ws = XLSX.utils.aoa_to_sheet(data)
    const wb = XLSX.utils.book_new()
    XLSX.utils.book_append_sheet(wb, ws, '战败敌军队伍')
    XLSX.writeFile(wb, `战败敌军队伍_${new Date().toLocaleDateString().replace(/\//g, '-')}.xlsx`)
    nmessage.success(`导出成功，共 ${enemyResults.value.length} 条`)
}

onMounted(() => {
    loadMemberTeams(1)
    loadEnemyTeams(1)
})

const groupedResults = () => {
    const map = {}
    results.value.forEach(r => {
        if (!map[r.player_name]) {
            map[r.player_name] = []
        }
        map[r.player_name].push(r)
    })
    return map
}
</script>

<template>
    <div class="page-team-query">
        <n-card class="page-card" embedded>
            <div class="page-header">
                <div class="page-header-info">
                    <h2 class="page-title">队伍查询</h2>
                    <p class="page-desc">通过战报数据查询玩家队伍配置</p>
                </div>
            </div>

            <div class="search-bar">
                <n-input v-model:value="searchName" placeholder="玩家名称" clearable @keyup.enter="doSearch" />
                <n-input v-model:value="searchUnion" placeholder="同盟名称" clearable @keyup.enter="doSearch" />
                <n-button type="primary" @click="doSearch()" :loading="loading">
                    <template #icon><Search :size="16" /></template>
                    查询
                </n-button>
                <n-button quaternary :type="useBigImage ? 'primary' : 'default'" @click="useBigImage = !useBigImage" :title="useBigImage ? '切换小图' : '切换大图'">
                    <template #icon><Image :size="16" /></template>
                    {{ useBigImage ? '大图' : '小图' }}
                </n-button>
            </div>

            <div class="pagination-wrap" v-if="total > pageSize">
                <n-pagination
                    v-model:page="page"
                    :page-size="pageSize"
                    :item-count="total"
                    :on-update:page="(p) => doSearch(p)"
                />
            </div>

            <div class="result-area" v-if="loading">
                <div class="loading-wrap">
                    <n-spin size="medium" />
                    <span>查询中...</span>
                </div>
            </div>

            <div class="result-area" v-else-if="hasSearched && results.length === 0">
                <n-empty description="未找到队伍数据" style="padding: 60px 0;" />
            </div>

            <div class="result-area" v-else-if="results.length > 0">
                <div class="result-summary">
                    共找到 <strong>{{ Object.keys(groupedResults()).length }}</strong> 名玩家，
                    <strong>{{ results.length }}</strong> 支队伍（共 {{ total }} 条）
                </div>

                <div class="player-section" v-for="(teams, playerName) in groupedResults()" :key="playerName">
                    <div class="player-name">
                        <Swords :size="16" />
                        {{ playerName }}
                    </div>

                    <TeamCard v-for="team in teams" :key="team.battle_id + team.role + team.hero1_id" :team="team" :use-big-image="useBigImage" />
                </div>

            </div>

            <div class="sub-section">
                <div class="sub-header">
                    <h3 class="sub-title">同盟成员常用队伍</h3>
                    <p class="sub-desc">每名成员只统计出现次数最多的一个队伍（含武将和战法），按总人数分页</p>
                    <div class="sub-tools">
                        <n-input-number v-model:value="memberMinHp" :min="0" :max="99999" :step="1000" placeholder="兵力过滤" :style="{ width: '140px' }" @keyup.enter="loadMemberTeams(1)" />
                        <n-button type="primary" size="small" @click="loadMemberTeams(1)" :loading="memberLoading">
                            <template #icon><Search :size="14" /></template>
                            查询
                        </n-button>
                        <n-button size="small" @click="exportMemberTeams" :disabled="memberResults.length === 0">
                            <template #icon><Download :size="14" /></template>
                            导出
                        </n-button>
                    </div>
                </div>

                <div class="pagination-wrap" v-if="memberTotal > memberPageSize">
                    <n-pagination
                        v-model:page="memberPage"
                        :page-size="memberPageSize"
                        :item-count="memberTotal"
                        :on-update:page="(p) => loadMemberTeams(p)"
                    />
                </div>

                <div class="sub-scroll">
                    <div class="result-area" v-if="memberLoading">
                        <div class="loading-wrap">
                            <n-spin size="medium" />
                            <span>查询中...</span>
                        </div>
                    </div>

                    <div class="result-area" v-else-if="memberLoaded && memberResults.length === 0">
                        <n-empty description="暂无同盟成员队伍数据" style="padding: 40px 0;" />
                    </div>

                    <div class="result-area" v-else-if="memberResults.length > 0">
                        <div class="result-summary">
                            共 <strong>{{ memberTotal }}</strong> 名成员有常用队伍（兵力过滤 {{ memberMinHp || '不限' }}）
                        </div>
                        <TeamCard v-for="team in memberResults" :key="'m' + team.player_name + team.hero1_id" :team="team" :use-big-image="false" :compact="true" :extra="`使用 ${team.team_count} 次`" />
                    </div>
                </div>
            </div>

            <div class="sub-section">
                <div class="sub-header">
                    <h3 class="sub-title">战败的非己方同盟人员队伍</h3>
                    <p class="sub-desc">统计己方同盟战报中战败的非己方同盟人员队伍（含武将和战法），按战败次数递减排序</p>
                    <div class="sub-tools">
                        <n-input-number v-model:value="enemyMinHp" :min="0" :max="99999" :step="1000" placeholder="兵力过滤" :style="{ width: '140px' }" @keyup.enter="loadEnemyTeams(1)" />
                        <n-button type="primary" size="small" @click="loadEnemyTeams(1)" :loading="enemyLoading">
                            <template #icon><Search :size="14" /></template>
                            查询
                        </n-button>
                        <n-button size="small" @click="exportEnemyTeams" :disabled="enemyResults.length === 0">
                            <template #icon><Download :size="14" /></template>
                            导出
                        </n-button>
                    </div>
                </div>

                <div class="pagination-wrap" v-if="enemyTotal > enemyPageSize">
                    <n-pagination
                        v-model:page="enemyPage"
                        :page-size="enemyPageSize"
                        :item-count="enemyTotal"
                        :on-update:page="(p) => loadEnemyTeams(p)"
                    />
                </div>

                <div class="sub-scroll">
                    <div class="result-area" v-if="enemyLoading">
                        <div class="loading-wrap">
                            <n-spin size="medium" />
                            <span>查询中...</span>
                        </div>
                    </div>

                    <div class="result-area" v-else-if="enemyLoaded && enemyResults.length === 0">
                        <n-empty description="暂无战败敌军队伍数据" style="padding: 40px 0;" />
                    </div>

                    <div class="result-area" v-else-if="enemyResults.length > 0">
                        <div class="result-summary">
                            共 <strong>{{ enemyTotal }}</strong> 支战败敌军队伍（兵力过滤 {{ enemyMinHp || '不限' }}）
                        </div>
                        <TeamCard v-for="team in enemyResults" :key="'e' + team.player_name + team.hero1_id" :team="team" :use-big-image="false" :compact="true" :extra="`战败 ${team.loss_count} 次`" />
                    </div>
                </div>
            </div>
        </n-card>
    </div>
</template>

<style scoped lang="scss">
.page-team-query {
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

.search-bar {
    display: flex;
    gap: 12px;
    margin-bottom: 20px;
    flex-wrap: wrap;
}

.search-bar .n-input {
    flex: 1;
    min-width: 160px;
}

.result-area {
    min-height: 200px;
}

.loading-wrap {
    display: flex;
    align-items: center;
    justify-content: center;
    gap: 12px;
    padding: 60px 0;
    color: var(--color-text-secondary);
    font-size: 14px;
}

.result-summary {
    font-size: 13px;
    color: var(--color-text-secondary);
    margin-bottom: 16px;
}

.pagination-wrap {
    display: flex;
    justify-content: center;
    margin-top: 20px;
    padding: 16px 0;
}

.player-section {
    margin-bottom: 24px;
}

.player-name {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 16px;
    font-weight: 700;
    color: var(--color-text);
    margin-bottom: 12px;
    padding-bottom: 8px;
    border-bottom: 2px solid var(--color-border);
}

.sub-section {
    margin-top: 32px;
    padding-top: 24px;
    border-top: 2px solid var(--color-border);
}

.sub-header {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 16px;
    flex-wrap: wrap;
}

.sub-title {
    font-size: 17px;
    font-weight: 700;
    color: var(--color-text);
    margin: 0;
}

.sub-desc {
    font-size: 13px;
    color: var(--color-text-secondary);
    margin: 0;
    flex: 1;
    min-width: 200px;
}

.sub-tools {
    display: flex;
    align-items: center;
    gap: 8px;
}

.sub-scroll {
    max-height: 260px;
    overflow-y: auto;
    padding-right: 6px;

    &::-webkit-scrollbar {
        width: 8px;
    }

    &::-webkit-scrollbar-thumb {
        background: var(--color-border);
        border-radius: 4px;
    }

    &::-webkit-scrollbar-thumb:hover {
        background: var(--color-primary);
    }
}
</style>
