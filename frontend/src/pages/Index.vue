<script setup>
import { ref, onMounted } from 'vue'
import { NCard, NButton, NStatistic, NSpace, NGrid, NGi, NAlert, NTag, useMessage } from 'naive-ui'
import { EnableGetBattleReport, DisableGetBattleReport, GetTaskList, GetTeamUser, GetVersion } from '../../wailsjs/go/main/App'
import { Activity, Trophy, Shield, Crosshair, MessageSquare, List } from 'lucide-vue-next'

const nmessage = useMessage()

const taskCount = ref(0)
const memberCount = ref(0)
const version = ref('')
const showNotice = ref(true)

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

onMounted(() => {
    GetVersion().then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            version.value = resp.data
        }
    }).catch(() => {})

    GetTaskList().then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            taskCount.value = resp.data.length
        }
    }).catch(() => {})

    GetTeamUser("").then(v => {
        let data = JSON.parse(v)
        if (data.data) {
            memberCount.value = data.data.length
        }
    }).catch(() => {})
})
</script>

<template>
    <div class="page-index">
        <div class="page-hero">
            <div class="page-hero-content">
                <h1 class="page-hero-title">率土之滨助手</h1>
                <p class="page-hero-desc">stzbHelper &middot; Version {{ version }}</p>
            </div>
        </div>

        <n-alert v-if="showNotice" type="info" closable @close="showNotice = false"
            style="border-radius: 12px;">
            <template #header>
                <strong>📢 版本更新公告</strong>
            </template>
            <div style="font-size: 13px; line-height: 1.8;">
                <p>以下功能已可用：</p>
                <div style="display: flex; flex-wrap: wrap; gap: 8px; margin: 8px 0;">
                    <n-tag :bordered="false" type="success" size="small">赛季看板</n-tag>
                    <n-tag :bordered="false" type="success" size="small">活跃度分析</n-tag>
                    <n-tag :bordered="false" type="success" size="small">战役叫阵</n-tag>
                    <n-tag :bordered="false" type="success" size="small">敌军动向监控</n-tag>
                    <n-tag :bordered="false" type="success" size="small">热门排行</n-tag>
                    <n-tag :bordered="false" type="success" size="small">队伍克制分析</n-tag>
                    <n-tag :bordered="false" type="success" size="small">考勤导出增强</n-tag>
                    <n-tag :bordered="false" type="warning" size="small">同盟战报自动翻阅</n-tag>
                    <n-tag :bordered="false" type="warning" size="small">攻城考勤时间定位</n-tag>
                </div>
                <p><strong>自动翻阅：</strong>在「同盟战报」页面设定截止时间，程序自动在游戏中翻页抓取战报，到达指定时间自动停止。</p>
                <p><strong>时间定位：</strong>在「攻城考勤」弹窗中设定截止时间，翻阅时自动跟踪进度，翻越截止时间点的战报自动跳过。</p>
            </div>
        </n-alert>

        <n-grid :cols="3" :x-gap="16" :y-gap="16" class="stat-grid">
            <n-gi>
                <n-card embedded size="small">
                    <n-statistic label="同盟成员" :value="memberCount" />
                </n-card>
            </n-gi>
            <n-gi>
                <n-card embedded size="small">
                    <n-statistic label="攻城任务" :value="taskCount" />
                </n-card>
            </n-gi>
            <n-gi>
                <n-card embedded size="small">
                    <n-statistic label="应用版本" :value="version" />
                </n-card>
            </n-gi>
        </n-grid>

        <n-card class="control-card" title="控制面板" embedded>
            <div class="control-section">
                <div class="control-item">
                    <div class="control-item-info">
                        <div class="control-item-title">获取详细战报</div>
                        <div class="control-item-desc">用于查询队伍功能拉取战报使用，开启时无法获取攻城战报</div>
                    </div>
                    <n-space>
                        <n-button type="primary" @click="onEnableGetBattleReport">开启</n-button>
                        <n-button @click="onDisableGetBattleReport">关闭</n-button>
                    </n-space>
                </div>
            </div>
        </n-card>

        <n-card class="quick-nav-card" title="新功能快捷入口" embedded>
            <div class="quick-nav">
                <n-button quaternary @click="$router.push('/dashboard')">
                    <template #icon><Trophy :size="16" /></template>
                    赛季看板
                </n-button>
                <n-button quaternary @click="$router.push('/activity')">
                    <template #icon><Activity :size="16" /></template>
                    活跃度分析
                </n-button>
                <n-button quaternary @click="$router.push('/battlecall')">
                    <template #icon><MessageSquare :size="16" /></template>
                    战役叫阵
                </n-button>
                <n-button quaternary @click="$router.push('/hotrank')">
                    <template #icon><Shield :size="16" /></template>
                    热门排行
                </n-button>
                <n-button quaternary @click="$router.push('/teamcounter')">
                    <template #icon><Crosshair :size="16" /></template>
                    队伍克制
                </n-button>
                <n-button quaternary @click="$router.push('/battlereports')">
                    <template #icon><List :size="16" /></template>
                    同盟战报(自动翻阅)
                </n-button>
            </div>
        </n-card>
    </div>
</template>

<style scoped lang="scss">
.page-index {
    display: flex;
    flex-direction: column;
    gap: 20px;
}

.page-hero {
    background: var(--color-hero-bg);
    border-radius: 12px;
    padding: 32px;
    color: var(--color-hero-text);

    &-title {
        font-size: 24px;
        font-weight: 700;
        margin-bottom: 8px;
    }

    &-desc {
        font-size: 14px;
        opacity: 0.85;
    }
}

.stat-grid {
    margin-top: 0;
}

.control-card {
    border-radius: 12px;
}

.control-section {
    display: flex;
    flex-direction: column;
    gap: 16px;
}

.control-item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 16px;
    background: var(--color-surface-hover);
    border-radius: 8px;

    &-info {
        flex: 1;
    }

    &-title {
        font-size: 15px;
        font-weight: 600;
        color: var(--color-text);
        margin-bottom: 4px;
    }

    &-desc {
        font-size: 13px;
        color: var(--color-text-secondary);
    }
}
</style>
