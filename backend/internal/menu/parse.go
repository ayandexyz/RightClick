package menu

import (
	"fmt"
	"strings"
)

// Parse reads the small TOML subset the policy file uses: `key = ["a", "b"]`
// arrays (optionally spanning lines), comments, and blank lines. Malformed
// syntax, non-string items and duplicate keys are errors; unknown categories
// and formats are ignored and duplicate formats collapsed.
func Parse(data []byte) (Policy, error) {
	p := Policy{}
	seenKeys := map[string]bool{}
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		line := strings.TrimSpace(stripComment(lines[i]))
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !ok || key == "" || strings.ContainsAny(key, " \t[]\"'.") {
			return nil, fmt.Errorf("line %d: expected `key = [..]`", lineNo)
		}
		for !strings.Contains(value, "]") && i+1 < len(lines) {
			i++
			value += " " + strings.TrimSpace(stripComment(lines[i]))
		}
		items, err := parseArray(value)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		if seenKeys[key] {
			return nil, fmt.Errorf("line %d: duplicate key %q", lineNo, key)
		}
		seenKeys[key] = true
		if !knownCategory(key) {
			continue
		}
		list := []string{}
		dup := map[string]bool{}
		for _, item := range items {
			if knownFormats[item] && !dup[item] {
				dup[item] = true
				list = append(list, item)
			}
		}
		p[key] = list
	}
	return p, nil
}

func parseArray(value string) ([]string, error) {
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil, fmt.Errorf("value must be an array of strings")
	}
	inner := strings.TrimSpace(value[1 : len(value)-1])
	var items []string
	for inner != "" {
		quote := inner[0]
		if quote != '"' && quote != '\'' {
			return nil, fmt.Errorf("array items must be quoted strings")
		}
		end := strings.IndexByte(inner[1:], quote)
		if end < 0 {
			return nil, fmt.Errorf("unterminated string")
		}
		item := inner[1 : 1+end]
		if strings.Contains(item, `\`) {
			return nil, fmt.Errorf("escape sequences are not supported")
		}
		items = append(items, item)
		inner = strings.TrimSpace(inner[end+2:])
		if inner == "" {
			break
		}
		if inner[0] != ',' {
			return nil, fmt.Errorf("expected comma between array items")
		}
		inner = strings.TrimSpace(inner[1:])
	}
	return items, nil
}

// stripComment drops a trailing `# ...` that is not inside a string.
func stripComment(line string) string {
	var quote byte
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return line[:i]
		}
	}
	return line
}
