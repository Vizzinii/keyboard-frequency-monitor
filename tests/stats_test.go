package main

import (
	"encoding/json"
	"testing"
	"time"
)

// 面板的 JS 依赖 keys/days 是 [名称, 数值] 数组、hours 是 24 个整数，
// 这里锁住 JSON 形状防止以后改坏。
func TestStatsPayloadJSONShape(t *testing.T) {
	s := newTestStore(t)
	today := time.Now().Format(dateLayout)
	if err := s.Add(today, 9, map[string]int{"e": 100, "space": 40}); err != nil {
		t.Fatal(err)
	}

	p, err := BuildStats(s, "today")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["total"].(float64) != 140 {
		t.Fatalf("total = %v", m["total"])
	}
	keys := m["keys"].([]any)
	first := keys[0].([]any)
	if first[0] != "e" || first[1].(float64) != 100 {
		t.Fatalf("keys[0] = %v", first)
	}
	days := m["days"].([]any)
	if len(days) != 14 {
		t.Fatalf("days len = %d", len(days))
	}
	hours := m["hours"].([]any)
	if len(hours) != 24 || hours[9].(float64) != 140 { // e 和 space 都在 9 点
		t.Fatalf("hours 错误")
	}
	if _, ok := m["since"]; !ok {
		t.Fatal("缺少 since 字段")
	}
}

func TestRangeStart(t *testing.T) {
	now := time.Now()
	if got := rangeStart("today"); got != now.Format(dateLayout) {
		t.Errorf("today = %q, want %q", got, now.Format(dateLayout))
	}
	if got := rangeStart("week"); got != now.AddDate(0, 0, -6).Format(dateLayout) {
		t.Errorf("week = %q, want %q", got, now.AddDate(0, 0, -6).Format(dateLayout))
	}
	if got := rangeStart("all"); got != "" {
		t.Errorf("all = %q, want 空串", got)
	}
	if got := rangeStart("bogus"); got != "" { // 未知范围走默认分支
		t.Errorf("未知范围 = %q, want 空串", got)
	}
}
