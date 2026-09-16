import os
import json
import re
import time
import urllib.request
from datetime import datetime, timedelta

import streamlit as st
import pandas as pd

st.set_page_config(page_title="同盟数据查询", page_icon="🗡️", layout="wide")


class TursoCursor:
    """Turso HTTP API 游标(模拟 DBAPI2 供 pandas.read_sql_query 使用)"""

    def __init__(self, conn):
        self.conn = conn
        self.description = []
        self.rows = []
        self.rowcount = -1
        self._i = 0

    def execute(self, sql, params=None):
        desc, rows = self.conn._run(sql, params)
        self.description = desc
        self.rows = rows
        self.rowcount = -1
        self._i = 0
        return self

    def fetchall(self):
        return self.rows

    def fetchmany(self, size):
        r = self.rows[:size]
        self.rows = self.rows[size:]
        return r

    def fetchone(self):
        if self._i >= len(self.rows):
            return None
        r = self.rows[self._i]
        self._i += 1
        return r

    def close(self):
        pass


class TursoConnection:
    """基于标准库 urllib 的 Turso(Hrana over HTTP) 连接，零第三方依赖"""

    def __init__(self, url, token):
        self.url = url.replace("libsql://", "https://").rstrip("/")
        self.token = token

    @staticmethod
    def _lit(v):
        if v is None:
            return "NULL"
        if isinstance(v, int):
            return str(v)
        if isinstance(v, float):
            return repr(v)
        if isinstance(v, bool):
            return "1" if v else "0"
        return "'" + str(v).replace("'", "''") + "'"

    def _run(self, sql, params=None):
        if params:
            parts = str(sql).split("?")
            sql = parts[0]
            for p, rest in zip(params, parts[1:]):
                sql += self._lit(p) + rest
        body = json.dumps({"requests": [{"type": "execute", "stmt": {"sql": sql}}], "batches": []}).encode()
        req = urllib.request.Request(self.url + "/v2/pipeline", data=body, method="POST",
                                     headers={"Authorization": "Bearer " + self.token,
                                              "Content-Type": "application/json"})
        with urllib.request.urlopen(req) as resp:
            out = json.loads(resp.read())
        res = out["results"][0]
        if res.get("type") == "error":
            raise RuntimeError(res.get("error", {}).get("message", "Turso error"))
        result = res["response"]["result"]
        desc = [(c["name"], None, None, None, None, None, None) for c in result["cols"]]
        rows = [tuple(None if isinstance(v, dict) and v.get("type") == "null" else v.get("value", v)
                      for v in row) for row in result["rows"]]
        return desc, rows

    def execute(self, sql, params=None):
        return self.cursor().execute(sql, params)

    def cursor(self):
        return TursoCursor(self)

    def commit(self):
        pass

    def rollback(self):
        pass

    def close(self):
        pass


def turso_secret(key):
    """安全读取 st.secrets，本地未配置 secrets 文件时返回默认值"""
    try:
        return st.secrets.get(key, "")
    except Exception:
        return ""


def get_conn():
    """固定云端数据源：应用端同步器(sync.go)把本地数据推送到 Turso，本网站只从 Turso 读"""
    turso_url = turso_secret("TURSO_URL")
    turso_token = turso_secret("TURSO_TOKEN")
    if turso_url and turso_token:
        return TursoConnection(turso_url, turso_token)
    st.error("云端 Turso 未配置：请在 .streamlit/secrets.toml 设置 TURSO_URL/TURSO_TOKEN（与应用端 turso.json 同源）")
    return None


def data_fingerprint():
    """云端数据指纹(轻量)：关键表行数与最新时间戳。只查标量聚合，读量极小；
    用于判断数据是否变动——无变动时直接复用缓存，不重复读云端"""
    conn = get_conn()
    if conn is None:
        return None
    try:
        row = conn.execute(
            """SELECT (SELECT COUNT(*) FROM team_user WHERE name != ''),
                      (SELECT IFNULL(MAX(id), 0) FROM team_user),
                      (SELECT IFNULL(MAX(battle_id), 0) FROM battle_report),
                      (SELECT IFNULL(MAX(time), 0) FROM battle_report),
                      (SELECT IFNULL(MAX(battle_id), 0) FROM reports)"""
        ).fetchone()
    except Exception:
        return None
    return tuple(row)


def maybe_refresh_cache():
    """每次切换界面时轻量检查数据是否有更新：指纹变化才清缓存重读，无变动则复用缓存"""
    global _resolve_my_union
    fp = data_fingerprint()
    if fp is None:
        return
    if st.session_state.get("_fp") != fp:
        st.cache_data.clear()
        _resolve_my_union = None
        st.session_state["_fp"] = fp


# ---------- 数据配置(与桌面版 cfg.js 同步) ----------

import os as _os

_cfg_dir = _os.path.dirname(__file__)


def _load_cfg(name):
    try:
        with open(_os.path.join(_cfg_dir, name), encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return {}


hero_cfg = _load_cfg("hero_cfg.json")
skill_cfg = _load_cfg("skill_cfg.json")
gear_feature_cfg = _load_cfg("gear_feature_cfg.json")
gear_cfg = {str(g.get("gear_id")): g for g in _load_cfg("gear_cfg.json")}


def resolve_hero_id(hid):
    if not hid:
        return hid
    try:
        n = int(hid)
    except (TypeError, ValueError):
        return hid
    return n - 30000 if n >= 130000 else n


def hero_name(hid):
    if not hid:
        return ""
    h = hero_cfg.get(str(resolve_hero_id(hid)))
    return h.get("name", "") if h else f"未知({hid})"


def hero_icon_url(hid):
    if not hid:
        return ""
    h = hero_cfg.get(str(resolve_hero_id(hid)))
    icon = h.get("iconId") if h else hid
    return f"https://g0.gph.netease.com/ngsocial/community/stzb/cn/cards/cut/card_small_{icon}.jpg?gameid=g10"


def skill_name(sid):
    if not sid or str(sid) == "0":
        return ""
    s = skill_cfg.get(str(sid))
    return s.get("name", "") if s else f"未知({sid})"


def gear_name(gid):
    if not gid or str(gid) == "0":
        return ""
    g = gear_cfg.get(str(gid))
    return g.get("name", "") if g else f"未知({gid})"


def gear_entry_name(eid):
    if not eid or str(eid) == "0":
        return ""
    e = gear_feature_cfg.get(str(eid))
    return e.get("name", "") if e else f"未知({eid})"


def team_role(all_skill_info):
    """根据技能索引判断攻守: 1-3 进攻, 4-6 防守"""
    try:
        idx = int(str(all_skill_info).split(";")[0].split(",")[0])
        return "defend" if idx >= 4 else "attack"
    except (ValueError, IndexError, AttributeError):
        return "attack"


def parse_skills(all_skill_info, role="attack"):
    """解析 all_skill_info: '1,id,lv,id,lv,id,lv;...' -> 每武将 [{name,lv,quality,type}...]，
    与桌面版一致: 攻取 index 1-3、守取 4-6 且倒序"""
    if not all_skill_info:
        return []
    groups = [g for g in str(all_skill_info).split(";") if g.strip()]
    parsed = []
    for g in groups:
        parts = g.split(",")
        if len(parts) < 7:
            continue
        try:
            idx = int(parts[0])
        except ValueError:
            continue
        if role == "attack" and not (1 <= idx <= 3):
            continue
        if role == "defend" and not (4 <= idx <= 6):
            continue
        infos = []
        for i in (1, 3, 5):
            sid, lv = parts[i], parts[i + 1]
            if sid and sid != "0":
                infos.append(skill_info(sid, lv))
        parsed.append(infos)
    if role == "defend":
        parsed.reverse()
    return parsed


def parse_gears(gear_info, role="attack"):
    """解析宝物: 'gearId,level,entryId;...' -> 每武将 [{name,lv,entry,entry_quality,entry_advance}]。
    与桌面 TeamCard 一致: 先丢弃空组(gearId=0)，防守方整列反转"""
    if not gear_info:
        return []
    parsed = []
    for g in str(gear_info).split(";"):
        parts = g.split(",")
        if not parts[0] or parts[0] == "0":
            continue
        entry = parts[2] if len(parts) > 2 and parts[2] and parts[2] != "0" else ""
        parsed.append({
            "name": gear_name(parts[0]),
            "lv": parts[1],
            "entry": gear_entry_name(entry) if entry else "",
            "entry_quality": gear_entry_quality(entry) if entry else 0,
            "entry_advance": gear_entry_advance(entry) if entry else 0,
        })
    if role == "defend":
        parsed.reverse()
    return parsed


def skill_info(sid, lv=""):
    """技能信息(名称/等级/品质/类型)，缺失时显示未知"""
    c = skill_cfg.get(str(sid), {})
    return {"id": str(sid), "name": c.get("name") or f"未知({sid})",
            "lv": lv, "quality": c.get("zfQuality", ""), "type": c.get("type", "")}


def gear_entry_quality(eid):
    c = gear_feature_cfg.get(str(eid), {})
    return int(c.get("quality", 0) or 0)


def gear_entry_advance(eid):
    c = gear_feature_cfg.get(str(eid), {})
    return int(c.get("advance", 0) or 0)


def team_card_html(row):
    """把一行队伍渲染成 HTML 卡片(头像+武将名+Lv/红度+战法等级/品质+宝物等级/颜色)，与桌面 TeamCard 一致"""
    role = row.get("role") or team_role(row.get("all_skill_info"))
    skills = parse_skills(row.get("all_skill_info"), role)
    gears = parse_gears(row.get("gear_info"), role)
    hero_ids = [row.get(f"h{i}") for i in (1, 2, 3)]
    hero_lvs = [row.get(f"l{i}") or "" for i in (1, 2, 3)]
    hero_stars = [row.get(f"s{i}") or 0 for i in (1, 2, 3)]

    def gear_color(g):
        if g.get("entry_advance") == 1:
            return "#e33"
        if g.get("entry_quality", 0) >= 8:
            return "#e07bb8"
        return "#4a90d9"

    def skill_badge(q):
        if q == "S":
            return "<span style='background:#ff8c00;color:#fff;border-radius:3px;font-size:10px;padding:0 3px;margin-right:3px'>S</span>"
        if q == "A":
            return "<span style='background:#4a90d9;color:#fff;border-radius:3px;font-size:10px;padding:0 3px;margin-right:3px'>A</span>"
        if q == "B":
            return "<span style='background:#8a8a8a;color:#fff;border-radius:3px;font-size:10px;padding:0 3px;margin-right:3px'>B</span>"
        return ""

    cells = []
    for i, hid in enumerate(hero_ids):
        if not hid or str(hid) == "0":
            cells.append("<div style='width:210px;text-align:center;color:#999'>?</div>")
            continue
        sk_parts = []
        for s in (skills[i] if i < len(skills) else []):
            sk_parts.append(
                f"<div style='font-size:11px;color:#333;white-space:nowrap'>{skill_badge(s.get('quality',''))}"
                f"{s.get('type','')} {s.get('name','')} "
                f"<span style='color:#888'>Lv.{s.get('lv','')}</span></div>"
            )
        sk = "".join(sk_parts)
        g = gears[i] if i < len(gears) else None
        if g and g.get("name"):
            entry_html = f"[<span style='color:{gear_color(g)}'>{g['entry']}</span>]" if g.get("entry") else ""
            gd = (f"<div style='font-size:11px;color:#b8860b;white-space:nowrap'>宝物: "
                  f"<span style='color:{gear_color(g)};font-weight:600'>{g['name']}</span>{entry_html} "
                  f"<span style='color:#888'>Lv.{g.get('lv','')}</span></div>")
        else:
            gd = ""
        cells.append(
            f"<div style='width:210px;text-align:center'>"
            f"<img src='{hero_icon_url(hid)}' style='width:56px;height:56px;object-fit:cover;border-radius:8px' onerror=\"this.style.display='none'\">"
            f"<div style='font-weight:600'>{hero_name(hid)} "
            f"<span style='color:#888;font-weight:400'>Lv.{hero_lvs[i]} · {hero_stars[i]}红</span></div>"
            f"{sk}{gd}</div>"
        )
    return f"<div style='display:flex;gap:8px;padding:8px;border:1px solid #ddd;border-radius:10px;margin:6px 0'>{''.join(cells)}</div>"


def format_ts(ts):
    """时间戳安全格式化(兼容字符串/None/非法值)，与桌面 formatTime 一致"""
    if ts is None or ts == "":
        return ""
    try:
        t = int(ts)
    except (TypeError, ValueError):
        return ""
    if t <= 0:
        return ""
    return datetime.fromtimestamp(t).strftime("%Y-%m-%d %H:%M")


def format_pos(v):
    """位置数字转坐标，与桌面 splitwid 一致: 1050328 -> 105,328"""
    if v is None or v == "":
        return ""
    s = str(int(v))
    last4 = s[-4:]
    first = s[:-4] or "0"
    return f"{first},{int(last4)}"


def score_label(s):
    """活跃度等级，与桌面 MemberActivity 一致"""
    if s >= 100:
        return "核心成员"
    if s >= 50:
        return "活跃成员"
    if s >= 20:
        return "普通成员"
    return "不活跃"


def safe_query(fn, *args, **kwargs):
    """查询兜底：数据库不可用/配额受限/连接异常时返回 None 并提示，不让页面红屏"""
    try:
        return fn(*args, **kwargs)
    except Exception as e:
        st.error(f"数据库查询失败(云端配额受限或连接异常): {e}")
        return None


def rate_limited(seconds=3):
    """查询防抖：seconds 秒内只能触发一次(返回 False 表示被限流)"""
    now = time.time()
    last = st.session_state.get("_last_query", 0)
    if now - last < seconds:
        return False
    st.session_state["_last_query"] = now
    return True


# resolve_my_union 结果稳定(本盟盟名)，缓存到 session 并在指纹变化时重建，避免重复全表扫
_resolve_my_union = None


def resolve_my_union(conn):
    global _resolve_my_union
    if _resolve_my_union is None:
        row = conn.execute(
            """SELECT attack_union_name FROM battle_report
            WHERE attack_name IN (SELECT name FROM team_user WHERE name != '')
            AND attack_union_name != '' AND attack_union_name != defend_union_name
            GROUP BY attack_union_name ORDER BY COUNT(*) DESC LIMIT 1"""
        ).fetchone()
        _resolve_my_union = row[0] if row else ""
    return _resolve_my_union


def _to_int(v):
    """Turso 值安全转 int：None/NaN/''/非法值一律返回 0"""
    if v is None:
        return 0
    if isinstance(v, float) and v != v:  # NaN
        return 0
    try:
        i = int(v)
    except (TypeError, ValueError):
        return 0
    return i


def _coerce_numeric(df, cols=None):
    """Turso HTTP 返回值全是字符串、NULL 经 pandas 变 NaN；统一转 int 再算分/排序，避免 int(nan) 崩溃"""
    if df is None or not len(df):
        return df
    df = df.copy()
    numeric_cols = cols or ["id", "power", "wu", "contribute_total", "contribute_week",
                            "pos", "join_time", "atk_count", "def_count", "total_bat",
                            "land_count", "last_time"]
    for c in numeric_cols:
        if c in df.columns:
            df[c] = df[c].apply(_to_int)
    return df


@st.cache_data(show_spinner=False)
def latest_data_time():
    """最新战报时间(侧边栏展示)"""
    conn = get_conn()
    row = conn.execute("SELECT MAX(time) FROM battle_report").fetchone()
    v = row[0] if row else None
    if v is None:
        return None
    try:
        i = int(v)
    except (TypeError, ValueError):
        return None
    if isinstance(v, float) and v != v:  # NaN -> None(未知)
        return None
    return i


def week_start(offset=0):
    """指定周周一 0 点时间戳(本地时区)，offset: 0=本周, -1=上周"""
    today = datetime.now().date()
    monday = today - timedelta(days=today.weekday()) + timedelta(weeks=int(offset))
    return int(datetime(monday.year, monday.month, monday.day).timestamp())


@st.cache_data(show_spinner=False)
def query_weekly_activity(week_offset=0):
    """每周活跃度(与桌面 GetWeeklyActivity 一致)：选定周内的参战/翻地/周贡献等原始统计。
    改为服务端一次性 GROUP BY 聚合返回(4 次整表读换 1 次小结果集)，避免逐成员嵌套子查询反复全表扫烧云额度。
    只缓存原始统计，join_days/24h在线/活跃度得分由页面层按当前时间实时计算"""
    conn = get_conn()
    start, end = week_start(week_offset), week_start(week_offset) + 7 * 86400
    my_union = resolve_my_union(conn)
    mu_lit = "''" if not my_union else "'" + str(my_union).replace("'", "''") + "'"
    sql = f"""
    WITH stats AS (
        SELECT name,
               SUM(atk) AS atk_count, SUM(def) AS def_count,
               SUM(land) AS land_count, MAX(last_time) AS last_time
        FROM (
            SELECT attack_name AS name, 1 AS atk, 0 AS def, 0 AS land, time AS last_time
            FROM battle_report WHERE time >= {start} AND time < {end}
            UNION ALL
            SELECT defend_name AS name, 0 AS atk, 1 AS def, 0 AS land, time AS last_time
            FROM battle_report WHERE time >= {start} AND time < {end}
            UNION ALL
            SELECT attack_name AS name, 0 AS atk, 0 AS def, 1 AS land, 0 AS last_time
            FROM battle_report
            WHERE ((battle_desc != '' AND (battle_desc LIKE '%占领了%' OR battle_desc LIKE '%拆除%')
                    AND battle_desc NOT LIKE '%沃土%')
                   OR (battle_desc = '' AND wid_name LIKE '土地%' AND wid_name NOT LIKE '%沃土%'))
              AND defend_union_name != '' AND defend_union_name != {mu_lit}
              AND npc = 0 AND result IN (1,2,3,4,10,18,19)
              AND time >= {start} AND time < {end}
            UNION ALL
            SELECT attack_name AS name, 1 AS atk, 0 AS def, 0 AS land, time AS last_time
            FROM reports WHERE time >= {start} AND time < {end}
        )
        GROUP BY name
    )
    SELECT t.name, t.`group`, t.contribute_week, t.wu, t.power, t.join_time,
           IFNULL(s.atk_count, 0) AS atk_count, IFNULL(s.def_count, 0) AS def_count,
           IFNULL(s.atk_count, 0) + IFNULL(s.def_count, 0) AS total_bat,
           IFNULL(s.land_count, 0) AS land_count, IFNULL(s.last_time, 0) AS last_time
    FROM team_user t
    LEFT JOIN stats s ON s.name = t.name
    WHERE t.name != ''
    ORDER BY t.contribute_week DESC"""
    return _coerce_numeric(pd.read_sql_query(sql, conn))


@st.cache_data(show_spinner=False)
def query_member_activity():
    """赛季总活跃度(与桌面 GetMemberActivity 一致)：武勋/势力/参战/翻地等原始统计。
    服务端一次性 GROUP BY 聚合返回，避免逐成员嵌套子查询反复全表扫烧云额度。
    只缓存原始统计，join_days/24h在线/活跃度得分由页面层按当前时间实时计算"""
    conn = get_conn()
    my_union = resolve_my_union(conn)
    mu_lit = "''" if not my_union else "'" + str(my_union).replace("'", "''") + "'"
    sql = f"""
    WITH stats AS (
        SELECT name,
               SUM(atk) AS atk_count, SUM(def) AS def_count,
               SUM(land) AS land_count, MAX(last_time) AS last_time
        FROM (
            SELECT attack_name AS name, 1 AS atk, 0 AS def, 0 AS land, time AS last_time
            FROM battle_report
            UNION ALL
            SELECT defend_name AS name, 0 AS atk, 1 AS def, 0 AS land, time AS last_time
            FROM battle_report
            UNION ALL
            SELECT attack_name AS name, 0 AS atk, 0 AS def, 1 AS land, 0 AS last_time
            FROM battle_report
            WHERE ((battle_desc != '' AND (battle_desc LIKE '%占领了%' OR battle_desc LIKE '%拆除%')
                    AND battle_desc NOT LIKE '%沃土%')
                   OR (battle_desc = '' AND wid_name LIKE '土地%' AND wid_name NOT LIKE '%沃土%'))
              AND defend_union_name != '' AND defend_union_name != {mu_lit}
              AND npc = 0 AND result IN (1,2,3,4,10,18,19)
            UNION ALL
            SELECT attack_name AS name, 1 AS atk, 0 AS def, 0 AS land, time AS last_time
            FROM reports
        )
        GROUP BY name
    )
    SELECT t.name, t.`group`, t.wu, t.power, t.join_time,
           IFNULL(s.atk_count, 0) AS atk_count, IFNULL(s.def_count, 0) AS def_count,
           IFNULL(s.atk_count, 0) + IFNULL(s.def_count, 0) AS total_bat,
           IFNULL(s.land_count, 0) AS land_count, IFNULL(s.last_time, 0) AS last_time
    FROM team_user t
    LEFT JOIN stats s ON s.name = t.name
    WHERE t.name != ''"""
    return _coerce_numeric(pd.read_sql_query(sql, conn))


def decorate_activity(df):
    """按当前时间实时计算 join_days/24h在线/活跃度得分，公式与桌面 GetMemberActivity 完全一致：
    总场次x0.4 + 武勋/1000x0.3 + (24h内参战? +20) + (加入天数>0? 总场次/加入天数x5)"""
    if df is None or not len(df):
        return df
    df = df.copy()
    now = int(time.time())
    cutoff = now - 86400
    df["join_days"] = df["join_time"].apply(
        lambda j: max(1, (now - _to_int(j)) // 86400) if _to_int(j) > 0 else 0)
    df["active_24h"] = df["last_time"].apply(
        lambda t: 1 if _to_int(t) >= cutoff else 0)

    def score(r):
        s = float(_to_int(r["total_bat"])) * 0.4 + float(_to_int(r["wu"])) / 1000.0 * 0.3
        if r["active_24h"]:
            s += 20
        if r["join_days"] > 0:
            s += float(_to_int(r["total_bat"])) / float(r["join_days"]) * 5
        return round(s, 2)

    df["score"] = df.apply(score, axis=1)
    return df


@st.cache_data(show_spinner=False)
def query_members():
    """同盟成员全量(与桌面 TeamUser 一致)"""
    conn = get_conn()
    sql = ("SELECT id, name, `group`, power, wu, contribute_total, contribute_week, pos, join_time "
           "FROM team_user WHERE name != '' ORDER BY `group`, id")
    return pd.read_sql_query(sql, conn)


@st.cache_data(show_spinner=False)
def query_group_wu():
    """分组武勋(与桌面 GetGroupWu 一致)：分组/人数/总武勋/平均武勋/零武勋人数，按总武勋降序"""
    conn = get_conn()
    sql = """
    SELECT t.`group`, COUNT(*) AS member_count, SUM(t.wu) AS total_wu,
           ROUND(AVG(t.wu)) AS average_wu, IFNULL(s.zero_wu_count, 0) AS zero_wu_count
    FROM team_user t
    LEFT JOIN (SELECT `group`, COUNT(*) AS zero_wu_count FROM team_user WHERE wu = 0 GROUP BY `group`) s
           ON s.`group` = t.`group`
    WHERE t.name != ''
    GROUP BY t.`group`
    ORDER BY total_wu DESC"""
    return pd.read_sql_query(sql, conn)


@st.cache_data(show_spinner=False)
def query_teams(player_kw="", union_kw=""):
    """队伍查询：按人名关键字 + 同盟名关键字过滤(至少一个)，按(玩家,同盟,阵容)取最新队伍"""
    conn = get_conn()
    p_cond_a = "AND attack_name LIKE ?" if player_kw else ""
    p_cond_d = "AND defend_name LIKE ?" if player_kw else ""
    u_cond_a = "AND attack_union_name LIKE ?" if union_kw else ""
    u_cond_d = "AND defend_union_name LIKE ?" if union_kw else ""
    params = [1000]  # 攻分支 attack_hp
    if player_kw:
        params += [f"%{player_kw}%"]  # 攻分支 attack_name
    if union_kw:
        params += [f"%{union_kw}%"]  # 攻分支 attack_union_name
    params += [1000]  # 守分支 defend_hp
    if player_kw:
        params += [f"%{player_kw}%"]  # 守分支 defend_name
    if union_kw:
        params += [f"%{union_kw}%"]  # 守分支 defend_union_name
    sql = f"""
    WITH player_rows AS (
        SELECT attack_name AS player_name, attack_union_name AS union_name,
               attack_hero1_id AS h1, attack_hero2_id AS h2, attack_hero3_id AS h3,
               attack_hero1_level AS l1, attack_hero2_level AS l2, attack_hero3_level AS l3,
               attack_hero1_star AS s1, attack_hero2_star AS s2, attack_hero3_star AS s3,
               attack_total_star AS total_star, attack_hp AS hp, time, all_skill_info,
               attacker_gear_info AS gear_info, 'attack' AS role
        FROM battle_report
        WHERE attack_hero1_id != 0 AND attack_hero2_id != 0 AND attack_hero3_id != 0
          AND attack_hero1_level >= 15 AND attack_hero2_level >= 15 AND attack_hero3_level >= 15
          AND attack_hp >= ? AND npc = 0 AND all_skill_info IS NOT NULL AND all_skill_info != ''
          {p_cond_a} {u_cond_a}
        UNION ALL
        SELECT defend_name, defend_union_name,
               defend_hero1_id, defend_hero2_id, defend_hero3_id,
               defend_hero1_level, defend_hero2_level, defend_hero3_level,
               defend_hero1_star, defend_hero2_star, defend_hero3_star,
               defend_total_star, defend_hp, time, all_skill_info,
               defender_gear_info, 'defend'
        FROM battle_report
        WHERE defend_hero1_id != 0 AND defend_hero2_id != 0 AND defend_hero3_id != 0
          AND defend_hero1_level >= 15 AND defend_hero2_level >= 15 AND defend_hero3_level >= 15
          AND defend_hp >= ? AND npc = 0 AND all_skill_info IS NOT NULL AND all_skill_info != ''
          {p_cond_d} {u_cond_d}
    ),
    latest AS (
        SELECT *, ROW_NUMBER() OVER (PARTITION BY player_name, union_name, h1, h2, h3 ORDER BY time DESC) AS rn
        FROM player_rows
    )
    SELECT l.player_name, l.union_name, l.h1, l.h2, l.h3, l.l1, l.l2, l.l3, l.s1, l.s2, l.s3,
           l.total_star, l.hp, l.time AS last_time, l.all_skill_info, l.gear_info, l.role,
           (SELECT COUNT(*) FROM player_rows m2
            WHERE m2.player_name = l.player_name AND m2.h1 = l.h1 AND m2.h2 = l.h2 AND m2.h3 = l.h3) AS team_count
    FROM latest l WHERE l.rn = 1
    ORDER BY l.union_name, l.player_name"""
    return pd.read_sql_query(sql, conn, params=params)


# ---------- 页面 ----------

st.sidebar.title("🗡️ 同盟数据查询")
with st.sidebar.container():
    if not turso_secret("TURSO_URL") or not turso_secret("TURSO_TOKEN"):
        st.sidebar.warning("未配置云端 secrets(.streamlit/secrets.toml: TURSO_URL/TURSO_TOKEN)，将无法连接")
    # 每次切换界面时轻量检查数据是否有更新：无变动复用缓存，有变动才清缓存重读
    maybe_refresh_cache()
    _latest = safe_query(latest_data_time)
    st.sidebar.caption(f"📅 最新数据: {format_ts(_latest) if _latest is not None else '未知'}")
    st.sidebar.caption("数据源=云端 Turso，数据由应用端同步器(sync.go)推送，网站只读")
page = st.sidebar.radio("功能", ["队伍查询", "同盟成员", "分组武勋", "活跃度分析"], key="page")

def kw_inputs(page_key):
    p = st.text_input("人名关键字", key=f"pkw_{page_key}")
    u = st.text_input("同盟名关键字", key=f"ukw_{page_key}")
    return p, u


def do_query(fn, *args, **kwargs):
    """统一查询入口：3 秒防抖 + 空关键词拦截 + 结果入 session 缓存"""
    if not rate_limited(3):
        st.info("查询过于频繁，请 3 秒后再试(结果已缓存，重复查询不消耗配额)")
        return None
    with st.spinner("查询中..."):
        return safe_query(fn, *args, **kwargs)


def refresh_button():
    """手动刷新(与应用端一致)：清缓存并重跑，让数据重新读取"""
    if st.button("🔄 刷新"):
        st.cache_data.clear()
        st.rerun()


if page == "队伍查询":
    st.title("🗡️ 同盟队伍查询")
    player_kw, union_kw = kw_inputs("t")
    if st.button("查询", type="primary"):
        if not player_kw.strip() and not union_kw.strip():
            st.warning("请输入人名关键字或同盟名关键字后再查询(避免全量扫描)")
            st.session_state.pop("q_df", None)
            st.session_state.pop("q_show", None)
        else:
            df = do_query(query_teams, player_kw.strip(), union_kw.strip())
            if df is not None:
                st.session_state["q_df"] = df
                st.session_state["q_show"] = 20

    if "q_df" in st.session_state and st.session_state["q_df"] is not None and len(st.session_state["q_df"]):
        df = st.session_state["q_df"]
        st.success(f"共 {len(df)} 支队伍")
        show = st.session_state.get("q_show", 20)
        for _, row in df.head(show).iterrows():
            header = (f"**{row['player_name']}** · {row['union_name']} · 红度 {row['total_star']} · "
                      f"兵力 {row['hp']} · 使用 {row['team_count']} 次 · 最近 {format_ts(row['last_time'])}")
            st.markdown(header)
            st.markdown(team_card_html(row), unsafe_allow_html=True)
        if show < len(df):
            if st.button(f"显示更多(剩余 {len(df) - show})"):
                st.session_state["q_show"] = show + 50
        csv = df.copy()
        csv["武将"] = csv.apply(lambda r: " / ".join(hero_name(r[f"h{i}"]) for i in (1, 2, 3)), axis=1)
        csv["战法"] = csv.apply(lambda r: " | ".join(
            " / ".join(f"{s.get('name','')}Lv{s.get('lv','')}" for s in hero)
            for hero in parse_skills(r["all_skill_info"], r["role"])), axis=1)
        csv["宝物"] = csv.apply(lambda r: " | ".join(
            g["name"] + (f"[{g['entry']}]" if g.get("entry") else "") + f"Lv{g.get('lv','')}"
            for g in parse_gears(r["gear_info"], r["role"])), axis=1)
        st.download_button("导出 CSV", csv.to_csv(index=False).encode("utf-8-sig"), "teams.csv")

elif page == "同盟成员":
    st.title("🗡️ 同盟成员")
    refresh_button()
    df = safe_query(query_members)
    if df is not None and len(df):
        st.caption(f"成员数量：{len(df)}")
        disp = df.copy()
        disp["位置"] = disp["pos"].apply(format_pos)
        disp["进盟时间"] = disp["join_time"].apply(format_ts)
        st.dataframe(disp[["id", "name", "group", "power", "wu", "contribute_total", "contribute_week", "位置", "进盟时间"]].rename(
            columns={"id": "ID", "name": "名字", "group": "分组", "power": "势力", "wu": "周武勋",
                     "contribute_total": "总贡献", "contribute_week": "周贡献"}),
            width="stretch", hide_index=True)
        st.download_button("导出 CSV", df.to_csv(index=False).encode("utf-8-sig"), "members.csv")
    elif df is not None:
        st.info("暂无成员数据(需先在应用端同步成员)")

elif page == "分组武勋":
    st.title("🗡️ 分组武勋")
    refresh_button()
    df = safe_query(query_group_wu)
    if df is not None and len(df):
        disp = df[["group", "member_count", "total_wu", "average_wu", "zero_wu_count"]].rename(
            columns={"group": "分组名称", "member_count": "人数", "total_wu": "总武勋",
                     "average_wu": "平均武勋", "zero_wu_count": "0武勋人数"})
        st.dataframe(disp, width="stretch", hide_index=True)
        st.download_button("导出 CSV", df.to_csv(index=False).encode("utf-8-sig"), "group_wu.csv")
    elif df is not None:
        st.info("暂无分组武勋数据")

elif page == "活跃度分析":
    st.title("🗡️ 成员活跃度分析")
    st.caption("基于战报数据评估成员活跃度")
    refresh_button()
    mode = st.radio("面板", ["每周活跃度", "赛季总活跃度"], key="act_mode", horizontal=True)
    df = None
    if mode == "每周活跃度":
        week_opt = st.radio("选择周", ["本周", "上周", "前两周"], key="act_week", horizontal=True)
        week_off = {"本周": 0, "上周": -1, "前两周": -2}[week_opt]
        df = safe_query(query_weekly_activity, week_off)
        if df is not None and len(df):
            df = decorate_activity(df)
            # 与桌面一致：每周模式按周贡献降序，其次活跃度得分
            df = df.sort_values(["contribute_week", "score"], ascending=[False, False]).reset_index(drop=True)
    else:
        df = safe_query(query_member_activity)
        if df is not None and len(df):
            df = decorate_activity(df)
            # 与桌面一致：赛季模式按活跃度得分降序
            df = df.sort_values("score", ascending=False).reset_index(drop=True)

    if df is not None and len(df):
        df["rank"] = range(1, len(df) + 1)
        disp = df.copy()
        disp["最近参战"] = disp["last_time"].apply(lambda v: format_ts(v) or "从未参战")
        disp["24h在线"] = disp["active_24h"].apply(lambda v: "✅ 在线" if v == 1 else "离线")
        disp["活跃等级"] = disp["score"].apply(score_label)
        disp["活跃度"] = disp["score"].round(1)
        c1, c2, c3, c4 = st.columns(4)
        c1.metric("成员总数", len(df))
        c2.metric("24h在线", int(df["active_24h"].sum()))
        c3.metric("核心成员(≥100分)", int((df["score"] >= 100).sum()))
        c4.metric("不活跃(<20分)", int((df["score"] < 20).sum()))
        if mode == "每周活跃度":
            view = disp[["rank", "name", "group", "contribute_week", "atk_count", "def_count",
                         "total_bat", "land_count", "最近参战", "24h在线", "活跃度", "活跃等级"]].rename(
                columns={"rank": "排名", "name": "名称", "group": "分组", "contribute_week": "周贡献",
                         "atk_count": "进攻场次", "def_count": "防守场次", "total_bat": "总场次",
                         "land_count": "翻地次数"})
            st.dataframe(view, width="stretch", hide_index=True)
            st.download_button("导出 CSV", df.to_csv(index=False).encode("utf-8-sig"), "weekly_activity.csv")
        else:
            view = disp[["rank", "name", "group", "wu", "power", "atk_count", "def_count",
                         "total_bat", "land_count", "join_days", "最近参战", "24h在线", "活跃度", "活跃等级"]].rename(
                columns={"rank": "排名", "name": "名称", "group": "分组", "wu": "武勋", "power": "势力",
                         "atk_count": "进攻场次", "def_count": "防守场次", "total_bat": "总场次",
                         "land_count": "翻地次数", "join_days": "加入天数"})
            st.dataframe(view, width="stretch", hide_index=True)
            st.download_button("导出 CSV", df.to_csv(index=False).encode("utf-8-sig"), "season_activity.csv")
    elif df is not None:
        st.info("暂无活跃度数据")