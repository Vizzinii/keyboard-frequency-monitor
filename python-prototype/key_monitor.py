#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
键盘频率监视器 —— 后台记录 + 本地网页仪表盘

- 全局记录每个键的按下次数（带时间戳），每秒批量写入 SQLite，
  进程被强杀/断电最多丢最后一秒，不依赖"正常退出"来保存。
- 内置本地 HTTP 服务，浏览器打开面板查看：键位热力图、使用排行、
  每日趋势、时段分布。只监听 127.0.0.1，外部无法访问。
- 只对按键事件计数，不记录输入内容、剪贴板或窗口信息；数据仅存本机。
"""

import argparse
import csv
import json
import os
import sqlite3
import sys
import threading
import time
import urllib.parse
import urllib.request
import webbrowser
from collections import Counter
from datetime import date, datetime, timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

try:
    from pynput import keyboard
except ImportError:
    sys.exit("缺少依赖 pynput。请先运行:  pip install pynput")

BASE_DIR = os.path.dirname(os.path.abspath(__file__))
DB_PATH = os.path.join(BASE_DIR, "keyboard_stats.db")
DASHBOARD = os.path.join(BASE_DIR, "dashboard.html")
LEGACY_JSON = os.path.join(BASE_DIR, "key_counts.json")   # 旧版纯计数文件
DEFAULT_PORT = 8321
FLUSH_INTERVAL = 1.0        # 秒：按键批量落盘的间隔，也是断电时最多丢失的数据量

CHAR_ALIASES = {" ": "space", "\r": "enter", "\n": "enter", "\t": "tab"}
VALID_RANGES = ("today", "week", "all")


def key_label(key) -> str:
    """把 pynput 的按键对象转成统一的键名。"""
    if isinstance(key, keyboard.KeyCode):
        if key.char is not None:
            return CHAR_ALIASES.get(key.char, key.char.lower())
        if key.vk is not None:
            return f"<vk_{key.vk}>"     # 无字符映射的虚拟键（如小键盘某些键）
    return getattr(key, "name", None) or str(key)   # esc / shift / shift_r / f1 等


class Recorder:
    """监听线程只往内存缓冲追加标签；主循环每秒取走一批入库。"""

    def __init__(self):
        self.buffer = []
        self.lock = threading.Lock()

    def on_press(self, key):
        try:
            label = key_label(key)
        except Exception:
            return
        if label:
            with self.lock:
                self.buffer.append(label)

    def drain(self) -> dict:
        """取出缓冲并按 (键, 当天, 小时) 聚合成待写入字典。"""
        with self.lock:
            buf, self.buffer = self.buffer, []
        now = datetime.now()
        day, hour = now.strftime("%Y-%m-%d"), now.hour
        return {(k, day, hour): n for k, n in Counter(buf).items()}


class StatsDB:
    """SQLite 存储。按“键 × 日期 × 小时”预聚合，行数有限且查询快。"""

    def __init__(self, path):
        self.lock = threading.Lock()
        self.conn = sqlite3.connect(path, check_same_thread=False)
        self.conn.execute("PRAGMA journal_mode=WAL")
        self.conn.execute("PRAGMA synchronous=NORMAL")
        self.conn.execute(
            """CREATE TABLE IF NOT EXISTS key_hour (
                   key  TEXT    NOT NULL,
                   day  TEXT    NOT NULL,
                   hour INTEGER NOT NULL,
                   n    INTEGER NOT NULL,
                   PRIMARY KEY (key, day, hour))""")
        self.conn.commit()

    def add(self, agg: dict):
        rows = [(k, d, h, n) for (k, d, h), n in agg.items()]
        with self.lock:
            self.conn.executemany(
                "INSERT INTO key_hour(key, day, hour, n) VALUES(?,?,?,?) "
                "ON CONFLICT(key, day, hour) DO UPDATE SET n = n + excluded.n",
                rows)
            self.conn.commit()

    def keys(self, day_from=None):
        """[(键名, 次数)] 按次数降序。day_from 为 None 表示全部。"""
        sql, args = "SELECT key, SUM(n) FROM key_hour", ()
        if day_from:
            # 'legacy' 是旧数据导入标记，不是真实日期，不能参与日期范围比较
            sql += " WHERE day >= ? AND day != 'legacy'"
            args = (day_from,)
        with self.lock:
            return self.conn.execute(sql + " GROUP BY key ORDER BY 2 DESC", args).fetchall()

    def daily(self, days=14):
        """最近 N 天 [(日期, 次数)] 升序，缺的天补零；'legacy' 等非日期行排除。"""
        start = (date.today() - timedelta(days=days - 1)).isoformat()
        with self.lock:
            got = dict(self.conn.execute(
                "SELECT day, SUM(n) FROM key_hour "
                "WHERE day >= ? AND GLOB('[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]', day) "
                "GROUP BY day", (start,)).fetchall())
        out = []
        for i in range(days):
            d = (date.today() - timedelta(days=days - 1 - i)).isoformat()
            out.append((d, got.get(d, 0)))
        return out

    def hourly(self, day_from=None):
        """24 个小时桶的次数。旧数据导入的 'legacy' 行不计入时段分布。"""
        sql = "SELECT hour, SUM(n) FROM key_hour WHERE day != 'legacy'"
        args = ()
        if day_from:
            sql, args = sql + " AND day >= ?", (day_from,)
        with self.lock:
            got = dict(self.conn.execute(sql + " GROUP BY hour", args).fetchall())
        return [got.get(h, 0) for h in range(24)]

    def first_day(self):
        with self.lock:
            row = self.conn.execute(
                "SELECT MIN(day) FROM key_hour "
                "WHERE GLOB('[0-9][0-9][0-9][0-9]-*', day)").fetchone()
        return row[0] if row else None

    def close(self):
        with self.lock:
            self.conn.close()


def range_start(rng):
    """返回该范围的起始日期（含）；全部则返回 None。"""
    today = date.today()
    return {
        "today": today.isoformat(),
        "week": (today - timedelta(days=6)).isoformat(),
    }.get(rng)


def build_stats(db: StatsDB, rng: str) -> dict:
    start = range_start(rng)
    keys = db.keys(start)
    return {
        "range": rng,
        "total": sum(c for _, c in keys),
        "distinct": len(keys),
        "since": db.first_day(),
        "keys": keys[:300],
        "days": db.daily(14),
        "hours": db.hourly(start),
        "now": datetime.now().strftime("%H:%M:%S"),
    }


def import_legacy(db: StatsDB):
    """把旧版 key_counts.json 的历史总数并入（标记为 legacy，不计入时段分布）。"""
    if not os.path.exists(LEGACY_JSON):
        return
    try:
        with open(LEGACY_JSON, encoding="utf-8") as f:
            saved = {str(k): int(v) for k, v in json.load(f).get("counts", {}).items()}
    except (OSError, ValueError):
        saved = {}
    if saved:
        db.add({(k, "legacy", 0): v for k, v in saved.items()})
        print(f"已导入旧版统计 {sum(saved.values()):,} 次（保留在“全部”里）")
    os.replace(LEGACY_JSON, LEGACY_JSON + ".imported.bak")


class Handler(BaseHTTPRequestHandler):
    db = None      # 由 main 注入
    dashboard = DASHBOARD

    def do_GET(self):
        parsed = urllib.parse.urlparse(self.path)
        try:
            if parsed.path == "/api/stats":
                qs = urllib.parse.parse_qs(parsed.query)
                rng = (qs.get("range") or ["today"])[0]
                if rng not in VALID_RANGES:
                    rng = "today"
                self._send_json(build_stats(self.db, rng))
            elif parsed.path in ("/", "/index.html"):
                self._send_html()
            else:
                self.send_error(404)
        except Exception as e:
            self._send_json({"error": str(e)}, status=500)

    def _send_json(self, obj, status=200):
        body = json.dumps(obj, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _send_html(self):
        try:
            with open(self.dashboard, "rb") as f:
                body = f.read()
        except OSError:
            self.send_error(500, "找不到 dashboard.html")
            return
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass          # 面板每两秒拉一次数据，不必刷访问日志


def make_server(port):
    """绑定 127.0.0.1 上第一个可用端口；全忙则返回 (None, 尝试过的端口列表)。"""
    tried = []
    for p in range(port, port + 10):
        tried.append(p)
        try:
            srv = ThreadingHTTPServer(("127.0.0.1", p), Handler)
            srv.daemon_threads = True
            return srv, p, tried
        except OSError:
            continue
    return None, None, tried


def existing_instance(tried):
    """探测这些端口上是否已有本程序实例在跑，有则返回其面板地址。"""
    for p in tried:
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{p}/api/stats", timeout=1) as r:
                json.loads(r.read().decode("utf-8"))
            return p
        except Exception:
            continue
    return None


def export_csv(db: StatsDB, path: str):
    ranked = db.keys(None)
    total = sum(c for _, c in ranked)
    with open(path, "w", newline="", encoding="utf-8-sig") as f:
        w = csv.writer(f)
        w.writerow(["键名", "总次数", "占比%"])
        for name, c in ranked:
            w.writerow([name, c, f"{c / total * 100:.2f}" if total else "0"])
    print(f"已导出 {len(ranked)} 个键位的全部数据到 {path}")


def parse_args():
    p = argparse.ArgumentParser(description="键盘频率监视器（后台记录 + 浏览器面板）")
    p.add_argument("--port", type=int, default=DEFAULT_PORT,
                   help=f"面板端口（默认 {DEFAULT_PORT}，被占用会自动往后试）")
    p.add_argument("--no-browser", action="store_true", help="启动时不自动打开浏览器")
    p.add_argument("--reset", action="store_true", help="启动前清空所有统计数据")
    p.add_argument("--export", metavar="CSV文件", help="导出全部统计数据为 CSV 后退出")
    return p.parse_args()


def main():
    args = parse_args()

    if args.reset:
        for suffix in ("", "-wal", "-shm"):
            path = DB_PATH + suffix
            if os.path.exists(path):
                os.remove(path)
        print("[已清空所有统计数据]")

    db = StatsDB(DB_PATH)
    import_legacy(db)
    Handler.db = db

    if args.export:
        export_csv(db, args.export)
        db.close()
        return

    srv, port, tried = make_server(args.port)
    if srv is None:
        running = existing_instance(tried)
        if running:
            url = f"http://127.0.0.1:{running}/"
            print(f"已有实例在运行，直接打开它的面板: {url}")
            webbrowser.open(url)
        else:
            print(f"错误: 端口 {tried[0]}~{tried[-1]} 都被占用，请用 --port 换一个")
        db.close()
        return

    recorder = Recorder()
    listener = keyboard.Listener(on_press=recorder.on_press)
    listener.start()

    threading.Thread(target=srv.serve_forever, daemon=True).start()

    url = f"http://127.0.0.1:{port}/"
    print(f"记录已启动（全局生效），面板地址: {url}")
    print("本窗口保持开着就在持续记录；按 Ctrl+C 停止。数据每秒落盘，强关窗口也不丢。")
    if not args.no_browser:
        webbrowser.open(url)

    try:
        while True:
            time.sleep(FLUSH_INTERVAL)
            agg = recorder.drain()
            if agg:
                db.add(agg)
    except KeyboardInterrupt:
        pass
    finally:
        agg = recorder.drain()          # 收尾：把最后不足一秒的也写进去
        if agg:
            db.add(agg)
        srv.shutdown()
        listener.stop()
        db.close()
        print("\n已退出。数据保存在 keyboard_stats.db")


if __name__ == "__main__":
    try:
        sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    except Exception:
        pass
    main()
