import { defineStore } from 'pinia'
import { AiChat, ClearAiChat, SetAiThinking } from '../../wailsjs/go/main/App'

export const useAiChatStore = defineStore('aiChat', {
    state: () => ({
        messages: [
            {
                role: 'assistant',
                content: '我是长史助手，已实时同步同盟数据库数据（成员、战报、攻城任务）。你可以问我如"哪个分组武勋最高""最近战报打的最多的地方是哪里""某某任务的完成情况"等问题。'
            }
        ],
        sending: false,
        thinkingMode: true,
        reasoningError: false
    }),
    actions: {
        send(text) {
            if (!text || this.sending) return
            this.messages.push({ role: 'user', content: text })
            this.sending = true
            this.reasoningError = false
            AiChat(text).then(v => {
                let resp = JSON.parse(v)
                if (resp.code == 200) {
                    this.messages.push({
                        role: 'assistant',
                        content: resp.data.reply || '(无回复)',
                        thinking: resp.data.thinking || '',
                        showThinking: false
                    })
                } else {
                    this.messages.push({ role: 'assistant', content: '出错了：' + resp.msg })
                }
            }).catch(e => {
                this.messages.push({ role: 'assistant', content: '调用失败：' + e })
            }).finally(() => {
                this.sending = false
            })
        },
        clear() {
            ClearAiChat().then(v => {
                let resp = JSON.parse(v)
                if (resp.code == 200) {
                    this.messages = [
                        {
                            role: 'assistant',
                            content: '我是长史助手，已实时同步同盟数据库数据（成员、战报、攻城任务）。你可以问我如"哪个分组武勋最高""最近战报打的最多的地方是哪里""某某任务的完成情况"等问题。'
                        }
                    ]
                }
            })
        },
        toggleThinking(v) {
            this.thinkingMode = v
            SetAiThinking(v).then(res => {
                let data = JSON.parse(res)
                if (data.code != 200) {
                    this.thinkingMode = !v
                    this.reasoningError = data.msg
                }
            }).catch(e => {
                this.thinkingMode = !v
                this.reasoningError = '切换失败: ' + e
            })
        }
    }
})