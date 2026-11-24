# -*- coding: utf-8 -*-
"""
排行榜抓包实时调试面板
======================
独立运行的本地调试工具：tail 解析 npacp 落盘的 responses/requests_debug.log，
实时展示 cmd=700（势力榜）、cmd=8009（击杀/守备榜）等排行榜帧的解析结果，
用于用户打开游戏排行榜翻页时确认协议与字段。

用法:  python tools/rank_debug.py [端口，默认8765]
打开:  http://127.0.0.1:8765

不修改主程序、不重编译 wails、不影响正在运行的软件。
"""
import io
import json
import os
import re
import sys
import threading
import time
import zlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
RESP_LOG = os.path.join(ROOT, "build", "bin", "responses_debug.log")
REQ_LOG = os.path.join(ROOT, "build", "bin", "requests_debug.log")

HEAD_RE = re.compile(r"^(\d{2}:\d{2}:\d{2}\.\d{3}) .* cmd=(-?\d+) len=(\d+)$")

# 排行榜相关 cmd 标注
RANK_CMDS = {700: "势力榜", 8009: "击杀/守备榜", 2100: "候选-列表", 6314: "候选", 5026: "候选", 5028: "候选", 708: "候选", 509: "候选"}

recents = []          # 最近的响应帧 (ts, cmd, len, readable_preview, parsed) 最多 300
rank_frames = []      # 解析好的排行榜帧, 最多 200
cur_time = time.time()

lock = threading.Lock()


def parse_log_file(path):
    """返回 [(ts, cmd, data_bytes), ...] —— 从文件头全量解析（文件不大，1MB级）。"""
    if not os.path.exists(path):
        return []
    with io.open(path, "r", encoding="utf-8", errors="replace") as f:
        lines = f.readlines()
    out = []
    i, n = 0, len(lines)
    while i < n:
        m = HEAD_RE.match(lines[i].strip())
        if not m:
            i += 1
            continue
        ts, cmd, ln = m.group(1), int(m.group(2)), int(m.group(3))
        hexs = []
        i += 1
        while i < n and not HEAD_RE.match(lines[i].strip()) and not lines[i].startswith("=== NEW CONNECTION"):
            hexs.append(lines[i].strip())
            i += 1
        try:
            data = bytes.fromhex("".join(hexs))
        except Exception:
            data = b""
        out.append((ts, cmd, data))
    return out


def body_of(data):
    """返回 (type, body)。type: 3=zlib, 5=xor152, 2=明文, 其他未知。"""
    if len(data) < 13:
        return None, b""
    t = data[12]
    return t, data[17:] if len(data) > 17 else b""


def readable_text(data):
    """尽力解出可读文本。"""
    t, body = body_of(data)
    if t == 3:
        for off in (0, 1, 2, 3, 4):
            try:
                return zlib.decompress(body[off:]).decode("utf-8", "replace")
            except Exception:
                continue
        return None
    if t == 5:
        try:
            return bytes(b ^ 152 for b in body).decode("utf-8", "replace")
        except Exception:
            return None
    if t == 2:
        try:
            return body.decode("utf-8", "replace")
        except Exception:
            return None
    return None


def lenient_json(s):
    """宽容解析：把 dict 的数字键 {13826:1} 转成字符串键再做 json.loads。"""
    s2 = re.sub(r'([,{]\s*)(\d+)(:)', r'\1"\2"\3', s)
    try:
        return json.loads(s2)
    except Exception:
        return None


def parse_rank_frame(cmd, text):
    """把 cmd=700 / 8009 帧文本解析成结构化榜单。成功返回 dict，失败返回 None。"""
    try:
        if cmd == 700:
            # 完整结构 [0,-1,{self},300,[[0,{e}],...],null,{土地:等级}]
            if text.startswith("[0,-1,"):
                obj = lenient_json(text)
                if not isinstance(obj, list) or len(obj) < 5:
                    return None
                self_info = obj[2] if isinstance(obj[2], dict) else None
                entries_raw = obj[4] if isinstance(obj[4], list) else []
                entries = []
                for pair in entries_raw:
                    if not isinstance(pair, list) or len(pair) < 2:
                        continue
                    row = {"rank": len(entries) + 1}
                    ent = pair[1] if isinstance(pair[1], dict) else {}
                    for k in ("name", "power", "land_count", "fort_count", "force",
                              "region", "role_id", "user_id", "area", "branch_city_count",
                              "refresh_time", "shu_cheng_count", "show_info"):
                        if k in ent:
                            row[k] = ent[k]
                    entries.append(row)
                return {"cmd": cmd, "kind": RANK_CMDS.get(cmd, "?"), "self": self_info, "entries": entries,
                        "marker": obj[3] if len(obj) > 3 else None}
        elif cmd == 8009:
            # 文本形如 -1,{self},3,[[0,{e}],...],null] —— 补一个左括号成整体
            s = text
            if not s.startswith("["):
                s = "[" + s
            obj = lenient_json(s)
            if not isinstance(obj, list) or len(obj) < 4:
                return None
            self_info = obj[1] if isinstance(obj[1], dict) else None
            entries_raw = obj[3] if isinstance(obj[3], list) else []
            entries = []
            for pair in entries_raw:
                if not isinstance(pair, list) or len(pair) < 2:
                    continue
                row = {"rank": len(entries) + 1}
                ent = pair[1] if isinstance(pair[1], dict) else {}
                for k in ("defend_strength", "kill_count", "lose_count", "refresh_time",
                          "user_id", "user_info"):
                    if k in ent:
                        row[k] = ent[k]
                # user_info 是 JSON 字符串 ["名字","联盟","坐标"]
                ui = row.get("user_info")
                if isinstance(ui, str):
                    try:
                        parts = json.loads(ui)
                        row["name"] = parts[0] if parts else None
                        row["alliance_xy"] = parts[1] if len(parts) > 1 else None
                    except Exception:
                        row["name"] = ui
                entries.append(row)
            return {"cmd": cmd, "kind": RANK_CMDS.get(cmd, "?"), "self": self_info, "entries": entries}
    except Exception:
        return None
    return None


def build_snapshot():
    global cur_time
    resps = parse_log_file(RESP_LOG)
    reqs = parse_log_file(REQ_LOG)

    recents.clear()
    rank_frames.clear()
    cmds = {}
    for ts, cmd, data in resps:
        cmds[cmd] = cmds.get(cmd, 0) + 1
        prev = readable_text(data)
        parsed = None
        if cmd in (700, 8009):
            if prev:
                parsed = parse_rank_frame(cmd, prev)
                if parsed:
                    parsed["ts"] = ts
                    parsed["raw_len"] = len(data)
                    rank_frames.append(parsed)
        recents.append({
            "ts": ts, "cmd": cmd, "len": len(data),
            "preview": (prev or "")[:80],
            "is_rank": cmd in RANK_CMDS,
            "parsed": parsed is not None,
        })
        if len(recents) > 300:
            recents.pop(0)
    if len(rank_frames) > 200:
        del rank_frames[:-200]

    req_cmds = {}
    for _, cmd, _ in reqs:
        req_cmds[cmd] = req_cmds.get(cmd, 0) + 1

    # 请求侧最近 15 条 44871 的时间线（用于对照响应）
    recent_44871 = []
    for ts, cmd, data in reqs[-300:]:
        if cmd == 44871:
            t, body = body_of(data)
            recent_44871.append({"ts": ts, "type": t, "len": len(data),
                                 "hex": body[:48].hex() if body else ""})
            if len(recent_44871) >= 15:
                break

    cur_time = time.time()
    return {
        "time": time.strftime("%H:%M:%S"),
        "resp_path": RESP_LOG,
        "resp_exists": os.path.exists(RESP_LOG),
        "resp_size": os.path.getsize(RESP_LOG) if os.path.exists(RESP_LOG) else 0,
        "resp_mtime": time.strftime("%H:%M:%S", time.localtime(os.path.getmtime(RESP_LOG))) if os.path.exists(RESP_LOG) else "?",
        "resp_total": len(resps),
        "resp_cmds": cmds,
        "rank_frames": rank_frames,
        "recents": list(reversed(recents)),
        "req_total": len(reqs),
        "req_cmds": req_cmds,
        "recent_44871": recent_44871,
    }


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/" or self.path == "/index.html":
            html = PAGE
            data = html.encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
            return
        if self.path.startswith("/api/snapshot"):
            snap = build_snapshot()
            data = json.dumps(snap, ensure_ascii=False).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
            return
        self.send_response(404)
        self.end_headers()
        self.wfile.write(b"not found")

    def log_message(self, fmt, *args):
        pass


PAGE = """<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>排行榜抓包实时调试</title>
<style>
body{font-family:'Microsoft YaHei',sans-serif;background:#0f1115;color:#e6e6e6;margin:0;padding:16px}
h1{font-size:20px;margin:0 0 4px}
.sub{color:#888;font-size:12px;margin-bottom:12px}
.cards{display:flex;gap:12px;flex-wrap:wrap;margin-bottom:12px}
.card{background:#1a1d24;border:1px solid #2a2e38;border-radius:8px;padding:10px 14px;min-width:150px}
.card .t{color:#888;font-size:11px}
.card .v{font-size:18px;font-weight:bold;margin-top:2px}
table{border-collapse:collapse;width:100%;font-size:12px}
th,td{border:1px solid #2a2e38;padding:4px 8px;text-align:right;white-space:nowrap}
th{background:#232733;color:#ccc}
td:first-child{text-align:left}
.sec{font-size:15px;font-weight:bold;margin:16px 0 6px;color:#ffd479}
.rank700{color:#7db8ff}
.rank8009{color:#8be08b}
.badge{display:inline-block;padding:1px 6px;border-radius:4px;font-size:11px;margin-left:6px}
.b700{background:#1d3a5f;color:#7db8ff}
.b8009{background:#1d4a2a;color:#8be08b}
.new{background:#2a2015 !important}
pre{background:#12141a;border:1px solid #2a2e38;border-radius:8px;padding:10px;font-size:11px;overflow-x:auto;max-height:300px;white-space:pre-wrap;word-break:break-all}
</style>
</head>
<body>
<h1>🏆 排行榜抓包实时调试 <span id="status" class="badge" style="background:#333;color:#ccc">连接中…</span></h1>
<div class="sub" id="sub">读取 <code>build/bin/responses_debug.log</code> + <code>requests_debug.log</code>；请打开游戏排行榜并翻页，本页每2秒自动刷新。</div>
<div class="cards" id="cards"></div>
<div class="sec">📊 cmd 分布（响应侧）</div>
<div class="card" style="min-width:auto"><span class="t" id="resp_cmds"></span></div>

<div class="sec">🎯 解析出的排行榜帧</div>
<div id="ranks"></div>

<div class="sec">🕐 最近响应帧</div>
<div style="overflow-x:auto"><table id="recents"><tr><th>时间</th><th>cmd</th><th>len</th><th>rank?</th><th>可读预览</th></tr></table></div>

<div class="sec">📤 最近 44871 请求（对照响应）</div>
<div style="overflow-x:auto"><table id="reqs"><tr><th>时间</th><th>type</th><th>len</th><th>body hex 前48字节</th></tr></table></div>

<script>
let firstLoad = null;
async function refresh(){
  try{
    const r = await fetch('/api/snapshot', {cache:'no-store'});
    const s = await r.json();
    if(!firstLoad) firstLoad = s.time;
    document.getElementById('status').textContent = '已连接 ' + s.time + '  · 自动刷新中';
    document.getElementById('sub').textContent = '读取 ' + s.resp_path + '（' + (s.resp_exists?'存在':'不存在') + '，' + s.resp_size + ' 字节，最后写入 ' + s.resp_mtime + '）· 响应总帧 ' + s.resp_total + ' · 请求总帧 ' + s.req_total;
    document.getElementById('cards').innerHTML =
      '<div class="card"><div class="t">响应帧总数</div><div class="v">'+s.resp_total+'</div></div>' +
      '<div class="card"><div class="t">请求帧总数</div><div class="v">'+s.req_total+'</div></div>' +
      '<div class="card"><div class="t">日志最后写入</div><div class="v">'+s.resp_mtime+'</div></div>' +
      '<div class="card"><div class="t">排行榜帧(解析成功)</div><div class="v">'+s.rank_frames.length+'</div></div>';
    document.getElementById('resp_cmds').textContent = JSON.stringify(s.resp_cmds);
    // 排行榜帧
    let rh = '';
    s.rank_frames.slice().reverse().forEach(f=>{
      const cls = f.cmd===700?'rank700':'rank8009';
      const b = f.cmd===700?'b700':'b8009';
      rh += '<div class="sec '+cls+'">'+(f.cmd===700?"📜 势力榜 ":"⚔️ 击杀/守备榜 ")+'@'+f.ts+' <span class="badge '+b+'">cmd='+f.cmd+'</span> 条目 '+f.entries.length+' 条</div>';
      if(f.self){
        rh += '<div class="sub" style="margin:2px 0 6px">自己: ' + (f.self.name||'?') + ' · 战力 '+(f.self.power??'?')+' · 土地 '+(f.self.land_count??'?')+' · 城 '+(f.self.fort_count??'?')+'</div>';
      }
      rh += '<div style="overflow-x:auto"><table><tr><th>#</th>';
      if(f.cmd===700){ rh += '<th>名称</th><th>战力</th><th>土地</th><th>城</th><th>势力</th><th>区域</th><th>role_id</th><th>user_id</th>'; }
      else { rh += '<th>名称</th><th>守备</th><th>击杀</th><th>战损</th><th>user_id</th>'; }
      rh += '</tr>';
      f.entries.slice(0,30).forEach(e=>{
        rh += '<tr><td>'+e.rank+'</td>';
        if(f.cmd===700){ rh += '<td>'+(e.name??'')+'</td><td>'+(e.power??'')+'</td><td>'+(e.land_count??'')+'</td><td>'+(e.fort_count??'')+'</td><td>'+(e.force??'')+'</td><td>'+(e.region??'')+'</td><td>'+(e.role_id??'')+'</td><td>'+(e.user_id??'')+'</td>'; }
        else { rh += '<td>'+(e.name??'')+'</td><td>'+(e.defend_strength??'')+'</td><td>'+(e.kill_count??'')+'</td><td>'+(e.lose_count??'')+'</td><td>'+(e.user_id??'')+'</td>'; }
        rh += '</tr>';
      });
      rh += '</table></div>';
    });
    if(!s.rank_frames.length){
      rh += '<div class="sub">尚无解析成功的排行榜帧 —— 请打开游戏排行榜翻页（cmd=700 势力榜 / cmd=8009 击杀守备榜）</div>';
    }
    document.getElementById('ranks').innerHTML = rh;
    // 最近响应帧
    let rt = '';
    s.recents.slice(0,80).forEach(f=>{
      const newf = (f.ts >= firstLoad) ? ' class="new"' : '';
      rt += '<tr'+newf+'><td>'+f.ts+'</td><td>'+f.cmd+'</td><td>'+f.len+'</td><td>'+((f.is_rank?'<span class="badge '+(f.cmd===700?'b700':'b8009')+'">'+f.cmd+'</span>':'—'))+'</td><td style="text-align:left;max-width:520px;overflow:hidden;text-overflow:ellipsis">'+(f.parsed?'<b>✓解析</b> ':'')+(f.preview||'').replace(/&/g,'&amp;').replace(/</g,'&lt;')+'</td></tr>';
    });
    document.getElementById('recents').innerHTML = '<tr><th>时间</th><th>cmd</th><th>len</th><th>rank?</th><th>可读预览</th></tr>'+rt;
    // 44871
    let qt = '';
    s.recent_44871.forEach(f=>{ qt += '<tr><td>'+f.ts+'</td><td>'+f.type+'</td><td>'+f.len+'</td><td style="text-align:left;font-family:monospace">'+f.hex+'</td></tr>'; });
    document.getElementById('reqs').innerHTML = '<tr><th>时间</th><th>type</th><th>len</th><th>body hex 前48字节</th></tr>'+qt;
  }catch(e){
    document.getElementById('status').textContent = '连接失败: ' + e;
  }
}
setInterval(refresh, 2000);
refresh();
</script>
</body>
</html>
"""


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8765
    print("排行榜抓包实时调试面板 -> http://127.0.0.1:%d" % port)
    print("日志:", RESP_LOG)
    srv = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()