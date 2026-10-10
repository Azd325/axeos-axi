package app

import (
	"errors"
	"io/fs"
	"os"
	"regexp"
	"strings"
)

const (
	codexConfigDisplayPath = "~/.codex/config.toml"

	codexHooksEnabled          = "enabled"
	codexHooksAdded            = "added"
	codexHooksChangedFromFalse = "changed_from_false"
	codexHooksDisabled         = "disabled"
	codexHooksMissing          = "missing"
	codexHooksUnverifiable     = "unverifiable"
)

var (
	tomlHooksBool        = regexp.MustCompile(`^(\s*hooks\s*=\s*)(true|false)(\s*(?:#.*)?)$`)
	tomlDottedHooks      = regexp.MustCompile(`^(\s*features\s*\.\s*hooks\s*=\s*)(true|false)(\s*(?:#.*)?)$`)
	tomlHooksKey         = regexp.MustCompile(`^\s*["']?hooks["']?\s*[.=]`)
	tomlFeaturesKey      = regexp.MustCompile(`^\s*["']?features["']?\s*[.=]`)
	errCodexConfigUnsafe = errors.New(codexConfigDisplayPath + " cannot be edited safely; set [features].hooks = true there manually")
)

type codexHooksUpdate struct {
	Content string
	State   string
}

type tomlLine struct{ text, eol string }

type codexConfigScan struct {
	lines         []tomlLine
	hooksLine     int
	hooksValue    string
	hooksPattern  *regexp.Regexp
	featuresFound bool
	featuresLast  int
}

func readCodexConfig(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, codexConfigFileError("read", err)
	}
	return data, nil
}

func codexConfigFileError(op string, cause error) error {
	if errors.Is(cause, fs.ErrPermission) {
		return errors.New("permission denied for " + codexConfigDisplayPath)
	}
	return errors.New("cannot " + op + " " + codexConfigDisplayPath)
}

func splitTOMLLines(content string) []tomlLine {
	parts := strings.SplitAfter(content, "\n")
	lines := make([]tomlLine, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		line := tomlLine{text: part}
		switch {
		case strings.HasSuffix(part, "\r\n"):
			line = tomlLine{text: strings.TrimSuffix(part, "\r\n"), eol: "\r\n"}
		case strings.HasSuffix(part, "\n"):
			line = tomlLine{text: strings.TrimSuffix(part, "\n"), eol: "\n"}
		}
		lines = append(lines, line)
	}
	return lines
}

func parseTOMLTableHeader(text string) (keys []string, array, ok bool) {
	rest := strings.TrimSpace(text)
	if array = strings.HasPrefix(rest, "[["); array {
		rest = rest[2:]
	} else {
		rest = rest[1:]
	}
	for {
		rest = strings.TrimLeft(rest, " \t")
		var key string
		switch {
		case rest == "":
			return nil, false, false
		case rest[0] == '"' || rest[0] == '\'':
			end := closingTOMLQuote(rest)
			if end < 0 {
				return nil, false, false
			}
			key, rest = rest[1:end], rest[end+1:]
		default:
			end := strings.IndexFunc(rest, func(r rune) bool {
				return (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-'
			})
			if end <= 0 {
				return nil, false, false
			}
			key, rest = rest[:end], rest[end:]
		}
		keys = append(keys, key)
		rest = strings.TrimLeft(rest, " \t")
		if strings.HasPrefix(rest, ".") {
			rest = rest[1:]
			continue
		}
		break
	}
	closer := "]"
	if array {
		closer = "]]"
	}
	rest, found := strings.CutPrefix(rest, closer)
	rest = strings.TrimSpace(rest)
	return keys, array, found && (rest == "" || strings.HasPrefix(rest, "#"))
}

func closingTOMLQuote(text string) int {
	for i := 1; i < len(text); i++ {
		switch {
		case text[0] == '"' && text[i] == '\\':
			i++
		case text[i] == text[0]:
			return i
		}
	}
	return -1
}

func lexTOMLLine(text, open string) (string, int) {
	depth := 0
	for i := 0; i < len(text); {
		if open != "" {
			switch {
			case open == `"""` && text[i] == '\\':
				i += 2
			case strings.HasPrefix(text[i:], open):
				open = ""
				i += 3
			default:
				i++
			}
			continue
		}
		switch c := text[i]; {
		case strings.HasPrefix(text[i:], `"""`) || strings.HasPrefix(text[i:], `'''`):
			open = text[i : i+3]
			i += 3
		case c == '"' || c == '\'':
			end := closingTOMLQuote(text[i:])
			if end < 0 {
				return open, depth
			}
			i += end + 1
		case c == '#':
			return open, depth
		case c == '[':
			depth++
			i++
		case c == ']':
			depth--
			i++
		default:
			i++
		}
	}
	return open, depth
}

func scanCodexConfig(content string) (codexConfigScan, error) {
	scan := codexConfigScan{lines: splitTOMLLines(content), hooksLine: -1, featuresLast: -1}
	if strings.HasPrefix(content, "\uFEFF") {
		return scan, errCodexConfigUnsafe
	}
	inFeatures, inTable := false, false
	open, arrayDepth := "", 0
	for i, line := range scan.lines {
		text := line.text
		trimmed := strings.TrimSpace(text)
		if open == "" && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
			continue
		}
		if open != "" || arrayDepth > 0 {
			var delta int
			open, delta = lexTOMLLine(text, open)
			arrayDepth = max(0, arrayDepth+delta)
			if inFeatures {
				scan.featuresLast = i
			}
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			keys, array, ok := parseTOMLTableHeader(text)
			if !ok {
				return scan, errCodexConfigUnsafe
			}
			if len(keys) >= 2 && keys[0] == "features" && keys[1] == "hooks" {
				return scan, errCodexConfigUnsafe
			}
			inTable = true
			inFeatures = len(keys) == 1 && keys[0] == "features"
			if inFeatures {
				if array || scan.featuresFound {
					return scan, errCodexConfigUnsafe
				}
				scan.featuresFound = true
				scan.featuresLast = i
			}
			continue
		}
		var delta int
		open, delta = lexTOMLLine(text, "")
		arrayDepth = max(0, delta)
		if inFeatures {
			scan.featuresLast = i
			if tomlHooksKey.MatchString(text) {
				if err := scan.recordHooks(i, text, tomlHooksBool); err != nil {
					return scan, err
				}
			}
			continue
		}
		if tomlFeaturesKey.MatchString(text) && !inTable {
			if err := scan.recordHooks(i, text, tomlDottedHooks); err != nil {
				return scan, err
			}
		}
	}
	return scan, nil
}

func (scan *codexConfigScan) recordHooks(index int, text string, pattern *regexp.Regexp) error {
	match := pattern.FindStringSubmatch(text)
	if match == nil || scan.hooksLine >= 0 {
		return errCodexConfigUnsafe
	}
	scan.hooksLine, scan.hooksValue, scan.hooksPattern = index, match[2], pattern
	return nil
}

func codexHooksState(content string) string {
	scan, err := scanCodexConfig(content)
	switch {
	case err != nil:
		return codexHooksUnverifiable
	case scan.hooksLine < 0:
		return codexHooksMissing
	case scan.hooksValue == "true":
		return codexHooksEnabled
	}
	return codexHooksDisabled
}

func enableCodexHooks(content string) (codexHooksUpdate, error) {
	scan, err := scanCodexConfig(content)
	if err != nil {
		return codexHooksUpdate{}, err
	}
	newline := "\n"
	for _, line := range scan.lines {
		if line.eol != "" {
			newline = line.eol
			break
		}
	}
	switch {
	case scan.hooksLine >= 0 && scan.hooksValue == "true":
		return codexHooksUpdate{Content: content, State: codexHooksEnabled}, nil
	case scan.hooksLine >= 0:
		line := &scan.lines[scan.hooksLine]
		match := scan.hooksPattern.FindStringSubmatch(line.text)
		line.text = match[1] + "true" + match[3]
		return codexHooksUpdate{Content: joinTOMLLines(scan.lines), State: codexHooksChangedFromFalse}, nil
	case scan.featuresFound:
		return codexHooksUpdate{Content: insertTOMLLine(scan.lines, scan.featuresLast+1, "hooks = true", newline), State: codexHooksAdded}, nil
	case strings.TrimSpace(content) == "":
		return codexHooksUpdate{Content: "[features]" + newline + "hooks = true" + newline, State: codexHooksAdded}, nil
	}
	lines := append([]tomlLine(nil), scan.lines...)
	if lines[len(lines)-1].eol == "" {
		lines[len(lines)-1].eol = newline
	}
	if strings.TrimSpace(lines[len(lines)-1].text) != "" {
		lines = append(lines, tomlLine{eol: newline})
	}
	lines = append(lines, tomlLine{text: "[features]", eol: newline})
	return codexHooksUpdate{Content: insertTOMLLine(lines, len(lines), "hooks = true", newline), State: codexHooksAdded}, nil
}

func insertTOMLLine(lines []tomlLine, index int, text, newline string) string {
	out := append([]tomlLine(nil), lines...)
	if index > 0 && out[index-1].eol == "" {
		out[index-1].eol = newline
	}
	out = append(out[:index], append([]tomlLine{{text: text, eol: newline}}, out[index:]...)...)
	return joinTOMLLines(out)
}

func joinTOMLLines(lines []tomlLine) string {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line.text)
		b.WriteString(line.eol)
	}
	return b.String()
}
