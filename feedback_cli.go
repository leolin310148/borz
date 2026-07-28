package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/leolin310148/borz/internal/config"
)

const feedbackUsage = "Usage: borz feedback <message> [--category ux|bug|feature|docs|perf] [--command <cmd>]  |  borz feedback list [--limit N] | path"

// feedbackFileName lives directly under the borz home directory so feedback
// survives profile switches and is trivial to find (~/.borz/feedback.jsonl).
const feedbackFileName = "feedback.jsonl"

// feedbackEntry is one JSONL line of the local feedback file. Entries are
// written by agents (or humans) to flag friction, missing features, or ideas
// while using borz, and reviewed later by a maintainer.
type feedbackEntry struct {
	Time      time.Time `json:"time"`
	Version   string    `json:"version,omitempty"`
	Profile   string    `json:"profile,omitempty"`
	SessionID string    `json:"sessionId,omitempty"`
	Category  string    `json:"category,omitempty"`
	Command   string    `json:"command,omitempty"`
	Message   string    `json:"message"`
}

func feedbackFilePath() (string, error) {
	home, err := config.EnsureHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, feedbackFileName), nil
}

func handleFeedback(cmdArgs, rawArgs []string, jsonOutput bool) {
	sub := ""
	if len(cmdArgs) > 0 {
		sub = strings.ToLower(cmdArgs[0])
	}
	switch sub {
	case "":
		fatal(feedbackUsage)
	case "path":
		path, err := feedbackFilePath()
		if err != nil {
			fatal(err.Error())
			return
		}
		if jsonOutput {
			printJSON(map[string]any{"path": path})
			return
		}
		fmt.Println(path)
	case "list":
		listFeedback(rawArgs, jsonOutput)
	case "add":
		addFeedback(cmdArgs[1:], rawArgs, jsonOutput)
	default:
		addFeedback(cmdArgs, rawArgs, jsonOutput)
	}
}

func addFeedback(cmdArgs, rawArgs []string, jsonOutput bool) {
	message := strings.TrimSpace(strings.Join(feedbackPositionals(cmdArgs), " "))
	if message == "" {
		fatal(feedbackUsage)
		return
	}
	category, command, err := parseFeedbackOptions(rawArgs)
	if err != nil {
		fatal(err.Error())
		return
	}
	entry := feedbackEntry{
		Time:    time.Now().UTC(),
		Version: version,
		Profile: config.Profile(),
		SessionID: cliSessionID(
			os.Getenv("BORZ_SESSION_ID"), os.Getenv("TMUX_PANE"), os.Getenv("TERM_SESSION_ID"), os.Getppid(),
		),
		Category: category,
		Command:  command,
		Message:  message,
	}
	path, err := appendFeedback(entry)
	if err != nil {
		fatal(err.Error())
		return
	}
	if jsonOutput {
		printJSON(map[string]any{"saved": true, "path": path, "entry": entry})
		return
	}
	fmt.Printf("Feedback saved to %s\n", path)
}

func parseFeedbackOptions(args []string) (category, command string, err error) {
	category, categorySet := getArgValueOK(args, "--category")
	if categorySet {
		category = strings.ToLower(strings.TrimSpace(category))
		switch category {
		case "ux", "bug", "feature", "docs", "perf":
		case "":
			return "", "", fmt.Errorf("--category requires a value (ux, bug, feature, docs, or perf)")
		default:
			return "", "", fmt.Errorf("--category must be one of: ux, bug, feature, docs, perf")
		}
	}

	command, commandSet := getArgValueOK(args, "--command")
	command = strings.TrimSpace(command)
	if commandSet && command == "" {
		return "", "", fmt.Errorf("--command requires a non-empty value")
	}
	return category, command, nil
}

// feedbackPositionals returns the non-flag tokens of cmdArgs, skipping the
// values of the feedback value flags so `borz feedback --category ux slow
// snapshots` and `borz feedback slow snapshots --category ux` both work.
func feedbackPositionals(args []string) []string {
	valueFlags := map[string]bool{"--category": true, "--command": true}
	var out []string
	skip := false
	for i, a := range args {
		if skip {
			skip = false
			continue
		}
		if valueFlags[a] {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				skip = true
			}
			continue
		}
		if strings.HasPrefix(a, "--") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func appendFeedback(entry feedbackEntry) (string, error) {
	path, err := feedbackFilePath()
	if err != nil {
		return "", err
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return "", err
	}
	return path, nil
}

func listFeedback(rawArgs []string, jsonOutput bool) {
	limit := 50
	if raw := strings.TrimSpace(getArgValue(rawArgs, "--limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			fatal("--limit must be a positive integer")
			return
		}
		limit = n
	}
	entries, err := readFeedback()
	if err != nil {
		fatal(err.Error())
		return
	}
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	if jsonOutput {
		printJSON(entries)
		return
	}
	if len(entries) == 0 {
		fmt.Println("No feedback recorded yet")
		return
	}
	for _, entry := range entries {
		fmt.Println(formatFeedbackEntry(entry))
	}
}

// readFeedback parses the feedback JSONL file, oldest first. Unparseable
// lines are skipped so one corrupt write never hides the rest of the file.
func readFeedback() ([]feedbackEntry, error) {
	path, err := feedbackFilePath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var entries []feedbackEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry feedbackEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func formatFeedbackEntry(entry feedbackEntry) string {
	var meta []string
	if entry.Category != "" {
		meta = append(meta, entry.Category)
	}
	if entry.Command != "" {
		meta = append(meta, "cmd="+entry.Command)
	}
	if entry.SessionID != "" {
		meta = append(meta, "session="+entry.SessionID)
	}
	tags := ""
	if len(meta) > 0 {
		tags = " [" + strings.Join(meta, " ") + "]"
	}
	return fmt.Sprintf("%s%s %s", entry.Time.Local().Format("2006-01-02 15:04:05"), tags, entry.Message)
}
