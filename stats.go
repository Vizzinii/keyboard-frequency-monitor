package main

import (
	"time"
)

// StatsPayload 的字段结构与网页面板的 JS 约定一致：
// keys/days 是 [名称, 数值] 数组，hours 是 24 个整数。
type StatsPayload struct {
	Range    string   `json:"range"`
	Total    int64    `json:"total"`
	Distinct int      `json:"distinct"`
	Since    *string  `json:"since"`
	Keys     [][]any  `json:"keys"`
	Days     [][]any  `json:"days"`
	Hours    []int64  `json:"hours"`
	Now      string   `json:"now"`
}

func rangeStart(rng string) string {
	now := time.Now()
	switch rng {
	case "today":
		return now.Format(dateLayout)
	case "week":
		return now.AddDate(0, 0, -6).Format(dateLayout)
	default: // all
		return ""
	}
}

func BuildStats(s *Store, rng string) (StatsPayload, error) {
	start := rangeStart(rng)
	keys, err := s.Keys(start)
	if err != nil {
		return StatsPayload{}, err
	}
	hours, err := s.Hourly(start)
	if err != nil {
		return StatsPayload{}, err
	}
	days, err := s.Daily(14)
	if err != nil {
		return StatsPayload{}, err
	}
	since, err := s.FirstDay()
	if err != nil {
		return StatsPayload{}, err
	}

	var total int64
	payload := StatsPayload{
		Range:    rng,
		Since:    since,
		Hours:    hours[:],
		Keys:     make([][]any, 0, len(keys)),
		Now:      time.Now().Format("15:04:05"),
	}
	for _, kc := range keys {
		total += kc.Count
		payload.Keys = append(payload.Keys, []any{kc.Name, kc.Count})
	}
	payload.Total = total
	payload.Distinct = len(keys)
	for _, dc := range days {
		payload.Days = append(payload.Days, []any{dc.Day, dc.Count})
	}
	return payload, nil
}
