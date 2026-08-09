package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"log"
	"os"
	"strconv"
	"strings"
	"stzbHelper/global"
	"stzbHelper/model"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var databaseSelected bool = false

func runNpcap() {
	// 获取所有网络接口
	devices, err := pcap.FindAllDevs()
	if err != nil {
		log.Fatal("无法获取网络接口列表:", err)
	}

	// 如果没有找到任何接口，退出
	if len(devices) == 0 {
		log.Fatal("未找到可用的网络接口")
	}

	if global.IsDebug == true {
		// 打印所有可用的网络接口
		fmt.Println("可用的网络接口:")
		for i, device := range devices {
			fmt.Printf("%d: %s (%s)\n", i+1, device.Name, device.Description)
		}
	}

	// 使用 WaitGroup 等待所有 Goroutine 完成
	var wg sync.WaitGroup

	// 遍历所有接口并启动 Goroutine 监听
	log.Println("stzbHelper开始运行!")
	log.Println("version:", global.Version)
	//log.Println("提示：0.0.3版本开始启动软件后需要进入游戏点击自己的主公簿进行激活软件。此改动是为了之后实现多数据库与绑定游戏连接IP信息，避免出现连接到多个8001端口导致的数据错乱")
	time.Sleep(100 * time.Millisecond)
	// log.Println("等待打开主公簿激活软件...")
	// log.Println("未打开主公簿激活软件前软件可能会出现报错！")

	for _, device := range devices {
		wg.Add(1)
		go captureTCPPackets(device.Name, &wg)
	}

	// 等待所有 Goroutine 完成
	wg.Wait()
}

// captureTCPPackets 监听指定接口的 TCP 数据包
func captureTCPPackets(deviceName string, wg *sync.WaitGroup) {
	defer wg.Done()

	// 打开网络接口
	handle, err := pcap.OpenLive(deviceName, 65535, true, pcap.BlockForever)
	if err != nil {
		log.Printf("无法打开接口 %s: %v\n", deviceName, err)
		return
	}
	defer handle.Close()

	// 设置过滤器，捕获端口为 8001 的 TCP 数据包（双向：客户端->服务器请求 与 服务器->客户端响应）
	filter := "tcp port 8001"
	err = handle.SetBPFFilter(filter)
	if err != nil {
		log.Printf("无法在接口 %s 上设置过滤器: %v\n", deviceName, err)
		return
	}
	// 创建数据包源
	packetSource := gopacket.NewPacketSource(handle, handle.LinkType())

	// 循环读取数据包
	if global.IsDebug == true {
		fmt.Printf("开始在接口 %s 上捕获 TCP 数据包（端口 8001）...\n", deviceName)
	}
	for packet := range packetSource.Packets() {
		handlePacket(packet)
	}
}

var fullbuf = []byte{}
var fullsize = 0
var waitbuf = false

func handlePacket(packet gopacket.Packet) {
	if tcpLayer := packet.Layer(layers.LayerTypeTCP); tcpLayer != nil {
		if appLayer := packet.ApplicationLayer(); appLayer != nil {
			psh := tcpLayer.(*layers.TCP).PSH
			dstPort := int(tcpLayer.(*layers.TCP).DstPort)
			payload := appLayer.Payload()

			// 客户端 -> 服务器：请求方向（目标端口为8001）
			if dstPort == 8001 {
				handleClientRequest(packet, payload)
				return
			}

			// 服务器 -> 客户端：响应方向（落盘记录响应帧，用于确认请求<->响应对应关系）
			handleServerResponse(packet, payload)

			if len(payload) < 8 {
				return
			}
			var srcIP string
			var dstIP string
			var srcProt int
			var dstProt int
			if ipLayer := packet.NetworkLayer(); ipLayer != nil {
				switch ip := ipLayer.(type) {
				case *layers.IPv4:
					srcProt = int(tcpLayer.(*layers.TCP).SrcPort)
					dstProt = int(tcpLayer.(*layers.TCP).DstPort)
					srcIP = ip.SrcIP.String() + ":" + strconv.Itoa(srcProt)
					dstIP = ip.DstIP.String() + ":" + strconv.Itoa(dstProt)
				case *layers.IPv6:
					srcProt = int(tcpLayer.(*layers.TCP).SrcPort)
					dstProt = int(tcpLayer.(*layers.TCP).DstPort)
					srcIP = ip.SrcIP.String() + ":" + strconv.Itoa(srcProt)
					dstIP = ip.DstIP.String() + ":" + strconv.Itoa(dstProt)
				}
			}

			if global.ExVar.BindIpInfo == true && global.OnlySrcIp != "" && global.OnlyDstIp != "" {
				if global.OnlySrcIp != srcIP || global.OnlyDstIp != dstIP {
					if global.IsDebug == true {
						fmt.Println("IP信息不符合跳过数据处理")
					}
					return
				}
			}

			var buf []byte
			if psh != true {
				waitbuf = true
				fullbuf = append(fullbuf, payload...)
				return
			} else {
				if waitbuf == true {
					waitbuf = false
					buf = append(fullbuf, payload...)
					fullbuf = []byte{}
				} else {
					buf = payload
				}
			}

			if global.IsDebug == true {
				fmt.Println("")
				fmt.Println("====================================================")
				fmt.Println("")
			}
			bufread := NewBufferFrom(buf)
			bufsize := bufread.ReadInt()
			if global.IsDebug == true {
				fmt.Println("包大小", bufsize)
			}
			cmdId := bufread.ReadInt()
			if global.IsDebug == true {
				fmt.Println("协议号", cmdId)
			}

			if len(buf) > 14 {
				if global.IsDebug == true {
					fmt.Println("数据类型", buf[12])
				}

				if buf[12] == 3 {
					//fmt.Println(len(buf), bufsize, cmdId, "-----------")
					if len(buf)-bufsize != 4 && (cmdId == 103 || cmdId == 92) {
						global.LossCmdId = cmdId
						global.LossBytes = buf
						global.PacketLoss = true
						global.NeedBufSize = bufsize
					} else {
						go ParseData(cmdId, buf[17:])
					}

				} else if buf[12] == 5 {
					//println(buf)
					if global.IsDebug == true {
						data := DecodeType5(buf[12:])
						fmt.Println(data)
					}
				} else if buf[12] == 2 {

					//if cmdId == 5028 || cmdId == 5026 {
					//	fmt.Println(string(buf[12:]))
					//}
					//
					//if cmdId == 5028 {
					//	Parse5028(buf[13:])
					//}
				} else if cmdId > 99999 && global.PacketLoss == true && (global.LossCmdId == 103 || global.LossCmdId == 92) {
					result := make([]byte, len(buf)+len(global.LossBytes))
					copy(result, global.LossBytes)
					copy(result[len(global.LossBytes):], buf)
					if len(buf)+len(global.LossBytes)-global.NeedBufSize != 4 {
						global.LossBytes = result
					} else {
						global.PacketLoss = false
						go ParseData(global.LossCmdId, result[17:])
					}

				}

				if cmdId == 3686 {
					var data []byte
					if buf[12] == 5 {
						data = []byte(DecodeType5(buf[12:]))
					} else if buf[12] == 3 {
						data = parseZlibData(buf[17:])
					}

					if global.ExVar.NeedPushBookData {
						go parseBookData(data)
					}

					if databaseSelected == false {
						var raw []interface{}
						err := json.Unmarshal([]byte(data), &raw)
						if err != nil {
							log.Fatal(err)
						} else {
							dataMap := raw[1].(map[string]interface{})
							server, ok := dataMap["server"].([]interface{})
							if ok {
								log.Printf("服务器信息: %v\n", server)
							}

							var roleName string
							if logData, ok := dataMap["log"].(map[string]interface{}); ok {
								roleName = logData["role_name"].(string)
								log.Printf("角色名: %s\n", roleName)
							}

							log.Println("本地IP：" + dstIP)
							log.Println("游戏服务器IP：" + srcIP)
							global.OnlySrcIp = srcIP
							global.OnlyDstIp = dstIP
							dabesename := roleName + "_" + server[0].(string)
							log.Println("收到主公簿数据，将打开数据库文件" + dabesename + ".db")
							model.InitDB(dabesename)
							databaseSelected = true
						}
					}
				}
			}

			if global.IsDebug == true {
				fmt.Print("[]byte{")
				for i, b := range buf {
					if i > 0 {
						fmt.Print(", ")
					}
					fmt.Print(b)
				}
				fmt.Println("}")
				fmt.Println("")
				fmt.Println("====================================================")
				fmt.Println("")
			}
		}
	}
}

type Buffer struct {
	Byte   []byte
	pos    int
	offset int
}

// ---------------- 客户端->服务器 请求方向处理（协议分析用） ----------------

var (
	clientReqFullbuf = []byte{}
	clientReqWait    = false

	// 按连接(5元组)分组重组的客户端请求流，避免多连接交错搅乱帧
	clientStreamsMu sync.Mutex
	clientStreams   = map[string]*clientStream{}
)

type clientStream struct {
	buf []byte
}

const maxClientFrame = 8 * 1024 * 1024

// handleClientRequest 客户端->服务器方向：按帧头长度字段[0:4]重组(总长=长度+4)，
// 不依赖PSH，多分片/多帧合并均正确处理；按连接分组避免多连接交错。
func handleClientRequest(packet gopacket.Packet, payload []byte) {
	if payload == nil || len(payload) == 0 {
		return
	}
	streamKey := ""
	serverAddr := ""
	if tcpLayer := packet.Layer(layers.LayerTypeTCP); tcpLayer != nil {
		streamKey = tcpLayer.(*layers.TCP).SrcPort.String() + "-" + tcpLayer.(*layers.TCP).DstPort.String()
	}
	if ipLayer := packet.NetworkLayer(); ipLayer != nil {
		dstPort := 0
		if tcpLayer := packet.Layer(layers.LayerTypeTCP); tcpLayer != nil {
			dstPort = int(tcpLayer.(*layers.TCP).DstPort)
		}
		switch ip := ipLayer.(type) {
		case *layers.IPv4:
			serverAddr = ip.DstIP.String() + ":" + strconv.Itoa(dstPort)
		case *layers.IPv6:
			serverAddr = ip.DstIP.String() + ":" + strconv.Itoa(dstPort)
		}
	}

	clientStreamsMu.Lock()
	st, ok := clientStreams[streamKey]
	if !ok {
		st = &clientStream{}
		clientStreams[streamKey] = st
	}
	st.buf = append(st.buf, payload...)
	var frames [][]byte
	for len(st.buf) >= 4 {
		need := int(binary.BigEndian.Uint32(st.buf[0:4])) + 4
		if need < 4 || need > maxClientFrame {
			st.buf = []byte{}
			break
		}
		if len(st.buf) < need {
			break
		}
		frames = append(frames, st.buf[:need])
		st.buf = st.buf[need:]
	}
	clientStreamsMu.Unlock()

	for _, buf := range frames {
		// 无条件缓存44871翻页模板（供直连重放）
		stashFetchTemplate(buf, serverAddr)
		// 缓存建连握手帧（新连接的首个44871大帧）
		stashLoginFrame(buf, serverAddr)
		// 缓存登录后的初始化指令帧（打开战报列表/切换同盟战报等，翻页前必须重放）
		stashInitFrame(buf, serverAddr, streamKey)
		// 缓存 mode=0000000a 批量战报详情请求帧（翻页后拉取每条战报完整数据cmd=92）
		stashBatchFrame(buf, serverAddr)
		// 缓存 mode=0000005c 战报详情请求帧（含battle_id，回放可获得cmd=92完整战报）
		stashDetailFrame(buf, serverAddr)
		// 缓存 mode=000010e9 打开战报面板请求帧（详情请求前必须先打开面板）
		stashOpenPanelFrame(buf, serverAddr)
		// 落盘记录所有客户端请求（供逆向建连握手序列）
		logClientRequest(buf, serverAddr, streamKey)
		analyzeClientRequest(buf)
	}
}

var reqLogMu sync.Mutex

// ---------------- 服务器->客户端 响应方向处理（协议分析用） ----------------

var (
	serverStreamsMu sync.Mutex
	serverStreams   = map[string]*serverStream{}
)

type serverStream struct {
	buf []byte
}

// handleServerResponse 服务器->客户端方向：按帧头长度字段[0:4]重组后落盘记录，
// 与 requests_debug.log 时间戳对照即可确认 每个请求 -> 哪些响应cmd 的对应关系。
func handleServerResponse(packet gopacket.Packet, payload []byte) {
	if payload == nil || len(payload) == 0 {
		return
	}
	streamKey := ""
	serverAddr := ""
	if tcpLayer := packet.Layer(layers.LayerTypeTCP); tcpLayer != nil {
		streamKey = tcpLayer.(*layers.TCP).SrcPort.String() + "-" + tcpLayer.(*layers.TCP).DstPort.String()
	}
	if ipLayer := packet.NetworkLayer(); ipLayer != nil {
		srcPort := 0
		if tcpLayer := packet.Layer(layers.LayerTypeTCP); tcpLayer != nil {
			srcPort = int(tcpLayer.(*layers.TCP).SrcPort)
		}
		switch ip := ipLayer.(type) {
		case *layers.IPv4:
			serverAddr = ip.SrcIP.String() + ":" + strconv.Itoa(srcPort)
		case *layers.IPv6:
			serverAddr = ip.SrcIP.String() + ":" + strconv.Itoa(srcPort)
		}
	}

	serverStreamsMu.Lock()
	st, ok := serverStreams[streamKey]
	if !ok {
		st = &serverStream{}
		serverStreams[streamKey] = st
	}
	st.buf = append(st.buf, payload...)
	var frames [][]byte
	for len(st.buf) >= 4 {
		need := int(binary.BigEndian.Uint32(st.buf[0:4])) + 4
		if need < 4 || need > maxClientFrame {
			st.buf = []byte{}
			break
		}
		if len(st.buf) < need {
			break
		}
		frames = append(frames, st.buf[:need])
		st.buf = st.buf[need:]
	}
	serverStreamsMu.Unlock()

	for _, buf := range frames {
		logServerResponse(buf, serverAddr, streamKey)
	}
}

var respLogMu sync.Mutex

// logServerResponse 将服务器->客户端响应帧附加到 responses_debug.log
func logServerResponse(buf []byte, serverAddr string, streamKey string) {
	respLogMu.Lock()
	defer respLogMu.Unlock()
	f, err := os.OpenFile("responses_debug.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	if _, seen := seenRespStreams[streamKey]; !seen {
		seenRespStreams[streamKey] = struct{}{}
		f.WriteString("\n=== NEW CONNECTION " + streamKey + " " + serverAddr + " ===\n")
	}
	var cmdId int
	if len(buf) >= 8 {
		cmdId = int(binary.BigEndian.Uint32(buf[4:8]))
	}
	line := time.Now().Format("15:04:05.000") + " " + serverAddr + " cmd=" + strconv.Itoa(cmdId) + " len=" + strconv.Itoa(len(buf)) + "\n" + hex.EncodeToString(buf) + "\n"
	f.WriteString(line)
}

var seenRespStreams = map[string]struct{}{}

// logClientRequest 将所有客户端->服务器请求附加到 requests_debug.log（用于分析建连握手）
func logClientRequest(buf []byte, serverAddr string, streamKey string) {
	reqLogMu.Lock()
	defer reqLogMu.Unlock()
	f, err := os.OpenFile("requests_debug.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	// 新连接的第一帧打上连接标识，便于区分各会话/识别握手帧
	if _, seen := seenStreams[streamKey]; !seen {
		seenStreams[streamKey] = struct{}{}
		f.WriteString("\n=== NEW CONNECTION " + streamKey + " " + serverAddr + " ===\n")
	}
	var cmdId int
	if len(buf) >= 8 {
		cmdId = int(binary.BigEndian.Uint32(buf[4:8]))
	}
	line := time.Now().Format("15:04:05.000") + " " + serverAddr + " cmd=" + strconv.Itoa(cmdId) + " len=" + strconv.Itoa(len(buf)) + "\n" + hex.EncodeToString(buf) + "\n"
	f.WriteString(line)
}

var seenStreams = map[string]struct{}{}

// decodeClientPayload 尝试按上下行相同的帧结构解析客户端请求
// 帧结构(下行推断)：[0-3]包大小 [4-7]cmdId [8-11]? [12]类型 [13-16]? [17..]数据
// 返回值: (类型描述, 解码后的可读内容)
func decodeClientPayload(buf []byte) (int, string, string) {
	if len(buf) < 13 {
		return 0, "raw", hex.EncodeToString(buf)
	}
	actualSize := 0
	btype := buf[12]
	if len(buf) >= 4 {
		actualSize = int(binary.BigEndian.Uint32(buf[0:4]))
	}
	desc := fmt.Sprintf("size=%d", actualSize)

	if btype == 3 {
		// zlib 压缩数据
		if len(buf) > 17 && buf[0+17] == 120 && buf[1+17] == 156 {
			body := parseZlibData(buf[17:])
			if len(body) > 0 {
				return 3, desc, string(body)
			}
		}
		return 3, desc, "zlib数据(" + strconv.Itoa(len(buf)) + "字节)"
	}
	if btype == 5 {
		// xor 152 加密数据
		decoded := DecodeType5(buf[12:])
		if decoded != "" {
			return 5, desc, decoded
		}
		return 5, desc, "type5数据"
	}
	if btype == 2 {
		// 尝试读明文
		if len(buf) > 17 {
			raw := string(buf[17:])
			if strings.Contains(raw, "{") || strings.Contains(raw, "[") {
				return 2, desc, raw
			}
		}
		return 2, desc, hex.EncodeToString(buf)
	}

	// 兜底：尝试找 zlib 魔数
	for i := 4; i < len(buf)-1; i++ {
		if buf[i] == 120 && buf[i+1] == 156 {
			body := parseZlibData(buf[i:])
			if len(body) > 0 {
				return int(buf[12]), "zlib@" + strconv.Itoa(i), string(body)
			}
		}
	}
	return int(buf[12]), desc, hex.EncodeToString(buf)
}

func analyzeClientRequest(buf []byte) {
	if !global.ExVar.NeedCaptureRequests {
		return
	}
	if global.AppCtx == nil {
		return
	}
	info := map[string]interface{}{
		"time": time.Now().Unix(),
		"len":  len(buf),
		"hex":  hex.EncodeToString(buf),
	}
	cmdId := 0
	if len(buf) >= 8 {
		cmdId = int(binary.BigEndian.Uint32(buf[4:8]))
	}
	info["cmdId"] = cmdId

	t, d, body := decodeClientPayload(buf)
	info["type"] = t
	if len(body) > 2000 {
		body = body[:2000] + "..."
	}
	info["body"] = body

	if global.IsDebug {
		log.Printf("[请求分析] cmdId=%d %s body=%s", cmdId, d, body)
	}
	runtime.EventsEmit(global.AppCtx, "clientRequest", info)
}

func (bb *Buffer) ResetOffset() {
	bb.offset = 0
}

func NewBufferFrom(b []byte) *Buffer {
	return &Buffer{Byte: b}
}

func (bb *Buffer) ReadInt() int {
	if bb.offset+4 > len(bb.Byte) {
		return 0
	}
	value := binary.BigEndian.Uint32(bb.Byte[bb.offset : bb.offset+4])
	bb.offset += 4
	return int(value)
}

func (bb *Buffer) ReadByte() byte {
	if bb.offset+1 > len(bb.Byte) {
		return 0
	}
	value := bb.Byte[bb.offset : bb.offset+1]
	bb.offset += 1
	return value[0]
}
