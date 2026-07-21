# stzbHelper
率土之滨助手  
后续版本不再开源 仅在交流群发布编译后的文件 并增加授权机制(不收费,授权文件会包含在发布的文件中,仅限制软件使用时间)   
[交流群](https://t.me/stzbHelper)
## 使用说明
本程序依赖于 [Npcap](https://npcap.com/#download) 抓取网络数据包来获取战报与同盟成员信息, 所以你在使用前需要先安装Npcap(https://npcap.com/dist/npcap-1.82.exe)  
## 支持情况
- PC客户端
- 模拟器移动端客户端
- 移动端客户端（使用移动端设备时需运行本程序的主机带有无线网卡，并打开移动热点给移动端设备连接）
## 构建
1. 构建前需确保已安装 golang 1.24及以上版本、nodejs  
2. `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
3. `wails build`