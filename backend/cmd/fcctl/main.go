package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"file-converter/backend/internal/protocol"
)

type command struct {
	name       string
	files      []string
	preset     string
	outputDir  string
	conflict   string
	quality    *int
	maxSize    *int
	background string
	job        string
	jsonOutput bool
	menu       bool
	recursive  bool
	menuAction string
	menuArgs   []string
}

type wireEvent struct {
	Event string  `json:"event"`
	Data  jobData `json:"data"`
}

type jobData struct {
	ID       string     `json:"id"`
	State    string     `json:"state"`
	Progress float64    `json:"progress"`
	Files    []fileData `json:"files"`
}

type fileData struct {
	Source string     `json:"source"`
	State  string     `json:"state"`
	Error  *fileError `json:"error,omitempty"`
	Notice string     `json:"notice,omitempty"`
}

type fileError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func socketPath() (string, error) {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		return "", fmt.Errorf("XDG_RUNTIME_DIR is not set")
	}
	return filepath.Join(runtime, "file-converter", "fcd.sock"), nil
}

func main() {
	cmd, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		usage()
		os.Exit(2)
	}
	path, err := socketPath()
	if err != nil {
		fatal(err)
	}
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		fatal(err)
	}
	defer conn.Close()

	req := commandRequest(cmd)
	if err := writeRequest(conn, req); err != nil {
		fatal(err)
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 8*1024*1024)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var value map[string]any
		if err := json.Unmarshal(line, &value); err != nil {
			fatal(err)
		}
		if cmd.jsonOutput {
			fmt.Println(string(line))
		} else if _, hasError := value["error"]; hasError {
			fmt.Fprintln(os.Stderr, formatHuman(value))
		} else {
			fmt.Println(formatHuman(value))
		}
		if _, hasError := value["error"]; hasError {
			os.Exit(1)
		}
		if cmd.name != "convert" {
			if _, hasResult := value["result"]; hasResult {
				return
			}
			continue
		}
		if value["event"] == "job.finished" {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		fatal(err)
	}
}

func parseArgs(args []string) (command, error) {
	if len(args) == 0 {
		return command{}, errors.New("command is required")
	}
	cmd := command{name: args[0], conflict: "rename"}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			cmd.jsonOutput = true
		case "--preset":
			value, next, err := optionValue(args, i)
			if err != nil {
				return command{}, err
			}
			cmd.preset, i = value, next
		case "--to":
			value, next, err := optionValue(args, i)
			if err != nil {
				return command{}, err
			}
			cmd.preset, i = presetForTarget(value), next
		case "--output-dir", "--out-dir":
			value, next, err := optionValue(args, i)
			if err != nil {
				return command{}, err
			}
			cmd.outputDir, i = value, next
		case "--quality", "--max-size":
			flag := args[i]
			value, next, err := optionValue(args, i)
			if err != nil {
				return command{}, err
			}
			n, err := strconv.Atoi(value)
			if err != nil {
				return command{}, fmt.Errorf("%s must be an integer", flag)
			}
			if flag == "--quality" {
				cmd.quality = &n
			} else {
				cmd.maxSize = &n
			}
			i = next
		case "--background":
			value, next, err := optionValue(args, i)
			if err != nil {
				return command{}, err
			}
			cmd.background, i = value, next
		case "--recursive":
			cmd.recursive = true
		case "--menu":
			cmd.menu = true
		case "--on-conflict":
			value, next, err := optionValue(args, i)
			if err != nil {
				return command{}, err
			}
			cmd.conflict, i = value, next
		default:
			if strings.HasPrefix(args[i], "-") {
				return command{}, fmt.Errorf("unknown option: %s", args[i])
			}
			switch cmd.name {
			case "menu":
				if cmd.menuAction == "" {
					cmd.menuAction = args[i]
				} else {
					cmd.menuArgs = append(cmd.menuArgs, args[i])
				}
			case "cancel":
				cmd.job = args[i]
			default:
				cmd.files = append(cmd.files, args[i])
			}
		}
	}
	if cmd.name != "hello" && cmd.name != "engines" && cmd.name != "menu" && cmd.name != "targets" && cmd.name != "convert" && cmd.name != "cancel" {
		return command{}, fmt.Errorf("unknown command: %s", cmd.name)
	}
	if cmd.name == "targets" || cmd.name == "convert" {
		if len(cmd.files) == 0 {
			return command{}, fmt.Errorf("at least one file is required")
		}
	}
	if cmd.name == "convert" && cmd.preset == "" {
		return command{}, fmt.Errorf("--to or --preset is required")
	}
	if cmd.name == "menu" {
		if err := validateMenuArgs(cmd); err != nil {
			return command{}, err
		}
	}
	if cmd.recursive && cmd.name != "convert" {
		return command{}, fmt.Errorf("--recursive requires convert")
	}
	if cmd.menu && cmd.name != "targets" {
		return command{}, fmt.Errorf("--menu requires targets")
	}
	if (cmd.name == "hello" || cmd.name == "engines") && len(cmd.files) > 0 {
		return command{}, fmt.Errorf("%s does not accept files", cmd.name)
	}
	if cmd.name == "cancel" && cmd.job == "" {
		return command{}, fmt.Errorf("job id is required")
	}
	if cmd.name != "convert" && (cmd.outputDir != "" || cmd.preset != "" || cmd.quality != nil || cmd.maxSize != nil || cmd.background != "") {
		return command{}, fmt.Errorf("conversion options require convert")
	}
	return cmd, nil
}

func validateMenuArgs(cmd command) error {
	switch cmd.menuAction {
	case "show":
		if len(cmd.menuArgs) != 0 {
			return fmt.Errorf("menu show takes no arguments")
		}
	case "set":
		if len(cmd.menuArgs) < 2 {
			return fmt.Errorf("menu set requires a category and at least one format")
		}
	case "reset":
		if len(cmd.menuArgs) > 1 {
			return fmt.Errorf("menu reset takes at most one category")
		}
	default:
		return fmt.Errorf("menu requires show, set or reset")
	}
	return nil
}

func optionValue(args []string, index int) (string, int, error) {
	if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
		return "", index, fmt.Errorf("%s requires a value", args[index])
	}
	return args[index+1], index + 1, nil
}

func presetForTarget(target string) string {
	switch strings.ToLower(target) {
	case "png":
		return "image-to-png"
	case "jpg", "jpeg":
		return "image-to-jpeg"
	case "webp":
		return "image-to-webp"
	case "jxl":
		return "image-to-jxl"
	case "avif":
		return "image-to-avif"
	case "gif":
		return "image-to-gif"
	case "bmp":
		return "image-to-bmp"
	case "tif", "tiff":
		return "image-to-tiff"
	case "ico":
		return "image-to-ico"
	case "heic", "heif":
		return "image-to-heic"
	default:
		return ""
	}
}

func commandRequest(cmd command) protocol.Request {
	switch cmd.name {
	case "hello":
		return protocol.Request{ID: 1, Method: "hello", Params: raw(map[string]any{"client": "fcctl", "protocol": 1})}
	case "engines":
		return protocol.Request{ID: 1, Method: "engines", Params: raw(map[string]any{})}
	case "targets":
		params := map[string]any{"files": cmd.files}
		if cmd.menu {
			params["menu"] = true
		}
		params["files"] = absolutePaths(cmd.files)
		return protocol.Request{ID: 1, Method: "targets", Params: raw(params)}
	case "menu":
		params := map[string]any{"action": cmd.menuAction}
		switch cmd.menuAction {
		case "set":
			params["category"], params["formats"] = cmd.menuArgs[0], cmd.menuArgs[1:]
		case "reset":
			if len(cmd.menuArgs) == 1 {
				params["category"] = cmd.menuArgs[0]
			}
		}
		return protocol.Request{ID: 1, Method: "menu", Params: raw(params)}
	case "cancel":
		return protocol.Request{ID: 1, Method: "cancel", Params: raw(map[string]any{"job": cmd.job})}
	default:
		params := map[string]any{"files": absolutePaths(cmd.files), "preset": cmd.preset, "output_dir": absolutePath(cmd.outputDir), "on_conflict": cmd.conflict}
		if cmd.recursive {
			params["recursive"] = true
		}
		if cmd.quality != nil {
			params["quality"] = *cmd.quality
		}
		if cmd.maxSize != nil {
			params["max_size"] = *cmd.maxSize
		}
		if cmd.background != "" {
			params["background"] = cmd.background
		}
		return protocol.Request{ID: 1, Method: "convert", Params: raw(params)}
	}
}

func writeRequest(conn net.Conn, req protocol.Request) error {
	data, err := protocol.EncodeMessage(req)
	if err != nil {
		return err
	}
	_, err = conn.Write(data)
	return err
}

func formatHuman(value map[string]any) string {
	if errValue, ok := value["error"].(map[string]any); ok {
		return fmt.Sprintf("error: %v", errValue["message"])
	}
	if _, ok := value["event"].(string); ok {
		var message wireEvent
		data, _ := json.Marshal(value)
		_ = json.Unmarshal(data, &message)
		return formatHumanEvent(message)
	}
	if result, ok := value["result"].(map[string]any); ok {
		if job, ok := result["job"].(string); ok {
			return "job " + job + " submitted"
		}
		if menuValue, ok := result["menu"].(map[string]any); ok {
			return formatMenu(result, menuValue)
		}
		if engines, ok := result["engines"].([]any); ok && result["server"] == nil {
			return formatEngines(engines)
		}
		return fmt.Sprintf("result: %v", result)
	}
	return fmt.Sprintf("%v", value)
}

func formatMenu(result, menuValue map[string]any) string {
	custom := map[string]bool{}
	if list, ok := result["custom"].([]any); ok {
		for _, c := range list {
			if name, ok := c.(string); ok {
				custom[name] = true
			}
		}
	}
	lines := []string{fmt.Sprintf("menu policy: %v", result["path"])}
	names := make([]string, 0, len(menuValue))
	for name := range menuValue {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var items []string
		if list, ok := menuValue[name].([]any); ok {
			for _, item := range list {
				items = append(items, fmt.Sprint(item))
			}
		}
		line := fmt.Sprintf("%s: %s", name, strings.Join(items, ", "))
		if custom[name] {
			line += " (custom)"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func formatEngines(engines []any) string {
	lines := make([]string, 0, len(engines))
	for _, item := range engines {
		engine, _ := item.(map[string]any)
		line := fmt.Sprintf("%v: available", engine["id"])
		if available, _ := engine["available"].(bool); !available {
			line = fmt.Sprintf("%v: unavailable", engine["id"])
			if reason, _ := engine["reason"].(string); reason != "" {
				line += ": " + reason
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func formatHumanEvent(message wireEvent) string {
	if message.Data.ID == "" {
		return message.Event
	}
	if message.Event == "job.updated" {
		return fmt.Sprintf("job %s %s: %.0f%%", message.Data.ID, message.Data.State, message.Data.Progress*100)
	}
	line := fmt.Sprintf("job %s %s", message.Data.ID, message.Data.State)
	for _, file := range message.Data.Files {
		if file.Error != nil {
			line += fmt.Sprintf("\n  %s: %s", file.Source, file.Error.Message)
		} else if file.State == "skipped" && file.Notice != "" {
			line += fmt.Sprintf("\n  skipped %s: %s", file.Source, file.Notice)
		}
	}
	return line
}

// absolutePath resolves a command-line path against the working directory; the
// daemon only accepts absolute paths. Empty and unresolvable paths pass through.
func absolutePath(path string) string {
	if path == "" {
		return path
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func absolutePaths(paths []string) []string {
	out := make([]string, len(paths))
	for i, path := range paths {
		out[i] = absolutePath(path)
	}
	return out
}

func raw(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func usage() {
	fmt.Fprintln(os.Stderr, "usage: fcctl hello | engines [--json] | targets FILE... [--menu] [--json] | menu show | menu set CATEGORY FORMAT... | menu reset [CATEGORY] | convert FILE... (--to png|jpeg|webp|jxl|avif|gif|bmp|tiff|ico|heic | --preset PRESET) [--quality 1-100] [--max-size PX] [--background COLOR] [--out-dir DIR] [--recursive] [--on-conflict POLICY] [--json] | cancel JOB [--json]")
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
