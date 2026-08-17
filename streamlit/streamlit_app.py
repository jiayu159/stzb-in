import sqlite3
import os
import json
import re
import urllib.request
from datetime import datetime

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


def get_conn():
    # 优先云端 Turso 实时库(secrets 配置 TURSO_URL/TURSO_TOKEN)，否则本地文件
    turso_url = st.secrets.get("TURSO_URL", "")
    turso_token = st.secrets.get("TURSO_TOKEN", "")
    if turso_url and turso_token:
        return TursoConnection(turso_url, turso_token)
    conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    return conn


def resolve_my_union(conn):
    row = conn.execute(
        """SELECT attack_union_name FROM battle_report
        WHERE attack_name IN (SELECT name FROM team_user WHERE name != '')
        AND attack_union_name != '' AND attack_union_name != defend_union_name
        GROUP BY attack_union_name ORDER BY COUNT(*) DESC LIMIT 1"""
    ).fetchone()
    return row[0] if row else ""


# ---------- 查询函数 ----------

@st.cache_data(ttl=10, show_spinner=False)
def query_member_teams(min_hp=0, name=""):
    """同盟成员常用队伍：每名成员出现次数最多的一个队伍"""
    conn = get_conn()
    name_cond = "AND attack_name LIKE ?" if name else ""
    name_cond2 = "AND defend_name LIKE ?" if name else ""
    if name:
        params = [f"%{name}%", min_hp, f"%{name}%", min_hp]
    else:
        params = [min_hp, min_hp]
    sql = f"""
    WITH member_rows AS (
        SELECT attack_name AS player_name, attack_hero1_id AS h1, attack_hero2_id AS h2, attack_hero3_id AS h3,
               attack_hero1_level AS l1, attack_hero2_level AS l2, attack_hero3_level AS l3,
               attack_hero1_star AS s1, attack_hero2_star AS s2, attack_hero3_star AS s3,
               attack_total_star AS total_star, attack_hp AS hp, time, all_skill_info
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
               defend_total_star, defend_hp, time, all_skill_info
        FROM battle_report
        WHERE defend_name IN (SELECT name FROM team_user WHERE name != '')
          {name_cond2}
          AND defend_hero1_id != 0 AND defend_hero2_id != 0 AND defend_hero3_id != 0
          AND defend_hero1_level >= 15 AND defend_hero2_level >= 15 AND defend_hero3_level >= 15
          AND defend_hp >= ? AND npc = 0 AND all_skill_info IS NOT NULL AND all_skill_info != ''
    ),
    team_counts AS (
        SELECT player_name, h1, h2, h3, COUNT(*) AS team_count,
               MAX(total_star) AS total_star, MAX(hp) AS hp, MAX(time) AS last_time,
               MAX(all_skill_info) AS all_skill_info
        FROM member_rows GROUP BY player_name, h1, h2, h3
    ),
    ranked AS (
        SELECT *, ROW_NUMBER() OVER (PARTITION BY player_name ORDER BY team_count DESC, last_time DESC) AS rn
        FROM team_counts
    )
    SELECT player_name, h1, h2, h3, total_star, hp, team_count, last_time, all_skill_info
    FROM ranked WHERE rn = 1 ORDER BY player_name"""
    return pd.read_sql_query(sql, conn, params=params)


@st.cache_data(ttl=10, show_spinner=False)
def query_enemy_teams(min_hp=0, name=""):
    """交战过的非己方同盟人员队伍(含胜负、过滤无归属)，按交战次数递减"""
    conn = get_conn()
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
    sql = f"""
    WITH enemy_encounters AS (
        SELECT defend_name AS player_name, defend_hero1_id AS h1, defend_hero2_id AS h2, defend_hero3_id AS h3,
               defend_total_star AS total_star, defend_hp AS hp, time, all_skill_info
        FROM battle_report
        WHERE attack_union_name = ?
          AND defend_name NOT IN (SELECT name FROM team_user WHERE name != '')
          AND defend_union_name != ''
          {name_cond}
          AND defend_hero1_id != 0 AND defend_hero2_id != 0 AND defend_hero3_id != 0
          AND defend_hp >= ? AND npc = 0 AND all_skill_info IS NOT NULL AND all_skill_info != ''
        UNION ALL
        SELECT attack_name, attack_hero1_id, attack_hero2_id, attack_hero3_id,
               attack_total_star, attack_hp, time, all_skill_info
        FROM battle_report
        WHERE defend_union_name = ?
          AND attack_name NOT IN (SELECT name FROM team_user WHERE name != '')
          AND attack_union_name != ''
          {name_cond2}
          AND attack_hero1_id != 0 AND attack_hero2_id != 0 AND attack_hero3_id != 0
          AND attack_hp >= ? AND npc = 0 AND all_skill_info IS NOT NULL AND all_skill_info != ''
    )
    SELECT player_name, h1, h2, h3, total_star, hp, COUNT(*) AS encounter_count, MAX(time) AS last_time,
           MAX(all_skill_info) AS all_skill_info
    FROM enemy_encounters GROUP BY player_name, h1, h2, h3
    ORDER BY encounter_count DESC"""
    return pd.read_sql_query(sql, conn, params=params)


@st.cache_data(ttl=10, show_spinner=False)
def query_member_activity():
    """成员活跃度 + 翻地次数"""
    conn = get_conn()
    my_union = resolve_my_union(conn)
    sql = """
    SELECT t.name, t.group, t.wu, t.power,
           (SELECT COUNT(*) FROM battle_report b WHERE b.attack_name = t.name) +
           (SELECT COUNT(*) FROM report r WHERE r.attack_name = t.name) AS atk_count,
           (SELECT COUNT(*) FROM battle_report b WHERE b.defend_name = t.name) AS def_count,
           (SELECT COUNT(*) FROM battle_report b
            WHERE ((b.battle_desc != '' AND (b.battle_desc LIKE '%占领了%' OR b.battle_desc LIKE '%拆除%')
                    AND b.battle_desc NOT LIKE '%沃土%')
                   OR (b.battle_desc = '' AND b.wid_name LIKE '土地%' AND b.wid_name NOT LIKE '%沃土%'))
              AND b.attack_name = t.name AND b.defend_union_name != '' AND b.defend_union_name != ?
              AND b.npc = 0 AND b.result IN (1,2,3,4,10,18,19)) AS land_count,
           (SELECT MAX(MAX(b.time), (SELECT MAX(time) FROM report r WHERE r.attack_name = t.name))
            FROM battle_report b WHERE b.attack_name = t.name OR b.defend_name = t.name) AS last_time
    FROM team_user t WHERE t.name != ''
    ORDER BY t.wu DESC"""
    return pd.read_sql_query(sql, conn, params=(my_union,))


@st.cache_data(ttl=10, show_spinner=False)
def query_battle_reports(name="", min_hp=0, limit=500):
    conn = get_conn()
    cond, params = [], []
    if name:
        cond.append("(attack_name LIKE ? OR defend_name LIKE ? OR wid_name LIKE ?)")
        params += [f"%{name}%"] * 3
    if min_hp:
        cond.append("attack_hp >= ?")
        params.append(min_hp)
    where = " AND " + " AND ".join(cond) if cond else ""
    sql = f"""SELECT time, wid_name, attack_name, attack_union_name, defend_name, defend_union_name,
                     attack_hp, defend_hp, garrison, result, attack_hero1_id, attack_hero2_id, attack_hero3_id
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


def ai_chat(message):
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
        conn = get_conn()
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
        with st.spinner("查询中..."):
            df = query_battle_reports(name, int(min_hp))
        st.success(f"共 {len(df)} 条")
        st.dataframe(df, use_container_width=True, hide_index=True)

elif page == "同盟成员常用队伍":
    st.header("同盟成员常用队伍")
    name = st.text_input("成员名关键词", key="mn")
    min_hp = st.number_input("兵力下限", 0, 99999, 0, step=1000, key="m")
    if st.button("查询", type="primary"):
        with st.spinner("查询中..."):
            df = query_member_teams(int(min_hp), name.strip())
        st.success(f"共 {len(df)} 名成员")
        df_disp = df.copy()
        df_disp["武将"] = df_disp["h1"].astype(str) + " / " + df_disp["h2"].astype(str) + " / " + df_disp["h3"].astype(str)
        df_disp["最近出战"] = pd.to_datetime(df_disp["last_time"], unit="s")
        st.dataframe(df_disp[["player_name", "武将", "total_star", "hp", "team_count", "最近出战"]].rename(
            columns={"player_name": "成员", "total_star": "红度", "hp": "兵力", "team_count": "使用次数"}),
            use_container_width=True, hide_index=True)
        st.download_button("导出 CSV", df.to_csv(index=False).encode("utf-8-sig"), "member_teams.csv")

elif page == "敌军队伍":
    st.header("敌军队伍")
    st.caption("与本盟交战过的非己方同盟人员(含胜负)，已过滤无同盟归属")
    name = st.text_input("玩家名关键词", key="en")
    min_hp = st.number_input("兵力下限", 0, 99999, 0, step=1000, key="e")
    if st.button("查询", type="primary"):
        with st.spinner("查询中..."):
            df = query_enemy_teams(int(min_hp), name.strip())
        st.success(f"共 {len(df)} 支队伍")
        df_disp = df.copy()
        df_disp["武将"] = df_disp["h1"].astype(str) + " / " + df_disp["h2"].astype(str) + " / " + df_disp["h3"].astype(str)
        st.dataframe(df_disp[["player_name", "武将", "total_star", "hp", "encounter_count"]].rename(
            columns={"player_name": "玩家", "total_star": "红度", "hp": "兵力", "encounter_count": "交战次数"}),
            use_container_width=True, hide_index=True)
        st.download_button("导出 CSV", df.to_csv(index=False).encode("utf-8-sig"), "enemy_teams.csv")

elif page == "成员活跃度":
    st.header("成员活跃度与翻地次数")
    if st.button("查询", type="primary"):
        with st.spinner("查询中..."):
            df = query_member_activity()
        df["最近参战"] = pd.to_datetime(df["last_time"], unit="s")
        st.dataframe(df[["name", "group", "wu", "power", "atk_count", "def_count", "land_count", "最近参战"]].rename(
            columns={"name": "成员", "group": "分组", "wu": "武勋", "power": "势力",
                     "atk_count": "进攻场次", "def_count": "防守场次", "land_count": "翻地次数"}),
            use_container_width=True, hide_index=True)
        st.download_button("导出 CSV", df.to_csv(index=False).encode("utf-8-sig"), "activity.csv")

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
            answer = ai_chat(q.strip())
        st.session_state.ai_history.append(("assistant", answer))

    for role, content in st.session_state.ai_history[-20:]:
        if role == "user":
            st.markdown(f'<div style="text-align:right;background:#e8f0fe;border-radius:10px;'
                        f'padding:8px 12px;margin:4px 0">{content}</div>', unsafe_allow_html=True)
        else:
            st.markdown(f'<div style="background:#f6f6f6;border-radius:10px;'
                        f'padding:8px 12px;margin:4px 0">{content}</div>', unsafe_allow_html=True)