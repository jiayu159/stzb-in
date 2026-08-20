import sqlite3
import os
import json
import re
import urllib.request
from datetime import datetime

import streamlit as st
import pandas as pd

st.set_page_config(page_title="同盟队伍查询", page_icon="🗡️", layout="wide")

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


def get_conn():
    """云端 secrets 直连，本地 data.db 兜底"""
    turso_url = st.secrets.get("TURSO_URL", "")
    turso_token = st.secrets.get("TURSO_TOKEN", "")
    if turso_url and turso_token:
        return TursoConnection(turso_url, turso_token)
    conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    return conn


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


def safe_query(fn, *args, **kwargs):
    """查询兜底：数据库不可用/配额受限/连接异常时返回 None 并提示，不让页面红屏"""
    try:
        return fn(*args, **kwargs)
    except Exception as e:
        st.error(f"数据库查询失败(可能是云端配额受限或连接异常): {e}")
        return None


@st.cache_data(ttl=10, show_spinner=False)
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

st.title("🗡️ 同盟队伍查询")
player_kw = st.text_input("人名关键字(如: 张三 / 龍風)")
union_kw = st.text_input("同盟名关键字(如: 龙 / 風雲)")
if st.button("查询", type="primary"):
    if not player_kw.strip() and not union_kw.strip():
        st.warning("请输入人名关键字或同盟名关键字后再查询(避免全量扫描)")
        st.session_state.pop("q_df", None)
        st.session_state.pop("q_show", None)
    else:
        with st.spinner("查询中..."):
            df = safe_query(query_teams, player_kw.strip(), union_kw.strip())
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