import sqlite3
import os
import json
import re
import time
import urllib.request
from datetime import datetime, timedelta

import streamlit as st
import pandas as pd
from openai import OpenAI

st.set_page_config(page_title="同盟数据查询", page_icon="🗡️", layout="wide")

DB_PATH = os.environ.get("DB_PATH", os.path.join(os.path.dirname(__file__), "data.db"))


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


def get_conn(db_key=None):
    # 优先按 db_key/侧边栏选中的数据库(总表 app_databases)连接，否则 secrets 直连，最后本地文件兜底
    db_key = db_key or st.session_state.get("db_key", "")
    if db_key:
        for d in load_databases():
            if d["name"] == db_key and d.get("url") and d.get("token"):
                return TursoConnection(d["url"], d["token"])
    turso_url = st.secrets.get("TURSO_URL", "")
    turso_token = st.secrets.get("TURSO_TOKEN", "")
    if turso_url and turso_token:
        return TursoConnection(turso_url, turso_token)
    conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    return conn


# ---------- 多数据库总表 ----------

def admin_conn():
    """管理库连接(总表所在库)"""
    url = st.secrets.get("TURSO_URL", "")
    token = st.secrets.get("TURSO_TOKEN", "")
    return TursoConnection(url, token) if (url and token) else None


def ensure_app_databases():
    """在管理库创建总表 app_databases，若为空则自动注册默认库(secrets 自身)"""
    conn = admin_conn()
    if conn is None:
        return
    conn.execute(
        """CREATE TABLE IF NOT EXISTS app_databases (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT UNIQUE NOT NULL,
            url TEXT NOT NULL,
            token TEXT NOT NULL,
            note TEXT DEFAULT '',
            enabled INTEGER DEFAULT 1
        )"""
    )
    n = conn.execute("SELECT COUNT(*) FROM app_databases").fetchone()[0]
    if n == 0:
        conn.execute(
            "INSERT INTO app_databases (name, url, token, note) VALUES (?, ?, ?, ?)",
            ["默认库", st.secrets.get("TURSO_URL", ""), st.secrets.get("TURSO_TOKEN", ""), "Secrets 直连库"],
        )


@st.cache_data(ttl=30, show_spinner=False)
def load_databases():
    """读取总表全部启用数据库(供侧边栏选择与连接)"""
    try:
        conn = admin_conn()
    except Exception:
        return []
    if conn is None:
        return []
    try:
        ensure_app_databases()
        cur = conn.execute(
            "SELECT id, name, url, token, note, enabled FROM app_databases WHERE enabled = 1 ORDER BY id"
        )
        cols = [d[0] for d in cur.description]
        return [dict(zip(cols, r)) for r in cur.fetchall()]
    except Exception:
        return []


def add_database(name, url, token, note=""):
    conn = admin_conn()
    if conn is None:
        return "未配置管理库(TURSO_URL/TURSO_TOKEN)"
    try:
        conn.execute(
            "INSERT INTO app_databases (name, url, token, note) VALUES (?, ?, ?, ?)",
            [name.strip(), url.strip(), token.strip(), note.strip()],
        )
        load_databases.clear()
        return "ok"
    except Exception as e:
        return f"失败: {e}"


def delete_database(name):
    conn = admin_conn()
    if conn is None:
        return "未配置管理库"
    conn.execute("UPDATE app_databases SET enabled = 0 WHERE name = ?", [name])
    load_databases.clear()
    return "ok"


# ---------- 物化缓存(活跃度等重聚合结果落库，避免全表重扫) ----------

def _ensure_cache_table(conn):
    try:
        conn.execute(
            "CREATE TABLE IF NOT EXISTS app_cache (cache_key TEXT PRIMARY KEY, data TEXT NOT NULL, updated_at INTEGER NOT NULL)"
        )
    except Exception:
        pass


def _cache_get(conn, key, ttl):
    """读物化缓存，命中(未过期)返回 DataFrame，否则 None"""
    _ensure_cache_table(conn)
    try:
        cur = conn.execute("SELECT data, updated_at FROM app_cache WHERE cache_key = ?", [key])
        row = cur.fetchone()
    except Exception:
        return None
    if not row:
        return None
    try:
        data = json.loads(row[0])
        age = int(time.time()) - int(row[1])
    except Exception:
        return None
    if age >= 0 and age < ttl:
        return pd.DataFrame(data) if data else pd.DataFrame()
    return None


def _cache_set(conn, key, df):
    """把聚合结果写入物化缓存(写一次小数据，换取下次免全表扫描)"""
    try:
        conn.execute(
            "INSERT INTO app_cache (cache_key, data, updated_at) VALUES (?, ?, ?) "
            "ON CONFLICT(cache_key) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at",
            [key, json.dumps(df.to_dict("records"), ensure_ascii=False), int(time.time())],
        )
    except Exception:
        pass


def safe_query(fn, *args, **kwargs):
    """查询兜底：数据库不可用/配额受限/连接异常时返回 None 并提示，不让页面红屏"""
    try:
        return fn(*args, **kwargs)
    except Exception as e:
        st.error(f"数据库查询失败(可能是云端配额受限或连接异常): {e}")
        return None


def resolve_my_union(conn):
    row = conn.execute(
        """SELECT attack_union_name FROM battle_report
        WHERE attack_name IN (SELECT name FROM team_user WHERE name != '')
        AND attack_union_name != '' AND attack_union_name != defend_union_name
        GROUP BY attack_union_name ORDER BY COUNT(*) DESC LIMIT 1"""
    ).fetchone()
    return row[0] if row else ""


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

@st.cache_data(ttl=10, show_spinner=False)
def query_member_teams(min_hp=0, name="", db=None):
    """同盟成员常用队伍：默认每名成员最新一个队伍；搜索具体玩家时按阵容分组显示其全部队伍"""
    conn = get_conn(db)
    name_cond = "AND attack_name LIKE ?" if name else ""
    name_cond2 = "AND defend_name LIKE ?" if name else ""
    if name:
        params = [f"%{name}%", min_hp, f"%{name}%", min_hp]
    else:
        params = [min_hp, min_hp]
    part_by = "PARTITION BY player_name, h1, h2, h3" if name else "PARTITION BY player_name"
    order_by = "team_count DESC, l.player_name" if name else "l.player_name"
    sql = f"""
    WITH member_rows AS (
        SELECT attack_name AS player_name, attack_hero1_id AS h1, attack_hero2_id AS h2, attack_hero3_id AS h3,
               attack_hero1_level AS l1, attack_hero2_level AS l2, attack_hero3_level AS l3,
               attack_hero1_star AS s1, attack_hero2_star AS s2, attack_hero3_star AS s3,
               attack_total_star AS total_star, attack_hp AS hp, time, all_skill_info,
               attacker_gear_info AS gear_info, 'attack' AS role
        FROM battle_report
        WHERE attack_name IN (SELECT name FROM team_user WHERE name != '')
          {name_cond}
          AND attack_hero1_id != 0 AND attack_hero2_id != 0 AND attack_hero3_id != 0
          AND attack_hero1_level >= 15 AND attack_hero2_level >= 15 AND attack_hero3_level >= 15
          AND attack_hp >= ? AND npc = 0 AND all_skill_info IS NOT NULL AND all_skill_info != ''
        UNION ALL
        SELECT defend_name, defend_hero1_id, defend_hero2_id, defend_hero3_id,
               defend_hero1_level, defend_hero2_level, defend_hero3_level,
               defend_hero1_star, defend_hero2_star, defend_hero3_star,
               defend_total_star, defend_hp, time, all_skill_info,
               defender_gear_info, 'defend'
        FROM battle_report
        WHERE defend_name IN (SELECT name FROM team_user WHERE name != '')
          {name_cond2}
          AND defend_hero1_id != 0 AND defend_hero2_id != 0 AND defend_hero3_id != 0
          AND defend_hero1_level >= 15 AND defend_hero2_level >= 15 AND defend_hero3_level >= 15
          AND defend_hp >= ? AND npc = 0 AND all_skill_info IS NOT NULL AND all_skill_info != ''
    ),
    latest AS (
        SELECT *, ROW_NUMBER() OVER ({part_by} ORDER BY time DESC) AS rn
        FROM member_rows
    )
    SELECT l.player_name, l.h1, l.h2, l.h3, l.l1, l.l2, l.l3, l.s1, l.s2, l.s3,
           l.total_star, l.hp, l.time AS last_time,
           l.all_skill_info, l.gear_info, l.role,
           (SELECT COUNT(*) FROM member_rows m2
            WHERE m2.player_name = l.player_name AND m2.h1 = l.h1 AND m2.h2 = l.h2 AND m2.h3 = l.h3) AS team_count
    FROM latest l WHERE l.rn = 1 ORDER BY {order_by}"""
    return pd.read_sql_query(sql, conn, params=params)


@st.cache_data(ttl=10, show_spinner=False)
def query_enemy_teams(min_hp=0, name="", db=None):
    """交战过的非己方同盟人员队伍(含胜负、过滤无归属)，按交战次数递减"""
    conn = get_conn(db)
    my_union = resolve_my_union(conn)
    name_cond = "AND defend_name LIKE ?" if name else ""
    name_cond2 = "AND attack_name LIKE ?" if name else ""
    params = [my_union]
    if name:
        params += [f"%{name}%"]
    params += [min_hp, my_union]
    if name:
        params += [f"%{name}%"]
    params += [min_hp]
    part_by = "PARTITION BY player_name, h1, h2, h3" if name else "PARTITION BY player_name"
    order_by = "l.player_name, encounter_count DESC" if name else "encounter_count DESC"
    sql = f"""
    WITH enemy_encounters AS (
        SELECT defend_name AS player_name, defend_hero1_id AS h1, defend_hero2_id AS h2, defend_hero3_id AS h3,
               defend_hero1_level AS l1, defend_hero2_level AS l2, defend_hero3_level AS l3,
               defend_hero1_star AS s1, defend_hero2_star AS s2, defend_hero3_star AS s3,
               defend_total_star AS total_star, defend_hp AS hp, time, all_skill_info,
               defender_gear_info AS gear_info, 'defend' AS role
        FROM battle_report
        WHERE attack_union_name = ?
          AND defend_name NOT IN (SELECT name FROM team_user WHERE name != '')
          AND defend_union_name != ''
          {name_cond}
          AND defend_hero1_id != 0 AND defend_hero2_id != 0 AND defend_hero3_id != 0
          AND defend_hp >= ? AND npc = 0 AND all_skill_info IS NOT NULL AND all_skill_info != ''
        UNION ALL
        SELECT attack_name, attack_hero1_id, attack_hero2_id, attack_hero3_id,
               attack_hero1_level, attack_hero2_level, attack_hero3_level,
               attack_hero1_star, attack_hero2_star, attack_hero3_star,
               attack_total_star, attack_hp, time, all_skill_info,
               attacker_gear_info, 'attack'
        FROM battle_report
        WHERE defend_union_name = ?
          AND attack_name NOT IN (SELECT name FROM team_user WHERE name != '')
          AND attack_union_name != ''
          {name_cond2}
          AND attack_hero1_id != 0 AND attack_hero2_id != 0 AND attack_hero3_id != 0
          AND attack_hp >= ? AND npc = 0 AND all_skill_info IS NOT NULL AND all_skill_info != ''
    ),
    latest AS (
        SELECT *, ROW_NUMBER() OVER ({part_by} ORDER BY time DESC) AS rn
        FROM enemy_encounters
    )
    SELECT l.player_name, l.h1, l.h2, l.h3, l.l1, l.l2, l.l3, l.s1, l.s2, l.s3,
           l.total_star, l.hp, l.time AS last_time,
           l.all_skill_info, l.gear_info, l.role,
           (SELECT COUNT(*) FROM enemy_encounters e2
            WHERE e2.player_name = l.player_name AND e2.h1 = l.h1 AND e2.h2 = l.h2 AND e2.h3 = l.h3) AS encounter_count
    FROM latest l WHERE l.rn = 1
    ORDER BY {order_by}"""
    return pd.read_sql_query(sql, conn, params=params)


def week_start(offset=0):
    """指定周周一 0 点时间戳(本地时区)，offset: 0=本周, -1=上周"""
    today = datetime.now().date()
    monday = today - timedelta(days=today.weekday()) + timedelta(weeks=int(offset))
    return int(datetime(monday.year, monday.month, monday.day).timestamp())


@st.cache_data(ttl=600, show_spinner=False)
def query_weekly_activity(week_offset=0, db=None):
    """每周活跃度: 指定周(周一0点起7天)的参战/翻地/周贡献(物化缓存 10 分钟)"""
    conn = get_conn(db)
    cached = _cache_get(conn, f"weekly_{week_offset}", 600)
    if cached is not None:
        return cached
    start, end = week_start(week_offset), week_start(week_offset) + 7 * 86400
    my_union = resolve_my_union(conn)
    sql = """
    SELECT t.name, t.`group`, t.contribute_week,
           (SELECT COUNT(*) FROM battle_report b WHERE b.attack_name = t.name AND b.time >= ? AND b.time < ?) +
           (SELECT COUNT(*) FROM reports r WHERE r.attack_name = t.name AND r.time >= ? AND r.time < ?) AS atk_count,
           (SELECT COUNT(*) FROM battle_report b WHERE b.defend_name = t.name AND b.time >= ? AND b.time < ?) AS def_count,
           (SELECT COUNT(*) FROM battle_report b
            WHERE ((b.battle_desc != '' AND (b.battle_desc LIKE '%占领了%' OR b.battle_desc LIKE '%拆除%')
                    AND b.battle_desc NOT LIKE '%沃土%')
                   OR (b.battle_desc = '' AND b.wid_name LIKE '土地%' AND b.wid_name NOT LIKE '%沃土%'))
              AND b.attack_name = t.name AND b.defend_union_name != '' AND b.defend_union_name != ?
              AND b.npc = 0 AND b.result IN (1,2,3,4,10,18,19)
              AND b.time >= ? AND b.time < ?) AS land_count,
           (SELECT MAX(time) FROM battle_report b
            WHERE (b.attack_name = t.name OR b.defend_name = t.name) AND b.time >= ? AND b.time < ?) AS last_time
    FROM team_user t WHERE t.name != ''
    ORDER BY t.contribute_week DESC"""
    params = [start, end, start, end, start, end, my_union, start, end, start, end]
    df = pd.read_sql_query(sql, conn, params=params)
    _cache_set(conn, f"weekly_{week_offset}", df)
    return df


@st.cache_data(ttl=3600, show_spinner=False)
def query_member_activity(db=None):
    """成员活跃度 + 翻地次数(物化缓存 1 小时)"""
    conn = get_conn(db)
    cached = _cache_get(conn, "season_activity", 3600)
    if cached is not None:
        return cached
    my_union = resolve_my_union(conn)
    sql = """
    SELECT t.name, t.`group`, t.wu, t.power,
           (SELECT COUNT(*) FROM battle_report b WHERE b.attack_name = t.name) +
           (SELECT COUNT(*) FROM reports r WHERE r.attack_name = t.name) AS atk_count,
           (SELECT COUNT(*) FROM battle_report b WHERE b.defend_name = t.name) AS def_count,
           (SELECT COUNT(*) FROM battle_report b
            WHERE ((b.battle_desc != '' AND (b.battle_desc LIKE '%占领了%' OR b.battle_desc LIKE '%拆除%')
                    AND b.battle_desc NOT LIKE '%沃土%')
                   OR (b.battle_desc = '' AND b.wid_name LIKE '土地%' AND b.wid_name NOT LIKE '%沃土%'))
              AND b.attack_name = t.name AND b.defend_union_name != '' AND b.defend_union_name != ?
              AND b.npc = 0 AND b.result IN (1,2,3,4,10,18,19)) AS land_count,
           (SELECT MAX(MAX(b.time), (SELECT MAX(time) FROM reports r WHERE r.attack_name = t.name))
            FROM battle_report b WHERE b.attack_name = t.name OR b.defend_name = t.name) AS last_time
    FROM team_user t WHERE t.name != ''
    ORDER BY t.wu DESC"""
    df = pd.read_sql_query(sql, conn, params=(my_union,))
    _cache_set(conn, "season_activity", df)
    return df


@st.cache_data(ttl=10, show_spinner=False)
def query_battle_reports(name="", min_hp=0, limit=500, db=None):
    conn = get_conn(db)
    cond, params = [], []
    if name:
        cond.append("(attack_name LIKE ? OR defend_name LIKE ? OR wid_name LIKE ?)")
        params += [f"%{name}%"] * 3
    if min_hp:
        cond.append("attack_hp >= ?")
        params.append(min_hp)
    where = " AND " + " AND ".join(cond) if cond else ""
    sql = f"""SELECT time, wid_name, attack_name, attack_union_name, defend_name, defend_union_name,
                     attack_hp, defend_hp, garrison, result, attack_hero1_id, attack_hero2_id, attack_hero3_id,
                     defend_hero1_id, defend_hero2_id, defend_hero3_id
              FROM battle_report WHERE 1=1{where}
              ORDER BY time DESC LIMIT ?"""
    return pd.read_sql_query(sql, conn, params=params + [limit])


# ---------- AI 小秘书 ----------

AI_SCHEMA = """数据库为 SQLite 只读库，共 3 张表：
1. team_user 同盟成员: name, group(分组,查询需用反引号转义), contribute_total, contribute_week, pos, power(势力), wu(武勋), join_time
2. battle_report 战报: time, wid_name(战斗地点), attack_name, attack_union_name, defend_name, defend_union_name,
   npc, result(0=攻方败 1=攻方胜 2=平局), attack_hp, defend_hp,
   attack_hero1_id/2/3_id(队伍武将ID), attack_hero*_level, attack_hero*_star, attack_total_star, all_skill_info(战法),
   defend_hero1_id/2/3_id, defend_*_level, defend_*_star, defend_total_star, garrison(0=主力 1=拆迁), battle_desc(翻地描述)
3. task 攻城任务: id, name, target_user_num, complete_user_num, status, target
规则：可以自由用 SELECT/WHERE/LIKE/IN/GROUP BY/ORDER BY/LIMIT/CASE WHEN/聚合函数/strftime('%m-%d %H:%M', time, 'unixepoch', 'localtime')。
角色名用 LIKE 模糊匹配；用户问队伍/阵容指 battle_report 中玩家的武将和战法，与 team_user.group 无关。
只允许 SELECT 开头的只读查询。"""


def ai_chat(message, db=None):
    api_key = st.secrets.get("AI_API_KEY", "")
    if not api_key:
        return "未配置 AI_API_KEY（Streamlit 的 Secrets 中设置）"
    client = OpenAI(
        base_url=st.secrets.get("AI_BASE_URL", "https://ai.gitee.com/v1"),
        api_key=api_key,
    )
    model = st.secrets.get("AI_MODEL", "Qwen3-8B")

    # 阶段1：生成 SQL
    plan = client.chat.completions.create(
        model=model,
        messages=[{"role": "user", "content": "你是查询规划器，根据用户问题写一条 SQLite SELECT 查询。只输出 {\"sql\": \"...\"}。\n\n" + AI_SCHEMA + "\n\n用户问题: " + message}],
    ).choices[0].message.content
    m = re.search(r'"sql"\s*:\s*"((?:[^"\\]|\\.)*)"', plan, re.S)
    if not m:
        return "AI 未产出有效 SQL: " + plan[:200]
    sql = m.group(1).replace('\\"', '"').replace('\\\\', '\\')
    if not sql.strip().upper().startswith("SELECT"):
        return "AI 生成的不是查询语句，已拒绝"

    # 阶段2：本地执行
    try:
        conn = get_conn(db)
        df = pd.read_sql_query(sql, conn)
    except Exception as e:
        return "查询执行失败: " + str(e)
    result_text = df.to_csv(sep="|", index=False) if len(df) else ""

    # 阶段3：生成回答
    if not result_text:
        system = "你是「妲己小秘书」。本地查询无数据，请如实回答，不要编造。"
    else:
        system = ("你是「妲己小秘书」。程序已在本地数据库执行查询，以下是结果(TSV，首行为列名)。请用地道中文口语回答，"
                  "引用具体数字，把列名翻译成中文(power=势力, wu=武勋, wid_name=地点, attack_name=进攻方, defend_name=防守方, "
                  "attack_hp=兵力, garrison=0主力1拆迁, npc=1=与NPC战斗, result=0攻败1攻胜2平局)，绝不编造。\n\n查询结果:\n" + result_text)
    reply = client.chat.completions.create(
        model=model,
        messages=[
            {"role": "system", "content": system},
            {"role": "user", "content": message},
        ],
    ).choices[0].message.content
    return reply


# ---------- 页面 ----------

st.sidebar.title("🗡️ 同盟数据查询")
page = st.sidebar.radio("功能", ["战报查询", "同盟成员常用队伍", "敌军队伍", "成员活跃度", "AI 小秘书"])

# 数据库选择(总表 app_databases)：进入时选择使用的库，全局生效
_db_list = load_databases()
_db_names = [d["name"] for d in _db_list]
_cur_db = st.session_state.get("db_key", "")
if _db_names:
    _idx = _db_names.index(_cur_db) if _cur_db in _db_names else 0
    _sel = st.sidebar.selectbox("选择数据库", _db_names, index=_idx, key="db_select")
    if _sel != st.session_state.get("db_key"):
        st.cache_data.clear()
        st.session_state["db_key"] = _sel
    _cur_db = _sel
st.session_state.setdefault("db_key", _cur_db)

with st.sidebar.expander("数据库管理"):
    with st.form("add_db_form", clear_on_submit=True):
        _n = st.text_input("名称", key="adn")
        _u = st.text_input("URL(https://xxx.turso.io)", key="adu")
        _t = st.text_input("Token", type="password", key="adt")
        _note = st.text_input("备注", key="adno")
        if st.form_submit_button("添加数据库"):
            if _n and _u and _t:
                st.sidebar.write(add_database(_n, _u, _t, _note))
            else:
                st.sidebar.write("名称/URL/Token 不能为空")
    if _db_names:
        _del = st.sidebar.selectbox("删除(禁用)数据库", _db_names, key="db_del")
        if st.sidebar.button("删除选中"):
            st.sidebar.write(delete_database(_del))

using_turso = bool(st.secrets.get("TURSO_URL", "") and st.secrets.get("TURSO_TOKEN", ""))
if not using_turso and not os.path.exists(DB_PATH):
    st.error(f"未找到数据库文件: {DB_PATH}。请把 .db 文件放到仓库根目录并命名为 data.db，"
             f"或在 Secrets 中配置 TURSO_URL/TURSO_TOKEN 直连云端实时库。")
    st.stop()

if using_turso:
    st.sidebar.caption("🌐 云端实时库(Turso)")

if page == "战报查询":
    st.header("战报查询")
    name = st.text_input("玩家名/地点关键词")
    min_hp = st.number_input("兵力下限", 0, 99999, 0, step=1000)
    if st.button("查询", type="primary"):
        if not name.strip():
            st.warning("请输入玩家名/地点关键词后再查询(避免全量扫描)")
            st.stop()
        with st.spinner("查询中..."):
            df = safe_query(query_battle_reports, name, int(min_hp), db=_cur_db)
        if df is None:
            st.stop()
        st.success(f"共 {len(df)} 条")
        df_disp = df.copy()
        df_disp["进攻阵容"] = df_disp.apply(lambda r: " / ".join(hero_name(r[f"attack_hero{i}_id"]) for i in (1, 2, 3)), axis=1)
        df_disp["防守阵容"] = df_disp.apply(lambda r: " / ".join(hero_name(r[f"defend_hero{i}_id"]) for i in (1, 2, 3)), axis=1)
        df_disp["最近时间"] = df_disp["time"].apply(format_ts)
        st.dataframe(df_disp[["最近时间", "wid_name", "attack_name", "attack_union_name", "defend_name",
                              "defend_union_name", "attack_hp", "defend_hp", "garrison", "result",
                              "进攻阵容", "防守阵容"]].rename(
            columns={"wid_name": "地点", "attack_name": "进攻方", "attack_union_name": "进攻方同盟",
                     "defend_name": "防守方", "defend_union_name": "防守方同盟", "attack_hp": "攻方兵力",
                     "defend_hp": "守方兵力", "garrison": "类型", "result": "结果"}),
            use_container_width=True, hide_index=True)
        csv = df.copy()
        csv["进攻阵容"] = df_disp["进攻阵容"]
        csv["防守阵容"] = df_disp["防守阵容"]
        st.download_button("导出 CSV", csv.to_csv(index=False).encode("utf-8-sig"), "battle_reports.csv")

elif page == "同盟成员常用队伍":
    st.header("同盟成员常用队伍")
    name = st.text_input("成员名关键词", key="mn")
    min_hp = st.number_input("兵力下限", 0, 99999, 0, step=1000, key="m")
    if st.button("查询", type="primary"):
        if not name.strip():
            st.warning("请输入成员名关键词后再查询(避免全量扫描)")
            st.session_state.pop("mt_df", None)
            st.session_state.pop("mt_show", None)
        else:
            with st.spinner("查询中..."):
                df = safe_query(query_member_teams, int(min_hp), name.strip(), db=_cur_db)
            if df is None:
                st.session_state.pop("mt_df", None)
                st.session_state.pop("mt_show", None)
            else:
                st.session_state["mt_df"] = df
                st.session_state["mt_show"] = 20
    if "mt_df" in st.session_state and len(st.session_state["mt_df"]):
        df = st.session_state["mt_df"]
        st.success(f"共 {len(df)} 名成员")
        show = st.session_state.get("mt_show", 20)
        for _, row in df.head(show).iterrows():
            header = f"**{row['player_name']}** · 红度 {row['total_star']} · 兵力 {row['hp']} · 使用 {row['team_count']} 次 · 最近 {format_ts(row['last_time'])}"
            st.markdown(header)
            st.markdown(team_card_html(row), unsafe_allow_html=True)
        if show < len(df):
            if st.button(f"显示更多(剩余 {len(df) - show})"):
                st.session_state["mt_show"] = show + 50
        csv = df.copy()
        csv["武将"] = csv.apply(lambda r: " / ".join(hero_name(r[f"h{i}"]) for i in (1, 2, 3)), axis=1)
        csv["战法"] = csv.apply(lambda r: " | ".join(
            " / ".join(f"{s.get('name','')}Lv{s.get('lv','')}" for s in hero)
            for hero in parse_skills(r["all_skill_info"], r["role"])), axis=1)
        csv["宝物"] = csv.apply(lambda r: " | ".join(
            g["name"] + (f"[{g['entry']}]" if g.get("entry") else "") + f"Lv{g.get('lv','')}"
            for g in parse_gears(r["gear_info"], r["role"])), axis=1)
        st.download_button("导出 CSV", csv.to_csv(index=False).encode("utf-8-sig"), "member_teams.csv")

elif page == "敌军队伍":
    st.header("敌军队伍")
    st.caption("与本盟交战过的非己方同盟人员(含胜负)，已过滤无同盟归属")
    name = st.text_input("玩家名关键词", key="en")
    min_hp = st.number_input("兵力下限", 0, 99999, 0, step=1000, key="e")
    if st.button("查询", type="primary"):
        if not name.strip():
            st.warning("请输入玩家名关键词后再查询(避免全量扫描)")
            st.session_state.pop("et_df", None)
            st.session_state.pop("et_show", None)
        else:
            with st.spinner("查询中..."):
                df = safe_query(query_enemy_teams, int(min_hp), name.strip(), db=_cur_db)
            if df is None:
                st.session_state.pop("et_df", None)
                st.session_state.pop("et_show", None)
            else:
                st.session_state["et_df"] = df
                st.session_state["et_show"] = 20
    if "et_df" in st.session_state and len(st.session_state["et_df"]):
        df = st.session_state["et_df"]
        st.success(f"共 {len(df)} 支队伍")
        show = st.session_state.get("et_show", 20)
        for _, row in df.head(show).iterrows():
            header = f"**{row['player_name']}** · 红度 {row['total_star']} · 兵力 {row['hp']} · 交战 {row['encounter_count']} 次 · 最近 {format_ts(row['last_time'])}"
            st.markdown(header)
            st.markdown(team_card_html(row), unsafe_allow_html=True)
        if show < len(df):
            if st.button(f"显示更多(剩余 {len(df) - show})"):
                st.session_state["et_show"] = show + 50
        csv = df.copy()
        csv["武将"] = csv.apply(lambda r: " / ".join(hero_name(r[f"h{i}"]) for i in (1, 2, 3)), axis=1)
        csv["战法"] = csv.apply(lambda r: " | ".join(
            " / ".join(f"{s.get('name','')}Lv{s.get('lv','')}" for s in hero)
            for hero in parse_skills(r["all_skill_info"], r["role"])), axis=1)
        csv["宝物"] = csv.apply(lambda r: " | ".join(
            g["name"] + (f"[{g['entry']}]" if g.get("entry") else "") + f"Lv{g.get('lv','')}"
            for g in parse_gears(r["gear_info"], r["role"])), axis=1)
        st.download_button("导出 CSV", csv.to_csv(index=False).encode("utf-8-sig"), "enemy_teams.csv")

elif page == "成员活跃度":
    st.header("成员活跃度")
    mode = st.radio("面板", ["每周活跃度", "赛季总活跃度"], key="act_mode", horizontal=True)
    if mode == "每周活跃度":
        week_opt = st.radio("选择周", ["本周", "上周", "前两周"], key="act_week", horizontal=True)
        week_off = {"本周": 0, "上周": -1, "前两周": -2}[week_opt]
        with st.spinner("查询中..."):
            df = safe_query(query_weekly_activity, week_off, db=_cur_db)
        if df is None:
            st.stop()
        if len(df):
            st.success(f"共 {len(df)} 名成员")
            disp = df.copy()
            disp["最近参战"] = disp["last_time"].apply(format_ts)
            st.dataframe(disp[["name", "group", "contribute_week", "atk_count", "def_count", "land_count", "最近参战"]].rename(
                columns={"name": "成员", "group": "分组", "contribute_week": "周贡献",
                         "atk_count": "进攻场次", "def_count": "防守场次", "land_count": "翻地次数"}),
                use_container_width=True, hide_index=True)
            st.download_button("导出 CSV", df.to_csv(index=False).encode("utf-8-sig"), f"weekly_activity_w{week_off}.csv")
    else:
        with st.spinner("查询中..."):
            df = safe_query(query_member_activity, db=_cur_db)
        if df is None:
            st.stop()
        if len(df):
            st.success(f"共 {len(df)} 名成员")
            disp = df.copy()
            disp["最近参战"] = disp["last_time"].apply(format_ts)
            st.dataframe(disp[["name", "group", "wu", "power", "atk_count", "def_count", "land_count", "最近参战"]].rename(
                columns={"name": "成员", "group": "分组", "wu": "武勋", "power": "势力",
                         "atk_count": "进攻场次", "def_count": "防守场次", "land_count": "翻地次数"}),
                use_container_width=True, hide_index=True)
            st.download_button("导出 CSV", df.to_csv(index=False).encode("utf-8-sig"), "season_activity.csv")

elif page == "AI 小秘书":
    st.header("🤖 AI 小秘书")
    # 注意: 不使用 st.chat_input/st.chat_message(存在 removeChild DOM 竞态 bug)，改用普通输入框
    if "ai_history" not in st.session_state:
        st.session_state.ai_history = []
    with st.form("ai_form", clear_on_submit=True):
        q = st.text_input("问点什么?例如:谁的武勋最高?张三的队伍配置?", key="ai_q")
        sent = st.form_submit_button("发送", type="primary")
    if sent and q.strip():
        st.session_state.ai_history.append(("user", q.strip()))
        with st.spinner("思考中..."):
            answer = ai_chat(q.strip(), db=_cur_db)
        st.session_state.ai_history.append(("assistant", answer))

    for role, content in st.session_state.ai_history[-20:]:
        if role == "user":
            st.markdown(f'<div style="text-align:right;background:#e8f0fe;border-radius:10px;'
                        f'padding:8px 12px;margin:4px 0">{content}</div>', unsafe_allow_html=True)
        else:
            st.markdown(f'<div style="background:#f6f6f6;border-radius:10px;'
                        f'padding:8px 12px;margin:4px 0">{content}</div>', unsafe_allow_html=True)