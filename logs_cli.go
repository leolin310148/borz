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
	Since           time.Time      `json:"since"`
	Commands        int            `json:"commands"`
	CommandFailures int            `json:"commandFailures"`
	Tools           int            `json:"tools"`
	ToolFailures    int            `json:"toolFailures"`
	CommandP95MS    int64          `json:"commandP95Ms"`
	ToolP95MS       int64          `json:"toolP95Ms"`
	ByAction        map[string]int `json:"byAction"`
	ByTool          map[string]int `json:"byTool"`
	ByError         map[string]int `json:"byError"`
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
	}
	var commandDurations, toolDurations []int64
	for _, entry := range entries {
		switch entry.Event {
		case "command_completed":
			stats.Commands++
			stats.ByAction[entry.Action]++
			if entry.Success != nil && !*entry.Success {
				stats.CommandFailures++
			}
			commandDurations = append(commandDurations, entry.DurationMS)
		case "tool_completed":
			stats.Tools++
			stats.ByTool[entry.Tool]++
			if entry.Success != nil && !*entry.Success {
				stats.ToolFailures++
			}
			toolDurations = append(toolDurations, entry.DurationMS)
		default:
			continue
		}
		if entry.ErrorCode != "" {
			stats.ByError[entry.Component+":"+entry.ErrorCode]++
		}
	}
	stats.CommandP95MS = percentile95(commandDurations)
	stats.ToolP95MS = percentile95(toolDurations)
	return stats
}

func percentile95(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	idx := (95*len(values) + 99) / 100
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
	printCountMap("Actions", stats.ByAction)
	printCountMap("Tools", stats.ByTool)
	printCountMap("Error events by layer", stats.ByError)
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
