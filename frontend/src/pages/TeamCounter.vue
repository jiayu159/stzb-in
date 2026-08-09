<script setup>
import { ref } from 'vue'
import { NCard, NButton, NInput, NTag, NEmpty, NSpin, NDataTable, NInputNumber, useMessage } from 'naive-ui'
import { GetTeamCounter } from '../../wailsjs/go/main/App'
import { Search, Swords, Shield, Crosshair } from 'lucide-vue-next'
import { herocfg, skillcfg } from '../cfg'

const heroMap = JSON.parse(herocfg)
const skillMap = JSON.parse(skillcfg)

// 名字→ID 反向映射
const nameToIdMap = {}
for (const [id, v] of Object.entries(heroMap)) {
    if (v.name) nameToIdMap[v.name] = id
}

const resolveHeroId = (val) => {
    const trimmed = val.trim()
    if (/^\d+$/.test(trimmed)) return parseInt(trimmed)
    const id = nameToIdMap[trimmed]
    if (id) return parseInt(id)
    // 部分匹配
    for (const [name, id] of Object.entries(nameToIdMap)) {
        if (name.includes(trimmed)) return parseInt(id)
    }
    return NaN
}

const nmessage = useMessage()
const loading = ref(false)
const hero1Id = ref('')
const hero2Id = ref('')
const hero3Id = ref('')
const minBattles = ref(5)
const hasSearched = ref(false)

const asAttacker = ref([])
const asDefender = ref([])

const getHeroName = (id) => {
    if (!id) return ''
    const normalized = Number(id) >= 130000 ? Number(id) - 30000 : Number(id)
    const hero = heroMap[String(normalized)]
    return hero ? hero.name : `未知(${id})`
}

const getHeroIconId = (id) => {
    if (!id) return id
    const hero = heroMap[String(Number(id) >= 130000 ? Number(id) - 30000 : Number(id))]
    return hero ? hero.iconId : id
}

const parseSkillInfo = (str) => {
    if (!str) return ''
    const groups = String(str).split(';').filter(s => s.trim() !== '')
    const lines = groups.map(g => {
        const parts = g.split(',')
        const skills = [parts[1], parts[3], parts[5]].filter(s => s && s !== '0')
        return skills.map(id => {
            const skill = skillMap[String(id)]
            return skill ? skill.name : `未知(${id})`
        }).join('/')
    }).filter(Boolean)
    return lines.join('\n')
}

const doSearch = () => {
    const h1 = resolveHeroId(hero1Id.value)
    const h2 = resolveHeroId(hero2Id.value)
    const h3 = resolveHeroId(hero3Id.value)
    if (isNaN(h1) || isNaN(h2) || isNaN(h3)) {
        nmessage.warning('请输入三个武将ID或名称')
        return
    }
    loading.value = true
    hasSearched.value = true
    GetTeamCounter(h1, h2, h3, minBattles.value).then(v => {
        let r = JSON.parse(v)
        if (r.code == 200) {
            asAttacker.value = r.data.as_attacker || []
            asDefender.value = r.data.as_defender || []
        } else nmessage.error(r.msg)
    }).catch(e => nmessage.error(e)).finally(() => { loading.value = false })
}

const rateColor = (r) => {
    if (r >= 60) return '#22c55e'
    if (r >= 50) return '#3b82f6'
    if (r >= 40) return '#f59e0b'
    return '#ef4444'
}

const teamColumns = [
    {
        title: '队伍', key: 'heroes', minWidth: 200,
        render: (row) => [1,2,3].map(i => getHeroName(row[`hero${i}_id`])).join(' / ')
    },
    {
        title: '战法', key: 'skills', width: 200,
        render: (row) => h('div', { style: 'white-space:pre-line;font-size:12px;' }, parseSkillInfo(row.all_skill_info))
    },
    {
        title: '总场次', key: 'total_battles', width: 80, align: 'center',
        sorter: (a, b) => a.total_battles - b.total_battles,
    },
    {
        title: '胜', key: 'win_count', width: 60, align: 'center',
        sorter: (a, b) => a.win_count - b.win_count,
    },
    {
        title: '负', key: 'loss_count', width: 60, align: 'center',
        sorter: (a, b) => a.loss_count - b.loss_count,
    },
    {
        title: '胜率', key: 'win_rate', width: 80, align: 'center',
        sorter: (a, b) => a.win_rate - b.win_rate, defaultSortOrder: 'descend',
        render: (row) => h('span', { style: `color:${rateColor(row.win_rate)};font-weight:700;` }, (row.win_rate || 0) + '%')
    },
]

import { h } from 'vue'
</script>

<template>
    <div class="page-teamcounter">
        <n-card class="page-card" embedded>
            <div class="page-header">
                <div class="page-header-info">
                    <h2 class="page-title">队伍克制分析</h2>
                    <p class="page-desc">输入我方三个武将（ID或名称），查看遇到各对手的胜率</p>
                </div>
            </div>

            <n-alert type="info" :show-icon="true" closable style="border-radius: 8px; margin-bottom: 20px; font-size: 13px;">
                <template #header>使用说明</template>
                输入我方三个武将的 ID，分析该队伍在实战中的表现：<br>
                <strong>作为进攻方</strong> — 列出遇到过的防守队伍及胜率（对方队伍胜率越高说明越克制你）<br>
                <strong>作为防守方</strong> — 列出进攻过你的队伍及胜率（你的胜率越高说明越克制他们）<br>
                武将 ID 可在 <strong>武将查询</strong> 或 <strong>队伍查询</strong> 页面找到，需要先抓取详细战报。
            </n-alert>

            <div class="search-bar">
                <div class="hero-input-group">
                    <span class="hero-input-label">武将1</span>
                    <n-input v-model:value="hero1Id" placeholder="ID或名称 如 10027/吕布" clearable @keyup.enter="doSearch" />
                    <span class="hero-input-name" v-if="hero1Id && getHeroName(resolveHeroId(hero1Id))">{{ getHeroName(resolveHeroId(hero1Id)) }}</span>
                </div>
                <div class="hero-input-group">
                    <span class="hero-input-label">武将2</span>
                    <n-input v-model:value="hero2Id" placeholder="ID或名称 如 10027/吕布" clearable @keyup.enter="doSearch" />
                    <span class="hero-input-name" v-if="hero2Id && getHeroName(resolveHeroId(hero2Id))">{{ getHeroName(resolveHeroId(hero2Id)) }}</span>
                </div>
                <div class="hero-input-group">
                    <span class="hero-input-label">武将3</span>
                    <n-input v-model:value="hero3Id" placeholder="ID或名称 如 10027/吕布" clearable @keyup.enter="doSearch" />
                    <span class="hero-input-name" v-if="hero3Id && getHeroName(resolveHeroId(hero3Id))">{{ getHeroName(resolveHeroId(hero3Id)) }}</span>
                </div>
                <div class="hero-input-group">
                    <span class="hero-input-label">最低场次</span>
                    <n-input-number v-model:value="minBattles" :min="1" :max="100" style="width:100px;" />
                </div>
                <n-button type="primary" @click="doSearch" :loading="loading">
                    <template #icon><Search :size="16" /></template>
                    分析
                </n-button>
            </div>

            <n-spin :show="loading">
                <template v-if="hasSearched && !loading">
                    <n-card embedded size="small" class="result-section" title="作为进攻方时的战绩">
                        <div v-if="asAttacker.length > 0">
                            <n-data-table :columns="teamColumns" :data="asAttacker" :bordered="false" size="small" :scroll-x="800" />
                        </div>
                        <n-empty v-else description="暂无数据" style="padding:30px 0;" />
                    </n-card>

                    <n-card embedded size="small" class="result-section" title="作为防守方时的战绩">
                        <div v-if="asDefender.length > 0">
                            <n-data-table :columns="teamColumns" :data="asDefender" :bordered="false" size="small" :scroll-x="800" />
                        </div>
                        <n-empty v-else description="暂无数据" style="padding:30px 0;" />
                    </n-card>
                </template>

                <n-empty v-else-if="!hasSearched" description="输入武将ID后点击分析" style="padding:60px 0;" />
            </n-spin>
        </n-card>
    </div>
</template>

<style scoped lang="scss">
.page-teamcounter { display: flex; flex-direction: column; }
.page-card { border-radius: 12px; }
.page-header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 20px; }
.page-title { font-size: 20px; font-weight: 600; color: var(--color-text); margin-bottom: 4px; }
.page-desc { font-size: 13px; color: var(--color-text-secondary); }

.search-bar {
    display: flex; gap: 12px; margin-bottom: 24px; flex-wrap: wrap; align-items: flex-end;
}

.hero-input-group {
    display: flex; flex-direction: column; gap: 4px;
    .hero-input-label { font-size: 12px; color: var(--color-text-secondary); }
    .hero-input-name { font-size: 12px; color: var(--color-primary); font-weight: 600; }
}

.result-section {
    margin-bottom: 20px;
    border-radius: 10px;
}
</style>
