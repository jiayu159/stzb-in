<script setup>
import { ref, nextTick, onMounted } from 'vue'
import { NCard, NInput, NButton, NTag, NEmpty, NSwitch, useMessage } from 'naive-ui'
import { Bot, Send, Trash2, RefreshCw, Brain } from 'lucide-vue-next'
import { marked } from 'marked'
import { storeToRefs } from 'pinia'
import { useAiChatStore } from '../stores/aiChat'

marked.setOptions({ gfm: true, breaks: true })

const nmessage = useMessage()
const store = useAiChatStore()
const { messages, sending, thinkingMode } = storeToRefs(store)
const input = ref('')
const msgListRef = ref(null)

const scrollToBottom = async () => {
    await nextTick()
    if (msgListRef.value) {
        msgListRef.value.scrollTop = msgListRef.value.scrollHeight
    }
}

const formatReply = (md) => {
    const html = marked.parse(md || '')
    return html.replace(/<(!DOCTYPE|html|body|head)[^>]*>/gi, '')
}

const onThinkingChange = (v) => {
    store.toggleThinking(v)
}

const send = () => {
    const text = input.value.trim()
    if (!text) return
    input.value = ''
    store.send(text)
    scrollToBottom()
}

const clearChat = () => {
    store.clear()
    nmessage.success('对话已清空')
}

onMounted(() => {
    scrollToBottom()
})
</script>

<template>
    <div class="page-ai">
        <n-card :bordered="false" class="page-card">
            <div class="page-header">
                <div>
                    <h2 class="page-title">妲己小秘书</h2>
                    <p class="page-desc">
                        <n-tag size="small" type="success" :bordered="false">数据实时同步</n-tag>
                        <n-tag v-if="sending" size="small" type="warning" :bordered="false" style="margin-left:6px;">回复中...</n-tag>
                    </p>
                </div>
                <div style="display:flex;align-items:center;gap:12px;">
                    <div style="display:flex;align-items:center;gap:6px;font-size:13px;">
                        <Brain :size="15" style="vertical-align:-2px;" />
                        思考模式
                        <n-switch :value="thinkingMode" size="small" @update:value="onThinkingChange" />
                    </div>
                    <n-button quaternary size="small" @click="clearChat">
                        <template #icon><Trash2 :size="16" /></template>
                        清空对话
                    </n-button>
                </div>
            </div>

            <div ref="msgListRef" class="msg-list">
                <div v-if="messages.length === 0" style="padding:40px 0;">
                    <n-empty description="暂无对话" />
                </div>
                <div v-for="(m, idx) in messages" :key="idx" class="msg-row" :class="m.role">
                    <div class="msg-avatar">
                        <Bot v-if="m.role === 'assistant'" :size="16" />
                        <span v-else style="font-size:12px;">我</span>
                    </div>
                    <div class="msg-bubble">
                        <div v-if="m.role === 'assistant' && m.thinking" class="thinking-toggle" @click="m.showThinking = !m.showThinking">
                            <Brain :size="13" style="vertical-align:-2px;" />
                            思考过程 {{ m.showThinking ? '收起' : '展开' }}
                        </div>
                        <div v-if="m.role === 'assistant' && m.thinking && m.showThinking" class="thinking-box">{{ m.thinking }}</div>
                        <div v-if="m.role === 'assistant'" class="msg-content" v-html="formatReply(m.content)"></div>
                        <pre v-else class="msg-content">{{ m.content }}</pre>
                    </div>
                </div>
                <div v-if="sending" class="msg-row assistant">
                    <div class="msg-avatar"><Bot :size="16" /></div>
                    <div class="msg-bubble thinking">
                        <RefreshCw :size="14" class="spin" style="vertical-align:-2px;" />
                        妲己思考中...
                    </div>
                </div>
            </div>

            <div class="input-area">
                <n-input
                    v-model:value="input"
                    type="textarea"
                    :autosize="{ minRows: 3, maxRows: 8 }"
                    placeholder="问关于同盟数据的问题，Enter 发送，Shift+Enter 换行"
                    :disabled="sending"
                    @keydown="(e) => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() } }"
                />
                <n-button type="primary" :loading="sending" @click="send" style="margin-top:8px;">
                    <template #icon><Send :size="16" /></template>
                    发送
                </n-button>
            </div>
        </n-card>
    </div>
</template>

<style scoped>
.page-ai {
    max-width: 960px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    height: calc(100vh - 100px);
}

.page-card {
    flex: 1;
    display: flex;
    flex-direction: column;
    margin-bottom: 0;
}

.page-header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    margin-bottom: 16px;
}

.page-title {
    font-size: 20px;
    font-weight: 600;
    margin: 0;
}

.page-desc {
    margin: 6px 0 0 0;
    font-size: 13px;
}

.msg-list {
    flex: 1;
    height: 420px;
    overflow-y: auto;
    padding: 16px 8px 24px;
    border: 1px solid rgba(128, 128, 128, 0.15);
    border-radius: 10px;
    background: rgba(128, 128, 128, 0.04);
}

.msg-row {
    display: flex;
    gap: 10px;
    margin-bottom: 12px;
    padding: 0 12px;
}

.msg-row.user {
    flex-direction: row-reverse;
}

.msg-avatar {
    width: 28px;
    height: 28px;
    border-radius: 50%;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    background: rgba(128, 128, 128, 0.15);
    color: var(--color-text);
    margin-top: 4px;
}

.msg-bubble {
    max-width: 82%;
    padding: 10px 14px;
    border-radius: 12px;
    background: rgba(128, 128, 128, 0.1);
    border-top-left-radius: 4px;
}

.msg-row.user .msg-bubble {
    background: #3b82f6;
    color: #fff;
    border-radius: 12px;
    border-top-right-radius: 4px;
}

.msg-content {
    margin: 0;
    white-space: pre-wrap;
    word-break: break-word;
    font-family: inherit;
    font-size: 14px;
    line-height: 1.7;
}

.msg-content.md {
    white-space: normal;
    font-size: 16px;
    line-height: 1.8;
    color: var(--color-text);
    font-weight: 500;
}

.msg-content.md :deep(p) { margin: 4px 0; }
.msg-content.md :deep(ul),
.msg-content.md :deep(ol) { margin: 4px 0; padding-left: 20px; }
.msg-content.md :deep(strong) { font-weight: 600; }
.msg-content.md :deep(code) { background: rgba(128,128,128,.15); padding: 1px 5px; border-radius: 4px; font-size: 12px; }
.msg-content.md :deep(pre) { background: rgba(128,128,128,.15); padding: 8px 10px; border-radius: 6px; overflow-x: auto; }
.msg-content.md :deep(h1),
.msg-content.md :deep(h2),
.msg-content.md :deep(h3) { font-size: 15px; margin: 8px 0 4px; }
.msg-content.md :deep(table) { border-collapse: collapse; }
.msg-content.md :deep(td),
.msg-content.md :deep(th) { border: 1px solid rgba(128,128,128,.3); padding: 2px 8px; }

.thinking-toggle {
    font-size: 12px;
    opacity: .7;
    cursor: pointer;
    margin-bottom: 6px;
    user-select: none;
    display: flex;
    align-items: center;
    gap: 4px;
}

.thinking-toggle:hover { opacity: 1; }

.thinking-box {
    font-size: 11.5px;
    opacity: .55;
    color: var(--color-text);
    background: rgba(128,128,128,.12);
    border-radius: 8px;
    padding: 8px 12px;
    margin-bottom: 8px;
    max-height: 200px;
    overflow-y: auto;
    white-space: pre-wrap;
    word-break: break-word;
}

.msg-row.user .msg-content {
    color: #fff;
}

.thinking {
    color: var(--color-text);
    opacity: 0.8;
    font-size: 13px;
}

.spin {
    animation: spin 1s linear infinite;
}

@keyframes spin {
    to { transform: rotate(360deg); }
}

.input-area {
    margin-top: 12px;
}
</style>
