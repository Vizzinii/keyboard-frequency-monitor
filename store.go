package main

import (
	"database/sql"
	"sync"
	"time"

	_ "modernc.org/sqlite" // 纯 Go 的 SQLite 驱动，无 cgo
)

const dateLayout = "2006-01-02"

const numericDay = "GLOB('[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]', day)"

type Store struct {
	mu sync.Mutex
	db *sql.DB
}

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// :memory: 时限制单连接，否则建表语句可能落在连接池的另一条连接上
	if path == ":memory:" {
		db.SetMaxOpenConns(1)
	}
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=3000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	const schema = `CREATE TABLE IF NOT EXISTS key_hour (
		key  TEXT    NOT NULL,
		day  TEXT    NOT NULL,
		hour INTEGER NOT NULL,
		n    INTEGER NOT NULL,
		PRIMARY KEY (key, day, hour))`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Add 把一批 键名->次数 写入指定的“日 + 小时”桶，一个事务提交。
// 每秒调用一次，进程被强杀最多丢最后一秒。
func (s *Store) Add(day string, hour int, labels map[string]int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO key_hour(key, day, hour, n) VALUES(?,?,?,?)
		ON CONFLICT(key, day, hour) DO UPDATE SET n = n + excluded.n`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for name, cnt := range labels {
		if _, err := stmt.Exec(name, day, hour, cnt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type KeyCount struct {
	Name  string
	Count int64
}

func argsIf(cond bool, v any) []any {
	if cond {
		return []any{v}
	}
	return nil
}

// Keys 返回 [(键名, 次数)] 按次数降序；dayFrom 为空表示全部（含 legacy）。
func (s *Store) Keys(dayFrom string) ([]KeyCount, error) {
	q := "SELECT key, SUM(n) FROM key_hour"
	if dayFrom != "" {
		// legacy 是旧数据导入标记，不是真实日期，不参与日期范围统计
		q += " WHERE day >= ? AND day <> 'legacy'"
	}
	q += " GROUP BY key ORDER BY 2 DESC"
	rows, err := s.db.Query(q, argsIf(dayFrom != "", dayFrom)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KeyCount
	for rows.Next() {
		var kc KeyCount
		if err := rows.Scan(&kc.Name, &kc.Count); err != nil {
			return nil, err
		}
		out = append(out, kc)
	}
	return out, rows.Err()
}

type DayCount struct {
	Day   string
	Count int64
}

// Daily 返回最近 days 天的总量，升序、缺的天补零；legacy 行天然排除。
func (s *Store) Daily(days int) ([]DayCount, error) {
	start := time.Now().AddDate(0, 0, -(days - 1)).Format(dateLayout)
	rows, err := s.db.Query(
		`SELECT day, SUM(n) FROM key_hour
		 WHERE day >= ? AND `+numericDay+` GROUP BY day`, start)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	got := map[string]int64{}
	for rows.Next() {
		var d string
		var c int64
		if err := rows.Scan(&d, &c); err != nil {
			return nil, err
		}
		got[d] = c
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]DayCount, days)
	today := time.Now()
	for i := range out {
		day := today.AddDate(0, 0, -(days - 1 - i)).Format(dateLayout)
		out[i] = DayCount{Day: day, Count: got[day]}
	}
	return out, nil
}

// Hourly 返回 24 个小时桶的总量；legacy 行不计入时段分布。
func (s *Store) Hourly(dayFrom string) ([24]int64, error) {
	var out [24]int64
	q := "SELECT hour, SUM(n) FROM key_hour WHERE day <> 'legacy'"
	if dayFrom != "" {
		q += " AND day >= ?"
	}
	q += " GROUP BY hour"
	rows, err := s.db.Query(q, argsIf(dayFrom != "", dayFrom)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var h int64
		var c int64
		if err := rows.Scan(&h, &c); err != nil {
			return out, err
		}
		if h >= 0 && h < 24 {
			out[h] = c
		}
	}
	return out, rows.Err()
}

// FirstDay 返回最早的真实统计日期（排除 legacy），无数据时返回 nil。
func (s *Store) FirstDay() (*string, error) {
	var ns sql.NullString
	err := s.db.QueryRow("SELECT MIN(day) FROM key_hour WHERE " + numericDay).Scan(&ns)
	if err != nil {
		return nil, err
	}
	if !ns.Valid {
		return nil, nil
	}
	return &ns.String, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 尽力而为：把 WAL 合并回主文件，兑现"备份 = 复制单个 db 文件"；
	// 失败不影响关闭（下次启动 SQLite 会自动恢复）。
	_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return s.db.Close()
}
