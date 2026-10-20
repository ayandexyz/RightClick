package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	job        string
	jsonOutput bool
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
		case "--output-dir":
			value, next, err := optionValue(args, i)
			if err != nil {
				return command{}, err
			}
			cmd.outputDir, i = value, next
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
			case "cancel":
				cmd.job = args[i]
			default:
				cmd.files = append(cmd.files, args[i])
			}
		}
	}
	if cmd.name != "hello" && cmd.name != "targets" && cmd.name != "convert" && cmd.name != "cancel" {
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
	if cmd.name == "cancel" && cmd.job == "" {
		return command{}, fmt.Errorf("job id is required")
	}
	if cmd.name != "convert" && (cmd.outputDir != "" || cmd.preset != "") {
		return command{}, fmt.Errorf("conversion options require convert")
	}
	return cmd, nil
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
	case "targets":
		return protocol.Request{ID: 1, Method: "targets", Params: raw(map[string]any{"files": cmd.files})}
	case "cancel":
		return protocol.Request{ID: 1, Method: "cancel", Params: raw(map[string]any{"job": cmd.job})}
	default:
		return protocol.Request{ID: 1, Method: "convert", Params: raw(map[string]any{"files": cmd.files, "preset": cmd.preset, "output_dir": cmd.outputDir, "on_conflict": cmd.conflict})}
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
		return fmt.Sprintf("result: %v", result)
	}
	return fmt.Sprintf("%v", value)
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
		}
	}
	return line
}

func raw(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func usage() {
	fmt.Fprintln(os.Stderr, "usage: fcctl hello | targets FILE... [--json] | convert FILE... (--to png|jpeg|webp|jxl | --preset PRESET) [--output-dir DIR] [--on-conflict POLICY] [--json] | cancel JOB [--json]")
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
