<script setup>
import { ref, onMounted } from 'vue'
import { NCard, NGrid, NGi, NStatistic, NSpin, NButton, NNumberAnimation, useMessage } from 'naive-ui'
import { GetDashboardStats, GetTeamGroup, GetTaskList } from '../../wailsjs/go/main/App'
import { RefreshCw, Users, Swords, ClipboardList, FileText, Activity, TrendingUp } from 'lucide-vue-next'

const nmessage = useMessage()
const loading = ref(true)
const stats = ref({
    member_count: 0, total_wu: 0, avg_wu: 0, task_count: 0,
    total_battles: 0, total_reports: 0, battle_report_24h: 0, member_report_24h: 0
})
const groupCount = ref(0)

const loadData = () => {
    loading.value = true
    GetDashboardStats().then(v => {
        let r = JSON.parse(v)
        if (r.code == 200) stats.value = r.data
        else nmessage.error(r.msg)
    }).catch(e => nmessage.error(e)).finally(() => { loading.value = false })

    GetTeamGroup().then(v => {
        let r = JSON.parse(v)
        if (r.code == 200) groupCount.value = (r.data || []).length
    }).catch(() => {})
}

onMounted(loadData)
</script>

<template>
    <div class="page-dashboard">
        <n-card class="page-card" embedded>
            <div class="page-header">
                <div class="page-header-info">
                    <h2 class="page-title">赛季数据看板</h2>
                    <p class="page-desc">赛季全局数据概览</p>
                </div>
                <n-button @click="loadData" :loading="loading">
                    <template #icon><RefreshCw :size="16" /></template>
                    刷新
                </n-button>
            </div>

            <n-alert type="info" :show-icon="true" closable style="border-radius: 8px; margin-bottom: 20px; font-size: 13px;">
                <template #header>使用说明</template>
                数据来源于已抓取的同盟成员、战报和攻城任务数据。<br>
                <strong>同盟成员/武勋</strong> — 需要在"同盟成员"页面点"同步成员"后在游戏内打开成员列表<br>
                <strong>战报数据</strong> — 需要在控制面板开启"获取详细战报"并在游戏内查看战报详情<br>
                <strong>24h统计</strong> — 基于战报时间，有近24小时战报记录才会计数
            </n-alert>

            <n-spin :show="loading">
                <div class="dashboard-grid">
                    <n-grid :cols="4" :x-gap="16" :y-gap="16">
                        <n-gi>
                            <n-card embedded size="small" class="stat-card">
                                <div class="stat-icon"><Users :size="20" /></div>
                                <n-statistic label="同盟成员">
                                    <n-number-animation :from="0" :to="stats.member_count" />
                                </n-statistic>
                            </n-card>
                        </n-gi>
                        <n-gi>
                            <n-card embedded size="small" class="stat-card">
                                <div class="stat-icon"><Swords :size="20" /></div>
                                <n-statistic label="总武勋">
                                    <n-number-animation :from="0" :to="stats.total_wu" />
                                </n-statistic>
                            </n-card>
                        </n-gi>
                        <n-gi>
                            <n-card embedded size="small" class="stat-card">
                                <div class="stat-icon"><TrendingUp :size="20" /></div>
                                <n-statistic label="平均武勋">
                                    <n-number-animation :from="0" :to="stats.avg_wu" />
                                </n-statistic>
                            </n-card>
                        </n-gi>
                        <n-gi>
                            <n-card embedded size="small" class="stat-card">
                                <div class="stat-icon"><ClipboardList :size="20" /></div>
                                <n-statistic label="分组数" :value="groupCount" />
                            </n-card>
                        </n-gi>
                        <n-gi>
                            <n-card embedded size="small" class="stat-card">
                                <div class="stat-icon"><FileText :size="20" /></div>
                                <n-statistic label="战报总数(详细)">
                                    <n-number-animation :from="0" :to="stats.total_battles" />
                                </n-statistic>
                            </n-card>
                        </n-gi>
                        <n-gi>
                            <n-card embedded size="small" class="stat-card">
                                <div class="stat-icon"><FileText :size="20" /></div>
                                <n-statistic label="战报总数(攻城)">
                                    <n-number-animation :from="0" :to="stats.total_reports" />
                                </n-statistic>
                            </n-card>
                        </n-gi>
                        <n-gi>
                            <n-card embedded size="small" class="stat-card">
                                <div class="stat-icon"><Activity :size="20" /></div>
                                <n-statistic label="24h战斗">
                                    <n-number-animation :from="0" :to="stats.battle_report_24h" />
                                </n-statistic>
                            </n-card>
                        </n-gi>
                        <n-gi>
                            <n-card embedded size="small" class="stat-card">
                                <div class="stat-icon"><Users :size="20" /></div>
                                <n-statistic label="24h参战人数">
                                    <n-number-animation :from="0" :to="stats.member_report_24h" />
                                </n-statistic>
                            </n-card>
                        </n-gi>
                    </n-grid>
                </div>

                <div class="insights">
                    <n-card embedded size="small" title="数据洞察">
                        <div class="insight-item">
                            同盟共 <strong>{{ stats.member_count }}</strong> 人，总武勋 <strong>{{ stats.total_wu.toLocaleString() }}</strong>，人均 <strong>{{ (stats.avg_wu).toLocaleString() }}</strong>
                        </div>
                        <div class="insight-item">
                            过去24小时发生 <strong>{{ stats.battle_report_24h }}</strong> 场战斗，共 <strong>{{ stats.member_report_24h }}</strong> 人参战
                        </div>
                        <div class="insight-item" v-if="stats.total_battles > 0 && stats.member_count > 0">
                            人均战斗 <strong>{{ (stats.total_battles / stats.member_count).toFixed(1) }}</strong> 场，
                            人均武勋 <strong>{{ (stats.total_wu / stats.member_count).toFixed(0) }}</strong>
                        </div>
                    </n-card>
                </div>
            </n-spin>
        </n-card>
    </div>
</template>

<style scoped lang="scss">
.page-dashboard { display: flex; flex-direction: column; }
.page-card { border-radius: 12px; }
.page-header {
    display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 20px;
}
.page-title { font-size: 20px; font-weight: 600; color: var(--color-text); margin-bottom: 4px; }
.page-desc { font-size: 13px; color: var(--color-text-secondary); }

.stat-card {
    position: relative;
    .stat-icon {
        position: absolute; top: 12px; right: 12px; color: var(--color-primary); opacity: 0.5;
    }
}

.dashboard-grid { margin-bottom: 20px; }

.insights {
    .insight-item {
        padding: 8px 0; font-size: 14px; color: var(--color-text);
        border-bottom: 1px solid var(--color-border);
        &:last-child { border-bottom: none; }
        strong { color: var(--color-primary); }
    }
}
</style>
