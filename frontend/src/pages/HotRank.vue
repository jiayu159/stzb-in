<script setup>
import { ref, onMounted } from 'vue'
import { NCard, NButton, NSpin, NEmpty, NTag, NGi, NGrid, NStatistic, useMessage } from 'naive-ui'
import { GetHotRank } from '../../wailsjs/go/main/App'
import { RefreshCw, TrendingUp, Swords } from 'lucide-vue-next'
import { herocfg, skillcfg } from '../cfg'

const heroMap = JSON.parse(herocfg)
const skillMap = JSON.parse(skillcfg)

const nmessage = useMessage()
const loading = ref(false)
const teams = ref([])

const loadData = () => {
    loading.value = true
    GetHotRank().then(v => {
        let r = JSON.parse(v)
        if (r.code == 200) {
            teams.value = (r.data.teams || []).map(t => ({
                ...t,
                hero1_name: getHeroName(t.hero1_id),
                hero2_name: getHeroName(t.hero2_id),
                hero3_name: getHeroName(t.hero3_id),
            }))
        } else nmessage.error(r.msg)
    }).catch(e => nmessage.error(e)).finally(() => { loading.value = false })
}

onMounted(loadData)

const resolveHeroId = (id) => {
    if (!id) return id
    const num = Number(id)
    return num >= 130000 ? num - 30000 : num
}

const getHeroIconId = (id) => {
    if (!id) return id
    const hero = heroMap[String(resolveHeroId(id))]
    return hero ? hero.iconId : id
}

const getHeroName = (id) => {
    if (!id) return ''
    const hero = heroMap[String(resolveHeroId(id))]
    return hero ? hero.name : `未知(${id})`
}

const getHeroCountry = (id) => {
    if (!id) return ''
    const hero = heroMap[String(resolveHeroId(id))]
    return hero ? hero.country : ''
}

const getHeroType = (id) => {
    if (!id) return ''
    const hero = heroMap[String(resolveHeroId(id))]
    return hero ? hero.type : ''
}

const getHeroQuality = (id) => {
    if (!id) return 5
    const hero = heroMap[String(resolveHeroId(id))]
    return hero ? hero.quality : 5
}

const getSkillName = (id) => {
    if (!id) return ''
    const skill = skillMap[String(id)]
    return skill ? skill.name : `未知(${id})`
}

const getSkillQuality = (id) => {
    if (!id) return ''
    const skill = skillMap[String(id)]
    return skill ? skill.zfQuality : ''
}

const getSkillType = (id) => {
    if (!id) return ''
    const skill = skillMap[String(id)]
    return skill ? skill.type : ''
}

const parseSkillInfo = (str) => {
    if (!str) return []
    const groups = String(str).split(';').filter(s => s.trim() !== '')
    const parsed = groups.map(g => {
        const parts = g.split(',')
        return {
            index: parseInt(parts[0]),
            skills: [
                { id: parts[1], level: parseInt(parts[2]) },
                { id: parts[3], level: parseInt(parts[4]) },
                { id: parts[5], level: parseInt(parts[6]) },
            ]
        }
    })
    return parsed.filter(g => g.index >= 1 && g.index <= 3)
}

const rateColor = (r) => {
    if (r >= 60) return '#22c55e'
    if (r >= 50) return '#3b82f6'
    if (r >= 40) return '#f59e0b'
    return '#ef4444'
}
</script>

<template>
    <div class="page-hotrank">
        <n-card class="page-card" embedded>
            <div class="page-header">
                <div class="page-header-info">
                    <h2 class="page-title">热门队伍排行</h2>
                    <p class="page-desc">进攻方队伍组合出现频率及胜率统计</p>
                </div>
                <n-button @click="loadData" :loading="loading">
                    <template #icon><RefreshCw :size="16" /></template>
                    刷新
                </n-button>
            </div>

            <n-alert type="info" :show-icon="true" closable style="border-radius: 8px; margin-bottom: 20px; font-size: 13px;">
                <template #header>使用说明</template>
                统计基于详细战报中进攻方队伍组合（按武将ID去重），展示各队伍的出现频率与胜率。<br>
                数据需要先开启控制面板的 <strong>"获取详细战报"</strong>，然后在游戏内查看同盟战报详情才会入库。<br>
                用来分析当前赛季哪些队伍在战场上最常见，以及它们的实战表现。
            </n-alert>

            <n-grid :cols="3" :x-gap="16" style="margin-bottom:20px;">
                <n-gi>
                    <n-card embedded size="small">
                        <n-statistic label="统计队伍数" :value="teams.length" />
                    </n-card>
                </n-gi>
                <n-gi>
                    <n-card embedded size="small">
                        <n-statistic label="最高出场数" :value="teams.length > 0 ? teams[0].total_battles : 0" />
                    </n-card>
                </n-gi>
                <n-gi>
                    <n-card embedded size="small">
                        <n-statistic label="最高胜率" :value="teams.length > 0 ? Math.max(...teams.map(t => t.win_rate)) : 0" :precision="1" />
                        <template #suffix>%</template>
                    </n-card>
                </n-gi>
            </n-grid>

            <n-spin :show="loading">
                <div v-if="teams.length > 0" class="team-list">
                    <div class="team-card" v-for="(team, idx) in teams" :key="idx">
                        <div class="team-rank">{{ idx + 1 }}</div>
                        <div class="team-main">
                            <div class="hero-row">
                                <div class="hero-slot" v-for="i in 3" :key="i">
                                    <div class="hero-avatar">
                                        <img v-if="team[`hero${i}_id`]"
                                            :src="`https://cbg-stzb.res.netease.com/game_res/cards/cut/card_medium_${getHeroIconId(team[`hero${i}_id`])}.jpg`"
                                            @error="$event.target.style.display='none'" />
                                    </div>
                                    <div class="hero-info">
                                        <span class="hero-name">{{ getHeroName(team[`hero${i}_id`]) }}</span>
                                        <span class="hero-meta">
                                            <n-tag v-if="getHeroCountry(team[`hero${i}_id`])" size="tiny" :bordered="false">{{ getHeroCountry(team[`hero${i}_id`]) }}</n-tag>
                                            <n-tag v-if="getHeroType(team[`hero${i}_id`])" size="tiny" :bordered="false" type="info">{{ getHeroType(team[`hero${i}_id`]) }}</n-tag>
                                        </span>
                                    </div>
                                    <div class="hero-skills" v-if="team.all_skill_info">
                                        <div class="skill-tag" v-for="(skill, si) in (parseSkillInfo(team.all_skill_info)[i - 1]?.skills || [])" :key="si">
                                            <template v-if="skill && skill.id && skill.id !== '0'">
                                                <n-tag v-if="getSkillQuality(skill.id)" size="tiny" :bordered="false" :type="getSkillQuality(skill.id) === 'S' ? 'warning' : getSkillQuality(skill.id) === 'A' ? 'info' : 'default'">{{ getSkillQuality(skill.id) }}</n-tag>
                                                <span class="skill-name">{{ getSkillName(skill.id) }}</span>
                                            </template>
                                        </div>
                                    </div>
                                </div>
                            </div>
                            <div class="team-stats">
                                <div class="stat-item">
                                    <span class="stat-label">出场</span>
                                    <span class="stat-value">{{ team.total_battles }}</span>
                                </div>
                                <div class="stat-item stat-win">
                                    <span class="stat-label">胜</span>
                                    <span class="stat-value">{{ team.win_count }}</span>
                                </div>
                                <div class="stat-item">
                                    <span class="stat-label">胜率</span>
                                    <span class="stat-value" :style="{ color: rateColor(team.win_rate), fontWeight: 700 }">{{ team.win_rate }}%</span>
                                </div>
                            </div>
                        </div>
                    </div>
                </div>
                <n-empty v-else description="暂无数据，请先抓取战报" style="padding: 60px 0;" />
            </n-spin>
        </n-card>
    </div>
</template>

<style scoped lang="scss">
.page-hotrank { display: flex; flex-direction: column; }
.page-card { border-radius: 12px; }
.page-header {
    display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 20px;
}
.page-title { font-size: 20px; font-weight: 600; color: var(--color-text); margin-bottom: 4px; }
.page-desc { font-size: 13px; color: var(--color-text-secondary); }

.team-list {
    display: flex;
    flex-direction: column;
    gap: 12px;
}

.team-card {
    display: flex;
    gap: 16px;
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: 10px;
    padding: 16px;
    transition: box-shadow 0.2s;
    &:hover { box-shadow: 0 4px 12px rgba(0,0,0,0.06); }
}

.team-rank {
    font-size: 20px;
    font-weight: 800;
    color: var(--color-text-secondary);
    min-width: 36px;
    display: flex;
    align-items: center;
    justify-content: center;
}

.team-main { flex: 1; min-width: 0; }

.hero-row {
    display: grid;
    grid-template-columns: repeat(3, 1fr);
    gap: 12px;
    margin-bottom: 12px;
}

.hero-slot {
    background: var(--color-bg);
    border-radius: 8px;
    padding: 10px;
    display: flex;
    flex-direction: column;
    gap: 6px;
}

.hero-avatar {
    width: 60px;
    height: 60px;
    border-radius: 8px;
    overflow: hidden;
    flex-shrink: 0;
    background: var(--color-surface-hover);
    img { width: 100%; height: 100%; object-fit: cover; }
}

.hero-info {
    display: flex;
    flex-direction: column;
    gap: 2px;
}

.hero-name {
    font-size: 14px;
    font-weight: 700;
    color: var(--color-text);
}

.hero-meta {
    display: flex;
    gap: 4px;
    flex-wrap: wrap;
}

.hero-skills {
    display: flex;
    flex-direction: column;
    gap: 3px;
    margin-top: 4px;
}

.skill-tag {
    display: flex;
    align-items: center;
    gap: 4px;
    font-size: 11px;
}

.skill-name {
    color: var(--color-text);
    font-size: 12px;
    font-weight: 500;
}

.team-stats {
    display: flex;
    gap: 24px;
    padding: 10px 16px;
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: 8px;
}

.stat-item {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    .stat-label { font-size: 12px; color: var(--color-text-secondary); }
    .stat-value { font-size: 16px; font-weight: 600; color: var(--color-text); }
    &.stat-win .stat-value { color: #22c55e; }
}
</style>
