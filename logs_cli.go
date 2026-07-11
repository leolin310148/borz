package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/config"
	"github.com/leolin310148/borz/internal/observability"
)

type logStats struct {
	Since           time.Time                    `json:"since"`
	Commands        int                          `json:"commands"`
	CommandFailures int                          `json:"commandFailures"`
	Tools           int                          `json:"tools"`
	ToolFailures    int                          `json:"toolFailures"`
	CommandP95MS    int64                        `json:"commandP95Ms"`
	ToolP95MS       int64                        `json:"toolP95Ms"`
	ByAction        map[string]int               `json:"byAction"`
	ByTool          map[string]int               `json:"byTool"`
	ByError         map[string]int               `json:"byError"`
	ActionStats     map[string]logOperationStats `json:"actionStats"`
	ToolStats       map[string]logOperationStats `json:"toolStats"`
	Bursts          []logBurst                   `json:"bursts,omitempty"`
}

type logOperationStats struct {
	Count    int   `json:"count"`
	Failures int   `json:"failures"`
	P50MS    int64 `json:"p50Ms"`
	P95MS    int64 `json:"p95Ms"`
	MaxMS    int64 `json:"maxMs"`
}

type logBurst struct {
	Second    time.Time `json:"second"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	SessionID string    `json:"sessionId,omitempty"`
	Count     int       `json:"count"`
}

func handleLogs(cmdArgs, rawArgs []string, sinceValue string, jsonOutput bool) {
	sub := "path"
	if len(cmdArgs) > 0 {
		sub = strings.ToLower(cmdArgs[0])
	}
	switch sub {
	case "path":
		if err := os.MkdirAll(config.LogsDir(), 0o700); err != nil {
			fatal(err.Error())
			return
		}
		_ = os.Chmod(config.LogsDir(), 0o700)
		if jsonOutput {
			printJSON(map[string]any{"path": config.LogsDir(), "profile": logProfileName()})
			return
		}
		fmt.Println(config.LogsDir())
	case "tail":
		lines := 50
		if raw := strings.TrimSpace(getArgValue(rawArgs, "--lines")); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				fatal("--lines must be a positive integer")
				return
			}
			lines = n
		}
		entries, err := observability.ReadEntries(time.Time{})
		if err != nil {
			fatal(err.Error())
			return
		}
		if len(entries) > lines {
			entries = entries[len(entries)-lines:]
		}
		if jsonOutput {
			printJSON(entries)
			return
		}
		if len(entries) == 0 {
			fmt.Println("No operational logs yet")
			return
		}
		for _, entry := range entries {
			fmt.Println(formatLogEntry(entry))
		}
	case "stats":
		since, err := parseLogSince(sinceValue, time.Now())
		if err != nil {
			fatal(err.Error())
			return
		}
		entries, err := observability.ReadEntries(since)
		if err != nil {
			fatal(err.Error())
			return
		}
		stats := calculateLogStats(entries, since)
		if jsonOutput {
			printJSON(stats)
			return
		}
		printLogStats(stats)
	default:
		fatal("Usage: borz logs [path|tail|stats] [--lines N] [--since 7d] [--json]")
	}
}

func logProfileName() string {
	if profile := config.Profile(); profile != "" {
		return profile
	}
	return "default"
}

func formatLogEntry(entry observability.Entry) string {
	name := entry.Action
	if entry.Tool != "" {
		name = entry.Tool
	}
	if name == "" {
		name = entry.Event
	}
	status := ""
	if entry.Success != nil {
		if *entry.Success {
			status = " ok"
		} else {
			status = " error=" + entry.ErrorCode
		}
	}
	duration := ""
	if entry.DurationMS > 0 {
		duration = fmt.Sprintf(" %dms", entry.DurationMS)
	}
	session := ""
	if entry.SessionID != "" {
		session = " session=" + entry.SessionID
	}
	return fmt.Sprintf("%s %-6s %-20s %s%s%s%s",
		entry.Time.Local().Format("2006-01-02 15:04:05"), entry.Component, entry.Event,
		name, status, duration, session)
}

func parseLogSince(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "7d"
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || days <= 0 {
			return time.Time{}, fmt.Errorf("--since must be a duration such as 24h or 7d")
		}
		return now.Add(-time.Duration(days) * 24 * time.Hour), nil
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return now.Add(-duration), nil
	}
	if absolute, err := time.Parse(time.RFC3339, value); err == nil {
		return absolute, nil
	}
	return time.Time{}, fmt.Errorf("--since must be a duration such as 24h or 7d, or RFC3339 time")
}

func calculateLogStats(entries []observability.Entry, since time.Time) logStats {
	stats := logStats{
		Since: since, ByAction: map[string]int{}, ByTool: map[string]int{}, ByError: map[string]int{},
		ActionStats: map[string]logOperationStats{}, ToolStats: map[string]logOperationStats{},
	}
	var commandDurations, toolDurations []int64
	actionDurations, toolDurationsByName := map[string][]int64{}, map[string][]int64{}
	actionFailures, toolFailuresByName := map[string]int{}, map[string]int{}
	type burstKey struct {
		second    time.Time
		kind      string
		name      string
		sessionID string
	}
	burstCounts := map[burstKey]int{}
	for _, entry := range entries {
		switch entry.Event {
		case "command_completed":
			stats.Commands++
			stats.ByAction[entry.Action]++
			actionDurations[entry.Action] = append(actionDurations[entry.Action], entry.DurationMS)
			if entry.Success != nil && !*entry.Success {
				stats.CommandFailures++
				actionFailures[entry.Action]++
			}
			commandDurations = append(commandDurations, entry.DurationMS)
			burstCounts[burstKey{entry.Time.Truncate(time.Second), "command", entry.Action, entry.SessionID}]++
		case "tool_completed":
			stats.Tools++
			stats.ByTool[entry.Tool]++
			toolDurationsByName[entry.Tool] = append(toolDurationsByName[entry.Tool], entry.DurationMS)
			if entry.Success != nil && !*entry.Success {
				stats.ToolFailures++
				toolFailuresByName[entry.Tool]++
			}
			toolDurations = append(toolDurations, entry.DurationMS)
			burstCounts[burstKey{entry.Time.Truncate(time.Second), "tool", entry.Tool, entry.SessionID}]++
		default:
			continue
		}
		if entry.ErrorCode != "" {
			stats.ByError[entry.Component+":"+entry.ErrorCode]++
		}
	}
	stats.CommandP95MS = percentile95(commandDurations)
	stats.ToolP95MS = percentile95(toolDurations)
	stats.ActionStats = summarizeOperations(actionDurations, actionFailures)
	stats.ToolStats = summarizeOperations(toolDurationsByName, toolFailuresByName)
	for key, count := range burstCounts {
		if count >= 5 {
			stats.Bursts = append(stats.Bursts, logBurst{
				Second: key.second, Kind: key.kind, Name: key.name, SessionID: key.sessionID, Count: count,
			})
		}
	}
	sort.Slice(stats.Bursts, func(i, j int) bool {
		if stats.Bursts[i].Count != stats.Bursts[j].Count {
			return stats.Bursts[i].Count > stats.Bursts[j].Count
		}
		return stats.Bursts[i].Second.Before(stats.Bursts[j].Second)
	})
	if len(stats.Bursts) > 10 {
		stats.Bursts = stats.Bursts[:10]
	}
	return stats
}

func summarizeOperations(durations map[string][]int64, failures map[string]int) map[string]logOperationStats {
	result := make(map[string]logOperationStats, len(durations))
	for name, values := range durations {
		copied := append([]int64(nil), values...)
		sort.Slice(copied, func(i, j int) bool { return copied[i] < copied[j] })
		result[name] = logOperationStats{
			Count: len(copied), Failures: failures[name], P50MS: percentile(copied, 50),
			P95MS: percentile(copied, 95), MaxMS: copied[len(copied)-1],
		}
	}
	return result
}

func percentile95(values []int64) int64 {
	return percentile(values, 95)
}

func percentile(values []int64, percentage int) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	idx := (percentage*len(values) + 99) / 100
	if idx < 1 {
		idx = 1
	}
	return values[idx-1]
}

func printLogStats(stats logStats) {
	fmt.Printf("Operational logs since %s\n", stats.Since.Local().Format(time.RFC3339))
	fmt.Printf("Commands: %d (%d failed)\n", stats.Commands, stats.CommandFailures)
	fmt.Printf("MCP tools: %d (%d failed)\n", stats.Tools, stats.ToolFailures)
	fmt.Printf("Command p95: %dms; MCP tool p95: %dms\n", stats.CommandP95MS, stats.ToolP95MS)
	printOperationStats("Action performance", stats.ActionStats)
	printOperationStats("Tool performance", stats.ToolStats)
	printCountMap("Error events by layer", stats.ByError)
	if len(stats.Bursts) > 0 {
		fmt.Println("Bursts (at least 5 identical operations in one second):")
		for _, burst := range stats.Bursts {
			session := ""
			if burst.SessionID != "" {
				session = " session=" + burst.SessionID
			}
			fmt.Printf("  %s %-7s %-24s %d%s\n", burst.Second.Local().Format("2006-01-02 15:04:05"), burst.Kind, burst.Name, burst.Count, session)
		}
	}
}

func printOperationStats(label string, stats map[string]logOperationStats) {
	if len(stats) == 0 {
		return
	}
	names := make([]string, 0, len(stats))
	for name := range stats {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if stats[names[i]].Count == stats[names[j]].Count {
			return names[i] < names[j]
		}
		return stats[names[i]].Count > stats[names[j]].Count
	})
	fmt.Printf("%s:\n", label)
	fmt.Printf("  %-24s %7s %7s %8s %8s %8s\n", "name", "count", "failed", "p50", "p95", "max")
	for _, name := range names {
		item := stats[name]
		fmt.Printf("  %-24s %7d %7d %7dms %7dms %7dms\n", name, item.Count, item.Failures, item.P50MS, item.P95MS, item.MaxMS)
	}
}

func printCountMap(label string, counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	type pair struct {
		name  string
		count int
	}
	items := make([]pair, 0, len(counts))
	for name, count := range counts {
		items = append(items, pair{name, count})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count == items[j].count {
			return items[i].name < items[j].name
		}
		return items[i].count > items[j].count
	})
	fmt.Printf("%s:\n", label)
	for _, item := range items {
		fmt.Printf("  %-28s %d\n", item.name, item.count)
	}
}
