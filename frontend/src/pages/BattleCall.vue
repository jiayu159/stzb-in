<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import { NCard, NButton, NTag, NEmpty, NScrollbar, NBadge, NSpace, useMessage } from 'naive-ui'
import { EnableBattleCall, DisableBattleCall } from '../../wailsjs/go/main/App'
import { EventsOn, EventsOff } from '../../wailsjs/runtime/runtime'
import { MessageSquare, Bell, BellOff } from 'lucide-vue-next'

const nmessage = useMessage()
const messages = ref([])
const enemyMessages = ref([])
const enabled = ref(false)
const monitorEnabled = ref(false)

onMounted(() => {
    EventsOn('battleCallData', (data) => {
        const msgs = Array.isArray(data) ? data : (data[0] || [])
        const raw = typeof data[1] === 'string' ? data[1] : ''
        msgs.forEach(m => {
            const entry = {
                player_name: m.player_name || '',
                content: m.content || '',
                alliance_name: m.alliance_name || '',
                timestamp: m.timestamp || Date.now() / 1000,
                raw: raw,
                time: new Date((m.timestamp || Date.now() / 1000) * 1000).toLocaleString()
            }
            messages.value.unshift(entry)
            if (messages.value.length > 500) messages.value.pop()

            if (monitorEnabled.value && m.content && m.player_name) {
                enemyMessages.value.unshift({
                    ...entry,
                    type: m.content.includes('集结') ? 'warning' :
                          m.content.includes('进攻') ? 'error' : 'info'
                })
                if (enemyMessages.value.length > 200) enemyMessages.value.pop()
            }
        })
    })
})

onUnmounted(() => {
    EventsOff('battleCallData')
})

const toggleEnable = () => {
    if (enabled.value) {
        DisableBattleCall().then(v => {
            let r = JSON.parse(v)
            if (r.code == 200) { enabled.value = false; nmessage.success('已关闭') }
            else nmessage.error(r.msg)
        })
    } else {
        EnableBattleCall().then(v => {
            let r = JSON.parse(v)
            if (r.code == 200) { enabled.value = true; nmessage.success('已开启') }
            else nmessage.error(r.msg)
        })
    }
}

const clearMsgs = () => { messages.value = [] }
const clearEnemy = () => { enemyMessages.value = [] }

const formatTime = (ts) => {
    if (!ts) return ''
    return new Date((typeof ts === 'number' ? ts : parseInt(ts)) * 1000).toLocaleString()
}
</script>

<template>
    <div class="page-battlecall">
        <n-card class="page-card" embedded>
            <div class="page-header">
                <div class="page-header-info">
                    <h2 class="page-title">战役叫阵</h2>
                    <p class="page-desc">实时同步同盟战役叫阵消息</p>
                </div>
                <n-space>
                    <n-button :type="enabled ? 'error' : 'primary'" @click="toggleEnable">
                        <template #icon><component :is="enabled ? BellOff : Bell" :size="16" /></template>
                        {{ enabled ? '关闭' : '开启同步' }}
                    </n-button>
                    <n-button @click="clearMsgs">清空消息</n-button>
                </n-space>
            </div>

            <n-alert type="info" :show-icon="true" closable style="border-radius: 8px; margin-bottom: 20px; font-size: 13px;">
                <template #header>使用说明</template>
                <strong>战役叫阵同步</strong> — 点击下方"开启同步"，然后在游戏内打开战役叫阵界面，程序会自动捕获消息。<br>
                <strong>敌军动向监控</strong> — 开启后自动标记含"集结""进攻"等关键词的消息，方便快速发现敌方行动。<br>
                需要游戏内有人发送战役叫阵消息才会收到数据。
            </n-alert>

            <div class="status-bar" v-if="!enabled">
                <n-tag type="warning" :bordered="false">未开启</n-tag>
                <span class="status-text">点击"开启同步"开始接收战役叫阵消息，需要在游戏内查看战役叫阵</span>
            </div>
            <div class="status-bar" v-else>
                <n-tag type="success" :bordered="false">同步中</n-tag>
                <span class="status-text">共收到 {{ messages.length }} 条消息</span>
            </div>

            <n-card title="敌军动向监控" embedded size="small" class="monitor-card">
                <div class="monitor-bar">
                    <n-button size="small" :type="monitorEnabled ? 'primary' : 'default'" @click="monitorEnabled = !monitorEnabled">
                        {{ monitorEnabled ? '监控中' : '开启监控' }}
                    </n-button>
                    <n-button size="small" @click="clearEnemy">清空</n-button>
                    <span class="monitor-count" v-if="monitorEnabled">已捕获 {{ enemyMessages.length }} 条动向</span>
                </div>
                <div class="msg-list" v-if="enemyMessages.length > 0">
                    <div class="msg-item" v-for="(msg, i) in enemyMessages" :key="'e'+i">
                        <n-tag :type="msg.type" size="tiny" :bordered="false">
                            {{ msg.type === 'warning' ? '集结' : msg.type === 'error' ? '进攻' : '消息' }}
                        </n-tag>
                        <span class="msg-player">{{ msg.player_name }}</span>
                        <span class="msg-content">{{ msg.content }}</span>
                        <span class="msg-time">{{ msg.time }}</span>
                    </div>
                </div>
                <n-empty v-else description="暂无敌军动向" style="padding: 30px 0;" />
            </n-card>

            <div class="section-title">全部消息 ({{ messages.length }})</div>
            <div class="msg-list" v-if="messages.length > 0">
                <div class="msg-item" v-for="(msg, i) in messages" :key="i">
                    <n-tag type="info" size="tiny" :bordered="false">{{ msg.alliance_name || '同盟' }}</n-tag>
                    <span class="msg-player">{{ msg.player_name }}</span>
                    <span class="msg-content">{{ msg.content }}</span>
                    <span class="msg-time">{{ msg.time }}</span>
                </div>
            </div>
            <n-empty v-else description="暂无消息，请先开启同步并在游戏内查看战役叫阵" style="padding: 60px 0;" />
        </n-card>
    </div>
</template>

<style scoped lang="scss">
.page-battlecall {
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

.page-title { font-size: 20px; font-weight: 600; color: var(--color-text); margin-bottom: 4px; }
.page-desc { font-size: 13px; color: var(--color-text-secondary); }

.status-bar {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 20px;
    padding: 12px 16px;
    background: var(--color-bg);
    border-radius: 8px;
}

.status-text { font-size: 13px; color: var(--color-text-secondary); }

.monitor-card {
    margin-bottom: 20px;
    border-radius: 10px;
}

.monitor-bar {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 12px;
}

.monitor-count { font-size: 13px; color: var(--color-text-secondary); }

.section-title {
    font-size: 16px;
    font-weight: 600;
    color: var(--color-text);
    margin-bottom: 12px;
    padding-bottom: 8px;
    border-bottom: 2px solid var(--color-border);
}

.msg-list {
    display: flex;
    flex-direction: column;
    gap: 6px;
}

.msg-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 12px;
    background: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: 8px;
    font-size: 13px;
    flex-wrap: wrap;
}

.msg-player { font-weight: 600; color: var(--color-text); }
.msg-content { color: var(--color-text); flex: 1; min-width: 100px; }
.msg-time { font-size: 12px; color: var(--color-text-secondary); white-space: nowrap; }
</style>
