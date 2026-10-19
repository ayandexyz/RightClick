package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"file-converter/backend/internal/protocol"
)

func socketPath() (string, error) {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		return "", fmt.Errorf("XDG_RUNTIME_DIR is not set")
	}
	return filepath.Join(runtime, "file-converter", "fcd.sock"), nil
}

func main() {
	if len(os.Args) < 2 {
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
	var req protocol.Request
	switch os.Args[1] {
	case "hello":
		req = protocol.Request{ID: 1, Method: "hello", Params: raw(map[string]any{"client": "fcctl", "protocol": 1})}
	case "targets":
		req = protocol.Request{ID: 1, Method: "targets", Params: raw(map[string]any{"files": os.Args[2:]})}
	case "convert":
		req = convertRequest(os.Args[2:])
	case "cancel":
		if len(os.Args) != 3 {
			usage()
			os.Exit(2)
		}
		req = protocol.Request{ID: 1, Method: "cancel", Params: raw(map[string]any{"job": os.Args[2]})}
	default:
		usage()
		os.Exit(2)
	}
	data, err := protocol.EncodeMessage(req)
	if err != nil {
		fatal(err)
	}
	if _, err = conn.Write(data); err != nil {
		fatal(err)
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 4096), 8*1024*1024)
	for scanner.Scan() {
		var value any
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			fatal(err)
		}
		encoded, _ := json.Marshal(value)
		fmt.Println(string(encoded))
		if response, ok := value.(map[string]any); ok {
			if _, has := response["result"]; has {
				return
			}
			if _, has := response["error"]; has {
				return
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fatal(err)
	}
}

func convertRequest(args []string) protocol.Request {
	files := make([]string, 0, len(args))
	preset, output, conflict := "", "", "rename"
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--preset":
			if i+1 < len(args) {
				i++
				preset = args[i]
			}
		case "--output-dir":
			if i+1 < len(args) {
				i++
				output = args[i]
			}
		case "--on-conflict":
			if i+1 < len(args) {
				i++
				conflict = args[i]
			}
		default:
			files = append(files, args[i])
		}
	}
	return protocol.Request{ID: 1, Method: "convert", Params: raw(map[string]any{"files": files, "preset": preset, "output_dir": output, "on_conflict": conflict})}
}
func raw(value any) json.RawMessage { data, _ := json.Marshal(value); return data }
func usage() {
	fmt.Fprintln(os.Stderr, "usage: fcctl hello | targets FILE... | convert FILE... --preset PRESET [--output-dir DIR] [--on-conflict POLICY] | cancel JOB")
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
