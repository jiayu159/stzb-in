<script setup>
import { ref, onMounted } from 'vue'
import { NCard, NButton, NStatistic, NGrid, NGi, NTag, NSpace, NSpin } from 'naive-ui'
import { GetTaskList, GetTeamUser, GetVersion, GetSyncStatus, ManualSync } from '../../wailsjs/go/main/App'
import { Activity, Trophy, Shield, Crosshair, List, Bot, CloudUpload, RefreshCw } from 'lucide-vue-next'

const taskCount = ref(0)
const memberCount = ref(0)
const version = ref('')
const syncEnabled = ref(false)
const syncLastRun = ref(0)
const syncLastErr = ref('')
const syncing = ref(false)
const syncMsg = ref('')

function loadSyncStatus() {
    GetSyncStatus().then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200 && resp.data) {
            syncEnabled.value = resp.data.enabled
            syncLastRun.value = resp.data.last_run || 0
            syncLastErr.value = resp.data.last_err || ''
        }
    }).catch(() => {})
}

function pushToCloud() {
    syncing.value = true
    syncMsg.value = ''
    ManualSync().then(v => {
        let resp = JSON.parse(v)
        if (resp.code == 200) {
            syncMsg.value = '推送完成，数据已同步到云端'
            if (resp.data && resp.data.last_run) {
                syncLastRun.value = resp.data.last_run
            }
        } else {
            syncMsg.value = resp.msg || '推送失败'
        }
    }).catch(() => {
        syncMsg.value = '调用失败，请查看运行日志'
    }).finally(() => {
        syncing.value = false
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
                    <n-tag v-if="syncEnabled" :bordered="false" type="success" size="small">已启用</n-tag>
                    <n-tag v-else :bordered="false" type="warning" size="small">未启用</n-tag>
                    <span v-if="syncLastRun" class="sync-meta">上次同步 {{ new Date(syncLastRun * 1000).toLocaleString() }}</span>
                    <span v-if="syncLastErr" class="sync-err">上次失败: {{ syncLastErr }}</span>
                </div>
                <n-button type="primary" :loading="syncing" @click="pushToCloud">
                    <template #icon><CloudUpload :size="16" /></template>
                    手动推送数据到云端
                </n-button>
            </div>
            <div v-if="syncMsg" class="sync-msg">{{ syncMsg }}</div>
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
