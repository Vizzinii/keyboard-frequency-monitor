package main

import (
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAddAndRangeQueries(t *testing.T) {
	s := newTestStore(t)
	today := time.Now().Format(dateLayout)
	yest := time.Now().AddDate(0, 0, -1).Format(dateLayout)

	if err := s.Add(today, 10, map[string]int{"a": 5, "space": 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(yest, 9, map[string]int{"a": 3}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("legacy", 0, map[string]int{"x": 7}); err != nil {
		t.Fatal(err)
	}

	got, err := s.Keys(today)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "a" || got[0].Count != 5 || got[1].Name != "space" {
		t.Fatalf("today = %+v", got)
	}

	all, err := s.Keys("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Count != 8 { // a 跨天累计 5+3
		t.Fatalf("all = %+v", all)
	}
}

func TestLegacyNotInDateRanges(t *testing.T) {
	s := newTestStore(t)
	today := time.Now().Format(dateLayout)
	_ = s.Add("legacy", 0, map[string]int{"x": 7})
	_ = s.Add(today, 1, map[string]int{"e": 100})

	for _, rng := range []string{"today", "week"} {
		p, err := BuildStats(s, rng)
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 100 {
			t.Fatalf("range=%s total=%d, legacy 混进了范围统计", rng, p.Total)
		}
	}
	all, err := BuildStats(s, "all")
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 107 {
		t.Fatalf("all total=%d, 应包含旧数据", all.Total)
	}
}

func TestDailyPaddingAndOrder(t *testing.T) {
	s := newTestStore(t)
	today := time.Now()
	_ = s.Add(today.Format(dateLayout), 8, map[string]int{"a": 7})
	_ = s.Add(today.AddDate(0, 0, -3).Format(dateLayout), 8, map[string]int{"b": 4})
	_ = s.Add("legacy", 0, map[string]int{"c": 99})

	days, err := s.Daily(14)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 14 {
		t.Fatalf("len=%d", len(days))
	}
	if days[13].Day != today.Format(dateLayout) || days[13].Count != 7 {
		t.Fatalf("last = %+v", days[13])
	}
	if days[10].Day != today.AddDate(0, 0, -3).Format(dateLayout) || days[10].Count != 4 {
		t.Fatalf("-3 天位置错误: %+v", days[10])
	}
	var sum int64
	for _, d := range days {
		sum += d.Count
	}
	if sum != 11 { // 补零后只有这两天有数，legacy 不算
		t.Fatalf("sum=%d", sum)
	}
}

func TestHourlyExcludesLegacy(t *testing.T) {
	s := newTestStore(t)
	today := time.Now().Format(dateLayout)
	_ = s.Add(today, 23, map[string]int{"a": 6})
	_ = s.Add("legacy", 0, map[string]int{"b": 50})

	h, err := s.Hourly("")
	if err != nil {
		t.Fatal(err)
	}
	if h[23] != 6 || h[0] != 0 {
		t.Fatalf("hourly=%v", h)
	}
}

func TestFirstDay(t *testing.T) {
	s := newTestStore(t)
	day, err := s.FirstDay()
	if err != nil {
		t.Fatal(err)
	}
	if day != nil {
		t.Fatalf("空库应返回 nil, got %v", *day)
	}
	today := time.Now().Format(dateLayout)
	_ = s.Add(today, 0, map[string]int{"a": 1})
	_ = s.Add("legacy", 0, map[string]int{"x": 1})
	day, err = s.FirstDay()
	if err != nil {
		t.Fatal(err)
	}
	if day == nil || *day != today {
		t.Fatalf("got %v, want %s（legacy 不应参与）", day, today)
	}
}
