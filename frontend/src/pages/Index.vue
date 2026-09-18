<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { NCard, NButton, NStatistic, NGrid, NGi, NTag, NSpace, NSpin, NInputNumber } from 'naive-ui'
import { GetTaskList, GetTeamUser, GetVersion, GetSyncStatus, ManualPushRecent, CheckAndPushAll } from '../../wailsjs/go/main/App'
import { Activity, Trophy, Shield, Crosshair, List, Bot, RefreshCw, Send } from 'lucide-vue-next'

const taskCount = ref(0)
const memberCount = ref(0)
const version = ref('')
const syncEnabled = ref(false)
const syncAlliance = ref('')
const syncConfigOk = ref(false)
const syncWaitingDb = ref(false)
const syncLastRun = ref(0)
const syncLastErr = ref('')
const pushCount = ref(3000)
const pushingRecent = ref(false)
const pushRecentMsg = ref('')
const pushingAll = ref(false)
const checkAllMsg = ref('')
let statusTimer = null

function loadSyncStatus() {
    GetSyncStatus().then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200 && resp.data) {
            syncEnabled.value = resp.data.enabled
            syncConfigOk.value = !!resp.data.config_ok
            syncWaitingDb.value = !!resp.data.waiting_db
            syncAlliance.value = resp.data.alliance || ''
            syncLastRun.value = resp.data.last_run || 0
            syncLastErr.value = resp.data.last_err || ''
        }
    }).catch(() => {})
}

function pushRecentToCloud() {
    if (!pushCount.value || pushCount.value <= 0) return
    pushingRecent.value = true
    pushRecentMsg.value = ''
    ManualPushRecent(pushCount.value).then(v => {
        let resp = JSON.parse(v)
        pushRecentMsg.value = resp.msg || (resp.code == 200 ? '推送完成' : '推送失败')
        if (resp.data && resp.data.pushed != null) {
            pushRecentMsg.value = `成功推送 ${resp.data.pushed} 条` + (resp.data.failed ? `，失败 ${resp.data.failed} 条（详见日志）` : '，数据已同步到云端')
            if (resp.data.min_battle_id && resp.data.max_battle_id) {
                pushRecentMsg.value += `（battle_id ${resp.data.min_battle_id} ~ ${resp.data.max_battle_id}）`
            }
        }
    }).catch(() => {
        pushRecentMsg.value = '调用失败，请查看运行日志'
    }).finally(() => {
        pushingRecent.value = false
        loadSyncStatus()
    })
}

function checkAllToCloud() {
    pushingAll.value = true
    checkAllMsg.value = ''
    CheckAndPushAll().then(v => {
        let resp = JSON.parse(v)
        let b = resp.data && resp.data.battle_report
        if (resp.code == 200 && b != null) {
            checkAllMsg.value = `检查并同步完成：战报共 ${b.total} 条、成功推送 ${b.pushed} 条` +
                (b.failed ? `、失败 ${b.failed} 条（详见日志）` : '，云端已同步全部数据')
            if (resp.data.team_user && resp.data.team_user.pushed != null) {
                checkAllMsg.value += `；成员 ${resp.data.team_user.pushed} 名`
            }
        } else {
            checkAllMsg.value = resp.msg || '检查并同步失败，请查看运行日志'
        }
    }).catch(() => {
        checkAllMsg.value = '调用失败，请查看运行日志'
    }).finally(() => {
        pushingAll.value = false
        loadSyncStatus()
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

    loadSyncStatus()
    // 云同步状态随后台初始化/数据库打开动态变化(如等待数据库打开后自动启用)，
    // 轮询刷新，避免首次加载时拿到"未启用"后不再更新
    statusTimer = setInterval(loadSyncStatus, 3000)
})

onUnmounted(() => {
    if (statusTimer) clearInterval(statusTimer)
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

        <n-card class="sync-card" embedded>
            <div class="sync-bar">
                <div class="sync-info">
                    <span class="sync-title">云端数据同步</span>
                    <n-tag v-if="!syncConfigOk" :bordered="false" type="warning" size="small">未启用</n-tag>
                    <n-tag v-else-if="syncWaitingDb" :bordered="false" type="info" size="small">等待数据库打开（配置就绪）</n-tag>
                    <n-tag v-else-if="syncEnabled" :bordered="false" type="success" size="small">已启用</n-tag>
                    <n-tag v-else :bordered="false" type="warning" size="small">未启用</n-tag>
                    <span v-if="syncEnabled && syncAlliance" class="sync-meta">联盟: {{ syncAlliance }}</span>
                    <span v-if="syncLastRun" class="sync-meta">上次同步 {{ new Date(syncLastRun * 1000).toLocaleString() }}</span>
                    <span v-if="syncLastErr" class="sync-err">上次失败: {{ syncLastErr }}</span>
                </div>
            </div>
            <div class="sync-row">
                <span class="sync-row-label">手动推送最新战报</span>
                <n-input-number v-model:value="pushCount" :min="1" :step="100"
                    :style="{ width: '140px' }" />
                <span class="sync-row-tip">条（按 battle_id 倒序取本地最新，强制覆盖云端，用于补齐漏掉的数据）</span>
                <n-button type="primary" :loading="pushingRecent" @click="pushRecentToCloud">
                    <template #icon><Send :size="16" /></template>
                    推送最新 {{ pushCount || 0 }} 条
                </n-button>
            </div>
            <div v-if="pushRecentMsg" class="sync-msg">{{ pushRecentMsg }}</div>
            <div class="sync-row">
                <span class="sync-row-label">全量同步</span>
                <n-button type="warning" :loading="pushingAll" @click="checkAllToCloud">
                    <template #icon><RefreshCw :size="16" /></template>
                    检查并数据
                </n-button>
                <span class="sync-row-tip">检查并同步本地全部数据到云端（不受条数限制，云端已存在的战报自动跳过）</span>
            </div>
            <div v-if="checkAllMsg" class="sync-msg">{{ checkAllMsg }}</div>
        </n-card>

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
                <n-button quaternary @click="$router.push('/hotrank')">
                    <template #icon><Shield :size="16" /></template>
                    热门排行
                </n-button>
                <n-button quaternary @click="$router.push('/teamcounter')">
                    <template #icon><Crosshair :size="16" /></template>
                    队伍克制
                </n-button>
                <n-button quaternary type="primary" @click="$router.push('/daji')">
                    <template #icon><Bot :size="16" /></template>
                    妲己小秘书
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

.sync-card {
    border-radius: 12px;

    .sync-bar {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 16px;
        flex-wrap: wrap;
    }

    .sync-row {
        display: flex;
        align-items: center;
        gap: 10px;
        flex-wrap: wrap;
        margin-top: 14px;
        padding-top: 12px;
        border-top: 1px solid rgba(128, 128, 128, 0.15);
    }

    .sync-row-label {
        font-size: 13px;
        font-weight: 600;
    }

    .sync-row-tip {
        font-size: 12px;
        opacity: 0.6;
    }

    .sync-info {
        display: flex;
        align-items: center;
        gap: 8px;
        flex-wrap: wrap;
    }

    .sync-title {
        font-size: 15px;
        font-weight: 600;
        margin-right: 4px;
    }

    .sync-meta {
        font-size: 12px;
        opacity: 0.7;
    }

    .sync-err {
        font-size: 12px;
        color: #d03050;
    }

    .sync-msg {
        margin-top: 10px;
        font-size: 13px;
        color: #18a058;
    }
}
</style>
