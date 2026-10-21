package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"file-converter/backend/internal/pdfops"
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
	// op carries a PDF operation ID for the run_operation method, or the
	// literal "pdf-list" when the user asked for a list.
	op       string
	pdfPages string
	pdfAngle *int
	pdfList  bool
	pdfInfo  bool
	// pdfThumbs carries "pdf thumbs", whose page list is parsed here rather
	// than by the daemon, because the CLI reports the pages it asked for.
	pdfThumbs       bool
	pdfWidth        int
	pdfEdit         string
	pdfRanges       string
	pdfEvery        *int
	pdfPagesFor     []string
	pdfPasswordFile string
	pdfEncryptUser  string
	pdfEncryptOwner string
	pdfPermissions  []string
	// pdfOpts holds the stamp, crop, resize and layout options by flag name,
	// still as typed on the command line. They are converted per operation by
	// pdfToolParams, because the same flag (--right, --margin) means different
	// things to different operations.
	pdfOpts map[string]string
	// cacheSub is "clear" for the cache command; other cache subcommands are
	// rejected rather than ignored.
	cacheSub string
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
		// convert and pdf run a job, so wait for job.finished; everything else
		// answers with a single result and is done.
		waitsForJob := cmd.name == "convert" || (cmd.name == "pdf" && !cmd.pdfList && !cmd.pdfInfo && !cmd.pdfThumbs)
		if !waitsForJob {
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
	if args[0] == "pdf" {
		return parsePDFArgs(args)
	}
	if args[0] == "cache" {
		return parseCacheArgs(args)
	}
	return parseCommonArgs(args)
}

func parseCommonArgs(args []string) (command, error) {
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
	if cmd.name != "hello" && cmd.name != "engines" && cmd.name != "menu" && cmd.name != "targets" && cmd.name != "convert" && cmd.name != "cancel" && cmd.name != "cache" {
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

// parsePDFArgs handles `fcctl pdf <op> file.pdf ...`. Mapping of the subcommand
// name to a protocol operation ID happens here; everything else is identical to
// the convert flow.
func parsePDFArgs(args []string) (command, error) {
	if len(args) < 2 {
		return command{}, fmt.Errorf("pdf <command> --help")
	}
	cmd := command{name: "pdf", conflict: "rename"}
	sub := args[1]
	// `list` and `info` are queries, not catalogue operations: they ask the
	// daemon about the selection rather than changing it.
	if sub == "list" {
		cmd.op, cmd.pdfList = "pdf-list", true
	} else if sub == "info" {
		cmd.op, cmd.pdfInfo = "pdf-info", true
	} else if sub == "thumbs" {
		cmd.op, cmd.pdfThumbs = "pdf-thumbs", true
	} else {
		op, ok := pdfCommandByShortName(sub)
		if !ok {
			return command{}, fmt.Errorf("unknown pdf command: %s; valid: %s", sub, strings.Join(pdfShortNames(), " "))
		}
		cmd.op = op
	}
	rest := args[2:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--json":
			cmd.jsonOutput = true
		case "--output-dir", "--out-dir":
			value, next, err := optionValue(rest, i)
			if err != nil {
				return command{}, err
			}
			cmd.outputDir, i = value, next
		case "--on-conflict":
			value, next, err := optionValue(rest, i)
			if err != nil {
				return command{}, err
			}
			cmd.conflict, i = value, next
		case "--pages-for":
			value, next, err := optionValue(rest, i)
			if err != nil {
				return command{}, err
			}
			cmd.pdfPagesFor = append(cmd.pdfPagesFor, value)
			i = next
		case "--pages":
			value, next, err := optionValue(rest, i)
			if err != nil {
				return command{}, err
			}
			cmd.pdfPages, i = value, next
		case "--ranges":
			value, next, err := optionValue(rest, i)
			if err != nil {
				return command{}, err
			}
			cmd.pdfRanges, i = value, next
		case "--every":
			value, next, err := optionValue(rest, i)
			if err != nil {
				return command{}, err
			}
			n, err := strconv.Atoi(value)
			if err != nil {
				return command{}, fmt.Errorf("--every must be an integer")
			}
			cmd.pdfEvery, i = &n, next
		case "--edit":
			value, next, err := optionValue(rest, i)
			if err != nil {
				return command{}, err
			}
			cmd.pdfEdit, i = value, next
		case "--width":
			value, next, err := optionValue(rest, i)
			if err != nil {
				return command{}, err
			}
			n, err := strconv.Atoi(value)
			if err != nil {
				return command{}, fmt.Errorf("--width must be an integer")
			}
			cmd.pdfWidth, i = n, next
		case "--angle":
			value, next, err := optionValue(rest, i)
			if err != nil {
				return command{}, err
			}
			n, err := strconv.Atoi(value)
			if err != nil || (n != 90 && n != 180 && n != 270) {
				return command{}, fmt.Errorf("--angle must be 90, 180 or 270")
			}
			cmd.pdfAngle = &n
			i = next
		case "--password-file":
			value, next, err := optionValue(rest, i)
			if err != nil {
				return command{}, err
			}
			password, err := readPasswordFile(value)
			if err != nil {
				return command{}, fmt.Errorf("read password file: %w", err)
			}
			cmd.pdfPasswordFile, i = password, next
		case "--user-password-file":
			value, next, err := optionValue(rest, i)
			if err != nil {
				return command{}, err
			}
			password, err := readPasswordFile(value)
			if err != nil {
				return command{}, fmt.Errorf("read user password file: %w", err)
			}
			cmd.pdfEncryptUser, i = password, next
		case "--owner-password-file":
			value, next, err := optionValue(rest, i)
			if err != nil {
				return command{}, err
			}
			password, err := readPasswordFile(value)
			if err != nil {
				return command{}, fmt.Errorf("read owner password file: %w", err)
			}
			cmd.pdfEncryptOwner, i = password, next
		case "--permissions":
			value, next, err := optionValue(rest, i)
			if err != nil {
				return command{}, err
			}
			cmd.pdfPermissions = strings.Split(value, ",")
			i = next
		default:
			if pdfToolBoolFlags[rest[i]] {
				cmd.setPDFOpt(rest[i], "true")
				continue
			}
			if pdfToolValueFlags[rest[i]] {
				value, next, err := optionValue(rest, i)
				if err != nil {
					return command{}, err
				}
				cmd.setPDFOpt(rest[i], value)
				i = next
				continue
			}
			if strings.HasPrefix(rest[i], "-") {
				return command{}, fmt.Errorf("unknown option: %s", rest[i])
			}
			cmd.files = append(cmd.files, rest[i])
		}
	}
	if cmd.pdfList {
		if len(cmd.files) != 1 {
			return command{}, fmt.Errorf("pdf list takes exactly one file")
		}
		return cmd, nil
	}
	if cmd.pdfInfo {
		if len(cmd.files) != 1 {
			return command{}, fmt.Errorf("pdf info takes exactly one file")
		}
		return cmd, nil
	}
	if cmd.pdfThumbs {
		if len(cmd.files) != 1 {
			return command{}, fmt.Errorf("pdf thumbs takes exactly one file")
		}
		if cmd.pdfPages == "" {
			return command{}, fmt.Errorf("pdf thumbs requires --pages")
		}
		// Parse here so a typo is a CLI error, not a round trip. The daemon
		// validates the same spec again against the real page count.
		if _, err := pdfops.ParsePages(cmd.pdfPages, thumbsMaxPages); err != nil {
			return command{}, fmt.Errorf("--pages: %w", err)
		}
		return cmd, nil
	}
	if _, err := pdfToolParams(cmd); err != nil {
		return command{}, err
	}
	if cmd.op == "pdf-split-ranges" && cmd.pdfRanges == "" {
		return command{}, fmt.Errorf("pdf split-ranges requires --ranges")
	}
	if cmd.op == "pdf-split-every" && cmd.pdfEvery == nil {
		return command{}, fmt.Errorf("pdf split-every requires --every")
	}
	if cmd.op == "pdf-edit" {
		if len(cmd.files) != 1 {
			return command{}, fmt.Errorf("pdf edit takes exactly one file")
		}
		if cmd.pdfEdit == "" {
			return command{}, fmt.Errorf("pdf edit requires --edit")
		}
		if _, err := parseEditSpec(cmd.pdfEdit); err != nil {
			return command{}, fmt.Errorf("--edit: %w", err)
		}
	}
	opDef, _ := pdfops.Lookup(cmd.op)
	if len(cmd.files) == 0 {
		return command{}, fmt.Errorf("at least one file is required")
	}
	if opDef.MinInputs >= 2 && len(cmd.files) < opDef.MinInputs {
		return command{}, fmt.Errorf("pdf %s needs at least %d files", sub, opDef.MinInputs)
	}
	if opDef.NeedsPages && cmd.pdfPages == "" {
		return command{}, fmt.Errorf("pdf %s requires --pages", sub)
	}
	if opDef.NeedsAngle && opDef.FixedAngle == 0 && cmd.pdfAngle == nil {
		return command{}, fmt.Errorf("pdf %s requires --angle", sub)
	}
	if !opDef.NeedsAngle && cmd.pdfAngle != nil {
		return command{}, fmt.Errorf("--angle is only valid for pdf rotate")
	}
	// The stamp, crop and resize operations take --pages inside their own
	// params, which pdfToolParams places and checks.
	if !opDef.NeedsPages && !opDef.OptionalPages && cmd.pdfPages != "" && !pdfToolTakesPages(cmd.op) {
		return command{}, fmt.Errorf("--pages is only valid for pdf extract, delete or rotate and the stamp, crop and resize tools")
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
	case "cache":
		return protocol.Request{ID: 1, Method: "cache_clear", Params: raw(map[string]any{})}
	case "cancel":
		return protocol.Request{ID: 1, Method: "cancel", Params: raw(map[string]any{"job": cmd.job})}
	case "pdf":
		files := absolutePaths(cmd.files)
		if cmd.pdfList {
			return protocol.Request{ID: 1, Method: "operations", Params: raw(map[string]any{"files": files})}
		}
		if cmd.pdfInfo {
			return protocol.Request{ID: 1, Method: "pdf_info", Params: raw(map[string]any{"file": files[0]})}
		}
		if cmd.pdfThumbs {
			// The spec was already parsed during argument validation, so a
			// failure here would mean the arguments changed underneath us.
			pages, err := pdfops.ParsePages(cmd.pdfPages, thumbsMaxPages)
			if err != nil {
				return protocol.Request{ID: 1, Method: "pdf_thumbnails", Params: raw(nil)}
			}
			return protocol.Request{ID: 1, Method: "pdf_thumbnails", Params: raw(map[string]any{
				"file": files[0], "pages": pages, "width": cmd.pdfWidth,
			})}
		}
		params := map[string]any{
			"op":          cmd.op,
			"files":       files,
			"output_dir":  absolutePath(cmd.outputDir),
			"on_conflict": cmd.conflict,
		}
		if len(cmd.pdfPagesFor) > 0 {
			filePages := make([]string, len(files))
			for _, spec := range cmd.pdfPagesFor {
				parts := strings.SplitN(spec, "=", 2)
				if len(parts) != 2 {
					return protocol.Request{ID: 1, Method: "run_operation", Params: raw(nil)}
				}
				idx, err := strconv.Atoi(parts[0])
				if err != nil || idx < 1 || idx > len(filePages) {
					return protocol.Request{ID: 1, Method: "run_operation", Params: raw(nil)}
				}
				filePages[idx-1] = parts[1]
			}
			params["params"] = map[string]any{"file_pages": filePages}
		} else if cmd.pdfEdit != "" {
			items, _ := parseEditSpec(cmd.pdfEdit)
			params["params"] = map[string]any{"edit": items}
		} else if cmd.pdfRanges != "" {
			params["params"] = map[string]any{"ranges": cmd.pdfRanges}
		} else if cmd.pdfEvery != nil {
			params["params"] = map[string]any{"every": *cmd.pdfEvery}
		} else if toolParams, _ := pdfToolParams(cmd); toolParams != nil {
			params["params"] = toolParams
		} else if cmd.pdfPages != "" || cmd.pdfAngle != nil || cmd.pdfPasswordFile != "" || cmd.pdfEncryptUser != "" || cmd.pdfEncryptOwner != "" || len(cmd.pdfPermissions) > 0 {
			operation := map[string]any{}
			if cmd.pdfPages != "" {
				operation["pages"] = cmd.pdfPages
			}
			if cmd.pdfAngle != nil {
				operation["angle"] = *cmd.pdfAngle
			}
			if cmd.pdfPasswordFile != "" {
				operation["password"] = cmd.pdfPasswordFile
			}
			// Crypto params for pdf-encrypt
			if cmd.pdfEncryptUser != "" {
				operation["encrypt_user"] = cmd.pdfEncryptUser
			}
			if cmd.pdfEncryptOwner != "" {
				operation["encrypt_owner"] = cmd.pdfEncryptOwner
			}
			// Crypto params for pdf-permissions
			if len(cmd.pdfPermissions) > 0 {
				operation["permissions_owner"] = cmd.pdfEncryptOwner
				operation["permissions"] = cmd.pdfPermissions
			}
			params["params"] = operation
		}
		return protocol.Request{ID: 1, Method: "run_operation", Params: raw(params)}
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

// readPasswordFile reads a password from a file or stdin. Never accepts
// password as a command-line flag to avoid shell history and /proc visibility.
func readPasswordFile(path string) (string, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", err
	}
	// Trim trailing newline that text editors add
	password := strings.TrimSuffix(string(data), "\n")
	password = strings.TrimSuffix(password, "\r\n")
	return password, nil
}

func writeRequest(conn net.Conn, req protocol.Request) error {
	data, err := protocol.EncodeMessage(req)
	if err != nil {
		return err
	}
	_, err = conn.Write(data)
	return err
}

// formatResult renders a successful result. Which method produced it is not
// passed in, so the shape decides: pdf_thumbnails returns a list of page
// objects, pdf_info returns a count, and cache_clear returns a tally.
func formatResult(result map[string]any) string {
	if removed, ok := result["removed"]; ok {
		return fmt.Sprintf("removed %v cached thumbnails", removed)
	}
	pages, ok := result["pages"]
	if !ok {
		return fmt.Sprintf("result: %v", result)
	}
	if list, isList := pages.([]any); isList {
		return formatThumbnails(list)
	}
	return formatPDFInfo(result, pages)
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
		if operations, ok := result["operations"].([]any); ok {
			return formatOperations(operations)
		}
		return formatResult(result)
	}
	return fmt.Sprintf("%v", value)
}

// formatThumbnails prints one line per thumbnail. The path is the point of the
// command: a caller wants the files, not a summary.
func formatThumbnails(pages []any) string {
	lines := make([]string, 0, len(pages))
	for _, item := range pages {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		page, w, h := 0, 0.0, 0.0
		if v, ok := entry["page"].(float64); ok {
			page = int(v)
		}
		if v, ok := entry["w"].(float64); ok {
			w = v
		}
		if v, ok := entry["h"].(float64); ok {
			h = v
		}
		lines = append(lines, fmt.Sprintf("%d\t%dx%d\t%s", page, int(w), int(h), entry["path"]))
	}
	return strings.Join(lines, "\n")
}

// formatPDFInfo prints a document's shape for a human reading a terminal. The
// page count always prints; per-page sizes are omitted entirely when pdfinfo was
// unavailable, rather than shown as zeroes that would be mistaken for real
// measurements.
func formatPDFInfo(result map[string]any, pages any) string {
	count, _ := pages.(float64)
	if _, isInt := pages.(int); isInt {
		count = float64(pages.(int))
	}
	lines := []string{fmt.Sprintf("pages: %d", int(count))}
	sizes, ok := result["sizes"].([]any)
	if !ok || len(sizes) == 0 {
		return strings.Join(lines, "\n")
	}
	for _, item := range sizes {
		size, ok := item.(map[string]any)
		if !ok {
			continue
		}
		w, h := size["w"].(float64), size["h"].(float64)
		rot := 0
		if r, ok := size["rot"].(float64); ok {
			rot = int(r)
		}
		line := fmt.Sprintf("  %.0f x %.0f pts", w, h)
		if rot != 0 {
			line += fmt.Sprintf(" rotated %d", rot)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
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

func formatOperations(operations []any) string {
	if len(operations) == 0 {
		return "no PDF operations apply to this selection"
	}
	lines := make([]string, 0, len(operations))
	for _, item := range operations {
		entry, _ := item.(map[string]any)
		line := fmt.Sprintf("%v: %v", entry["id"], entry["label"])
		if available, _ := entry["enabled"].(bool); !available {
			line += " (unavailable)"
			if reason, _ := entry["reason"].(string); reason != "" {
				line += ": " + reason
			}
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

const usageText = "usage: fcctl hello | engines [--json] | targets FILE... [--menu] [--json] | menu show | menu set CATEGORY FORMAT... | menu reset [CATEGORY] | convert FILE... (--to png|jpeg|webp|jxl|avif|gif|bmp|tiff|ico|heic | --preset PRESET) [--quality 1-100] [--max-size PX] [--background COLOR] [--out-dir DIR] [--recursive] [--on-conflict POLICY] [--json] | cancel JOB [--json] | pdf (merge FILE FILE... [--pages-for N=SPEC]... | extract FILE... --pages SPEC | delete FILE... --pages SPEC | rotate FILE... --angle 90|180|270 [--pages SPEC] | reverse FILE... | edit FILE --edit ITEMS | split-ranges FILE --ranges SPEC;SPEC... | split-every FILE --every N | decrypt FILE --password-file FILE | encrypt FILE --user-password-file FILE --owner-password-file FILE | permissions FILE --owner-password-file FILE --permissions LIST | stamp-text FILE --text TEXT [--position P] [--opacity 0-100] [--rotation DEG] [--size PT] [--color #rrggbb] [--on-top] [--pages SPEC] | stamp-image FILE --image FILE [--position P] [--opacity 0-100] [--rotation DEG] [--pages SPEC] | page-numbers FILE [--format number|of-total] [--start N] [--position P] [--pages SPEC] | header-footer FILE [--left T] [--centre T] [--right T] [--edge top|bottom] [--pages SPEC] | bates FILE --start N [--prefix TEXT] [--position P] [--pages SPEC] | crop FILE --unit mm|pt|percent [--top N] [--right N] [--bottom N] [--left N] [--pages SPEC] | resize FILE --to A4|A3|A5|Letter|Legal [--pages SPEC] | nup FILE --per-sheet N [--orientation portrait|landscape] [--margin N] [--border] | booklet FILE --per-sheet 2|4 | metadata FILE [--title TEXT] [--author TEXT] [--subject TEXT] [--keywords TEXT] | NAME FILE... | list FILE | info FILE | thumbs FILE --pages SPEC) [--width PX] [--out-dir DIR] [--on-conflict POLICY] [--json] | cache clear [--json]"

func usage()          { fmt.Fprintln(os.Stderr, usageText) }
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

// pdfShortName is the CLI verb for an operation ID, derived by stripping the
// "pdf-" prefix and well-known longer names that do not lend themselves to a
// straight strip (extract-images, from-images, to-text, compress-balanced).
func pdfShortName(id string) string {
	short := strings.TrimPrefix(id, "pdf-")
	switch id {
	case "pdf-extract-images":
		return "extract-images"
	case "pdf-from-images":
		return "from-images"
	case "pdf-to-text":
		return "to-text"
	case "pdf-compress-light":
		return "compress-light"
	case "pdf-compress-balanced":
		return "compress-balanced"
	case "pdf-compress-strong":
		return "compress-strong"
	case "pdf-rotate-cw":
		return "rotate-cw"
	case "pdf-rotate-ccw":
		return "rotate-ccw"
	case "pdf-rotate-180":
		return "rotate-180"
	}
	return short
}

// pdfCommandByShortName maps a CLI verb back to the operation ID. Recognising
// both `compress` (an alias for `compress-balanced`) and the long names makes
// the CLI forgiving when invoked by hand.
func pdfCommandByShortName(name string) (string, bool) {
	if name == "compress" {
		if _, ok := pdfops.Lookup("pdf-compress-balanced"); ok {
			return "pdf-compress-balanced", true
		}
	}
	for _, op := range pdfops.Catalogue() {
		if pdfShortName(op.ID) == name {
			return op.ID, true
		}
	}
	return "", false
}

// pdfShortNames lists the CLI verbs the CLI accepts, in catalogue order, for
// the error message printed when a user types an unknown one.
func pdfShortNames() []string {
	ops := pdfops.Catalogue()
	names := make([]string, 0, len(ops)+2)
	names = append(names, "list")
	for _, op := range ops {
		names = append(names, pdfShortName(op.ID))
	}
	return names
}

// parseEditSpec parses the compact CLI form: 3,1r90,b,3,4. A number is a
// source page, rN is its clockwise rotation delta, and b is a blank page.
func parseEditSpec(spec string) ([]protocol.PageItem, error) {
	parts := strings.Split(spec, ",")
	if len(parts) == 0 || (len(parts) == 1 && strings.TrimSpace(parts[0]) == "") {
		return nil, fmt.Errorf("edit list cannot be empty")
	}
	items := make([]protocol.PageItem, 0, len(parts))
	for _, raw := range parts {
		token := strings.TrimSpace(raw)
		if token == "b" {
			items = append(items, protocol.PageItem{})
			continue
		}
		rotate := 0
		if n := strings.Index(token, "r"); n >= 0 {
			value := token[n+1:]
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("%q has an invalid rotation", token)
			}
			rotate = parsed
			token = token[:n]
		}
		src, err := strconv.Atoi(token)
		if err != nil || src < 1 {
			return nil, fmt.Errorf("%q must be a positive page number, b, or page rN", raw)
		}
		if rotate != 0 && rotate != 90 && rotate != 180 && rotate != 270 {
			return nil, fmt.Errorf("rotation must be 90, 180 or 270")
		}
		items = append(items, protocol.PageItem{Src: src, Rotate: rotate})
	}
	if len(items) > 1000 {
		return nil, fmt.Errorf("edit list may contain at most 1000 items")
	}
	return items, nil
}

// thumbsMaxPages is the page count the CLI parses a --pages spec against when
// the daemon's own limit has not been consulted yet. The daemon re-validates
// against the real page count, so this only has to be generous enough not to
// reject a legitimate request.
const thumbsMaxPages = 1000

// parseCacheArgs handles "cache clear". Only clearing is offered: inspecting or
// sizing the cache is a diagnostic nobody asked for, and a half-implemented
// command is worse than a missing one.
func parseCacheArgs(args []string) (command, error) {
	if len(args) < 2 || args[1] != "clear" {
		return command{}, errors.New("usage: fcctl cache clear")
	}
	cmd := command{name: "cache"}
	for _, arg := range args[2:] {
		switch arg {
		case "--json":
			cmd.jsonOutput = true
		default:
			return command{}, fmt.Errorf("unknown option for cache clear: %s", arg)
		}
	}
	return cmd, nil
}

// Options for the stamp, crop, resize and layout operations. They are listed
// rather than parsed generically so a typo is an "unknown option" error and not
// a silently ignored flag.
var pdfToolValueFlags = map[string]bool{
	"--text": true, "--image": true, "--position": true, "--opacity": true, "--rotation": true,
	"--size": true, "--color": true, "--start": true, "--format": true, "--prefix": true,
	"--left": true, "--centre": true, "--right": true, "--edge": true,
	"--unit": true, "--top": true, "--bottom": true,
	"--to":        true,
	"--per-sheet": true, "--orientation": true, "--margin": true,
}

var pdfToolBoolFlags = map[string]bool{"--on-top": true, "--border": true}

func (c *command) setPDFOpt(flag, value string) {
	if c.pdfOpts == nil {
		c.pdfOpts = map[string]string{}
	}
	c.pdfOpts[flag] = value
}

// pdfToolParams turns the stamp, crop, resize and layout options into the
// params object for the command's operation. It returns nil for any other
// operation, and an error naming the flag when a value is not the type the
// operation needs.
func pdfToolParams(cmd command) (map[string]any, error) {
	group := ""
	switch cmd.op {
	case "pdf-stamp-text", "pdf-stamp-image", "pdf-page-numbers", "pdf-header-footer", "pdf-bates":
		group = "stamp"
	case "pdf-crop":
		group = "crop"
	case "pdf-resize":
		group = "resize"
	case "pdf-nup", "pdf-booklet":
		group = "layout"
	default:
		if len(cmd.pdfOpts) > 0 {
			return nil, fmt.Errorf("%s does not take %s", strings.TrimPrefix(cmd.op, "pdf-"), firstFlag(cmd.pdfOpts))
		}
		return nil, nil
	}
	// Which flags each group accepts, and how to read them.
	type spec struct{ key, kind string }
	allowed := map[string]map[string]spec{
		"stamp": {
			"--text": {"text", "s"}, "--image": {"image", "s"}, "--position": {"position", "s"},
			"--opacity": {"opacity", "i"}, "--rotation": {"rotation", "i"}, "--size": {"size", "i"},
			"--color": {"color", "s"}, "--start": {"start", "i"}, "--format": {"format", "s"},
			"--prefix": {"prefix", "s"}, "--left": {"left", "s"}, "--centre": {"centre", "s"},
			"--right": {"right", "s"}, "--edge": {"edge", "s"}, "--on-top": {"on_top", "b"},
		},
		"crop": {
			"--unit": {"unit", "s"}, "--top": {"top", "f"}, "--right": {"right", "f"},
			"--bottom": {"bottom", "f"}, "--left": {"left", "f"},
		},
		"resize": {"--to": {"to", "s"}},
		"layout": {
			"--per-sheet": {"per_sheet", "i"}, "--orientation": {"orientation", "s"},
			"--margin": {"margin", "i"}, "--border": {"border", "b"},
		},
	}[group]
	values := map[string]any{}
	for flag, raw := range cmd.pdfOpts {
		s, ok := allowed[flag]
		if !ok {
			return nil, fmt.Errorf("%s does not take %s", strings.TrimPrefix(cmd.op, "pdf-"), flag)
		}
		switch s.kind {
		case "i":
			n, err := strconv.Atoi(raw)
			if err != nil {
				return nil, fmt.Errorf("%s must be a whole number", flag)
			}
			values[s.key] = n
		case "f":
			n, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, fmt.Errorf("%s must be a number", flag)
			}
			values[s.key] = n
		case "b":
			values[s.key] = true
		default:
			values[s.key] = raw
		}
	}
	// --pages selects the pages the tool acts on, which these operations take
	// inside their own group rather than as the generic page selection.
	if cmd.pdfPages != "" && group != "layout" {
		values["pages"] = cmd.pdfPages
	}
	if cmd.pdfPages != "" && group == "layout" {
		return nil, fmt.Errorf("%s does not take --pages", strings.TrimPrefix(cmd.op, "pdf-"))
	}
	return map[string]any{group: values}, nil
}

func firstFlag(opts map[string]string) string {
	flags := make([]string, 0, len(opts))
	for flag := range opts {
		flags = append(flags, flag)
	}
	sort.Strings(flags)
	return flags[0]
}

func pdfToolTakesPages(op string) bool {
	switch op {
	case "pdf-stamp-text", "pdf-stamp-image", "pdf-page-numbers", "pdf-header-footer", "pdf-bates", "pdf-crop", "pdf-resize":
		return true
	}
	return false
}
