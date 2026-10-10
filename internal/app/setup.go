package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azd325/axeos-axi/internal/axeos"
	"github.com/Azd325/axeos-axi/internal/hostfile"
	"github.com/Azd325/axeos-axi/internal/output"
)

const (
	sessionHookMarker     = "axeos-axi-session-hook"
	defaultSessionTimeout = time.Second
	sessionCommandTail    = " session dashboard 2>/dev/null || true # " + sessionHookMarker

	setupUsage   = "usage: axeos-axi setup install|check|uninstall --agent claude|codex|opencode|all"
	sessionUsage = "usage: axeos-axi session dashboard"

	openCodePluginPrefix = "// " + sessionHookMarker + "\nimport { execFileSync } from \"node:child_process\";\nimport type { Plugin } from \"@opencode-ai/plugin\";\n\nconst injected = new Set<string>();\n\nexport const AxeosAxiPlugin: Plugin = async () => ({\n  \"experimental.chat.system.transform\": async (input, output) => {\n    if (input.sessionID && injected.has(input.sessionID)) return;\n    let context = \"\";\n    try {\n      context = execFileSync("
	openCodePluginSuffix = ", { encoding: \"utf8\", shell: true, timeout: 5000, stdio: [\"ignore\", \"pipe\", \"ignore\"] });\n    } catch {\n      return;\n    }\n    if (input.sessionID) injected.add(input.sessionID);\n    output.system.push(context);\n  },\n});\n\nexport default AxeosAxiPlugin;\n"
)

var (
	setupActions  = []string{"install", "check", "uninstall"}
	setupAgentIDs = []string{"claude", "codex", "opencode"}
)

type setupResult struct {
	agent, state, path string
	codexHooks         string
}

type hookLocation struct {
	agent      string
	path       string
	configPath string
}

func (a *App) session(ctx context.Context, opts options, stdout io.Writer) int {
	hints := []any{"axeos-axi for the home view of the miner", "axeos-axi health for a verdict", "axeos-axi --help for the commands"}
	host := a.sessionHost()
	if host == "" {
		hints = []any{"axeos-axi host save <address> to save a default host", "axeos-axi discover to find miners on the local network", "axeos-axi --help for the commands"}
		_ = write(stdout, output.Object{{Name: "help", Value: hints}})
		return 0
	}
	info, ok := a.sessionRead(ctx, host)
	if !ok {
		hints = []any{"axeos-axi for the home view of the miner", "axeos-axi discover to find miners on the local network", "axeos-axi host show for the saved host"}
		if opts.json {
			_ = write(stdout, output.Object{{Name: "miner", Value: "not reachable"}, {Name: "help", Value: hints}})
			return 0
		}
		_, _ = fmt.Fprintln(stdout, "miner not reachable")
		_ = write(stdout, output.Object{{Name: "help", Value: hints}})
		return 0
	}
	_ = write(stdout, output.Object{
		{Name: "hashrate", Value: measure(info, "hashRate", "GH/s")},
		{Name: "temperature", Value: measure(info, "temp", "C")},
		{Name: "power", Value: measure(info, "power", "W")},
		{Name: "firmware", Value: sessionVersion(info)},
		{Name: "help", Value: hints},
	})
	return 0
}

func sessionVersion(info map[string]any) string {
	version, _ := info["version"].(string)
	if version == "" || len(version) > 32 {
		return "unknown"
	}
	for _, r := range version {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_', r == '+':
		default:
			return "unknown"
		}
	}
	return version
}

func (a *App) sessionHost() string {
	path, err := hostfile.Path()
	if err != nil {
		return ""
	}
	saved, err := hostfile.Read(path)
	if err != nil {
		return ""
	}
	return saved
}

func (a *App) sessionRead(ctx context.Context, host string) (map[string]any, bool) {
	client, err := axeos.New(host)
	if err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, a.sessionTimeout)
	defer cancel()
	info, err := client.Get(ctx, "info")
	return info, err == nil
}

func (a *App) setup(opts options, stdout io.Writer) int {
	agents := setupAgentIDs
	if opts.agent != "all" {
		agents = []string{opts.agent}
	}
	results := make([]any, 0, len(agents))
	codexHooks := ""
	for _, agent := range agents {
		result, err := a.setupAgent(opts.action, agent)
		if err != nil {
			return failure(stdout, 1, "setup_failed", agent+": "+a.tildeHome(err.Error()), "axeos-axi setup check --agent "+agent)
		}
		results = append(results, output.Object{
			{Name: "agent", Value: result.agent},
			{Name: "state", Value: result.state},
			{Name: "path", Value: result.path},
		})
		if result.codexHooks != "" {
			codexHooks = result.codexHooks
		}
	}
	view := output.Object{{Name: "setup", Value: results}}
	if codexHooks != "" {
		view = append(view, output.Field{Name: "codex_hooks_feature", Value: codexHooks})
	}
	return write(stdout, view)
}

func (a *App) tildeHome(message string) string {
	home, err := a.homeDir()
	if err != nil || home == "" {
		return message
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil && resolved != home {
		message = strings.ReplaceAll(message, resolved, "~")
	}
	return strings.ReplaceAll(message, home, "~")
}

func (a *App) setupAgent(action, agent string) (setupResult, error) {
	home, err := a.homeDir()
	if err != nil {
		return setupResult{}, fmt.Errorf("resolve home directory: %w", err)
	}
	location := hookLocation{agent: agent}
	switch agent {
	case "claude":
		location.path = filepath.Join(home, ".claude", "settings.json")
	case "codex":
		location.path = filepath.Join(home, ".codex", "hooks.json")
		location.configPath = filepath.Join(home, ".codex", "config.toml")
	case "opencode":
		location.path = filepath.Join(home, ".config", "opencode", "plugins", "axeos-axi.ts")
	}
	command := ""
	if action == "install" {
		command, err = a.sessionCommand()
		if err != nil {
			return setupResult{}, err
		}
	}
	var result setupResult
	switch agent {
	case "opencode":
		result, err = manageOpenCodePlugin(action, location, command)
	case "codex":
		result, err = manageCodexHook(action, location, command)
	default:
		result, err = manageHookJSON(action, location, command)
	}
	result.path = "~" + strings.TrimPrefix(location.path, home)
	return result, err
}

func (a *App) sessionCommand() (string, error) {
	path, err := a.executable()
	if err != nil || path == "" {
		return "", errors.New("resolve axeos-axi executable")
	}
	return shellQuote(path) + sessionCommandTail, nil
}

func manageCodexHook(action string, location hookLocation, command string) (setupResult, error) {
	var config []byte
	var feature codexHooksUpdate
	if action == "install" {
		var err error
		if config, err = readCodexConfig(location.configPath); err != nil {
			return setupResult{}, err
		}
		if feature, err = enableCodexHooks(string(config)); err != nil {
			return setupResult{}, err
		}
	}
	result, err := manageHookJSON(action, location, command)
	if err != nil {
		return setupResult{}, err
	}
	switch action {
	case "install":
		if feature.Content != string(config) {
			if err := atomicWrite(location.configPath, []byte(feature.Content), 0o600); err != nil {
				return setupResult{}, codexConfigFileError("write", err)
			}
		}
		result.codexHooks = feature.State
	case "check":
		data, err := readCodexConfig(location.configPath)
		if err != nil {
			return setupResult{}, err
		}
		result.codexHooks = codexHooksState(string(data))
	}
	return result, nil
}

func manageHookJSON(action string, location hookLocation, command string) (setupResult, error) {
	root := map[string]any{}
	data, err := os.ReadFile(location.path)
	if err != nil && !os.IsNotExist(err) {
		return setupResult{}, err
	}
	if len(data) > 0 && json.Unmarshal(data, &root) != nil {
		return setupResult{}, errors.New("managed hook configuration is not valid JSON")
	}
	if root == nil {
		return setupResult{}, errors.New("managed hook configuration must be a JSON object")
	}
	hooksValue, hooksPresent := root["hooks"]
	if hooksPresent {
		if _, ok := hooksValue.(map[string]any); !ok {
			return setupResult{}, errors.New("managed hook configuration has incompatible hooks value")
		}
	} else {
		hooksValue = map[string]any{}
	}
	hooks := hooksValue.(map[string]any)
	root["hooks"] = hooks
	entriesValue, entriesPresent := hooks["SessionStart"]
	entries := []any(nil)
	if entriesPresent {
		var ok bool
		entries, ok = entriesValue.([]any)
		if !ok {
			return setupResult{}, errors.New("managed hook configuration has incompatible SessionStart value")
		}
	}
	hook := map[string]any{"type": "command", "command": command}
	rewritten, found, staleHook := rewriteOwnHooks(entries, hook, action == "install")
	state := "missing"
	if found > 0 {
		state = "installed"
		if staleHook {
			state = "stale"
		}
	}
	switch action {
	case "check":
		return setupResult{agent: location.agent, state: state}, nil
	case "install":
		if found == 0 {
			rewritten = append(rewritten, map[string]any{"matcher": "", "hooks": []any{hook}})
		}
		entries = rewritten
		state = "installed"
	case "uninstall":
		if found == 0 {
			return setupResult{agent: location.agent, state: "missing"}, nil
		}
		entries = rewritten
		state = "removed"
	}
	if action == "uninstall" && len(entries) == 0 {
		delete(hooks, "SessionStart")
		if len(hooks) == 0 {
			delete(root, "hooks")
		}
	} else {
		hooks["SessionStart"] = entries
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(root); err != nil {
		return setupResult{}, err
	}
	if err := atomicWrite(location.path, encoded.Bytes(), 0o600); err != nil {
		return setupResult{}, err
	}
	return setupResult{agent: location.agent, state: state}, nil
}

func rewriteOwnHooks(entries []any, hook map[string]any, replace bool) ([]any, int, bool) {
	out := make([]any, 0, len(entries)+1)
	found, stale, placed := 0, false, false
	for _, value := range entries {
		entry, ok := value.(map[string]any)
		items, itemsOK := entry["hooks"].([]any)
		if !ok || !itemsOK {
			out = append(out, value)
			continue
		}
		kept := make([]any, 0, len(items))
		own := 0
		for _, item := range items {
			command := ownHookCommand(item)
			if command == "" {
				kept = append(kept, item)
				continue
			}
			own++
			if program, _ := sessionProgram(command); program != "" && !fileExists(program) {
				stale = true
			}
			if replace && !placed {
				kept = append(kept, hook)
				placed = true
			}
		}
		found += own
		if own == 0 {
			out = append(out, value)
			continue
		}
		if len(kept) > 0 {
			entry["hooks"] = kept
			out = append(out, entry)
		}
	}
	return out, found, stale
}

func ownHookCommand(item any) string {
	hook, ok := item.(map[string]any)
	if !ok {
		return ""
	}
	command, _ := hook["command"].(string)
	if !strings.Contains(command, "# "+sessionHookMarker) {
		return ""
	}
	return command
}

func sessionProgram(command string) (string, bool) {
	suffix := "'" + sessionCommandTail
	if !strings.HasPrefix(command, "'") || !strings.HasSuffix(command, suffix) {
		return "", false
	}
	path := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(command, "'"), suffix), "'\"'\"'", "'")
	return path, shellQuote(path)+sessionCommandTail == command
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !os.IsNotExist(err)
}

func manageOpenCodePlugin(action string, location hookLocation, command string) (setupResult, error) {
	data, err := os.ReadFile(location.path)
	if err != nil && !os.IsNotExist(err) {
		return setupResult{}, err
	}
	own := strings.Contains(string(data), sessionHookMarker)
	if len(data) > 0 && !own {
		return setupResult{}, errors.New("OpenCode plugin path is occupied by unmanaged content")
	}
	switch action {
	case "check":
		state := "missing"
		if own {
			state = "installed"
			if program := openCodePluginProgram(data); program != "" && !fileExists(program) {
				state = "stale"
			}
		}
		return setupResult{agent: location.agent, state: state}, nil
	case "uninstall":
		if !own {
			return setupResult{agent: location.agent, state: "missing"}, nil
		}
		if err := os.Remove(location.path); err != nil {
			return setupResult{}, err
		}
		return setupResult{agent: location.agent, state: "removed"}, nil
	}
	plugin := openCodePluginPrefix + jsonStringCommand(command) + openCodePluginSuffix
	if err := atomicWrite(location.path, []byte(plugin), 0o600); err != nil {
		return setupResult{}, err
	}
	return setupResult{agent: location.agent, state: "installed"}, nil
}

func openCodePluginProgram(data []byte) string {
	content := string(data)
	if !strings.HasPrefix(content, openCodePluginPrefix) || !strings.HasSuffix(content, openCodePluginSuffix) {
		return ""
	}
	encodedCommand := strings.TrimSuffix(strings.TrimPrefix(content, openCodePluginPrefix), openCodePluginSuffix)
	var command string
	if json.Unmarshal([]byte(encodedCommand), &command) != nil {
		return ""
	}
	program, _ := sessionProgram(command)
	return program
}

func jsonStringCommand(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".axeos-axi-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func setupHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "setup"},
		{Name: "description", Value: "Installs, checks or removes an opt-in SessionStart hook that runs `axeos-axi session dashboard` at the start of each agent session; nothing else writes the hook; it needs no host, sends no request and reads no miner; a repeated install with the same executable path changes nothing, and an install from another path repairs the path or a stale hook; the hook that install writes is each hook whose command carries the text `axeos-axi-session-hook`, whatever its other fields; install replaces every such hook with one current hook, and uninstall removes every such hook and leaves the hooks of other tools, also in the same entry, unchanged; it writes no other file, except that install for codex also ensures `[features].hooks = true` in ~/.codex/config.toml, which Codex needs to run hooks: it changes only that setting, creates the file when it is missing, states `changed_from_false` when it changed a false value, refuses a file that it cannot edit safely without writing anything, and uninstall leaves the setting"},
		{Name: "actions", Value: output.Object{
			{Name: "install", Value: "writes the hook; state installed; for codex it also prints codex_hooks_feature: added, enabled or changed_from_false"},
			{Name: "check", Value: "reads the hook; state installed, missing, or stale when the program that the hook names no longer exists; install replaces a stale hook; for codex it also prints codex_hooks_feature: enabled, disabled, missing or unverifiable"},
			{Name: "uninstall", Value: "removes every hook that carries the session marker text; state removed, or missing when there is none"},
		}},
		{Name: "agents", Value: "claude: ~/.claude/settings.json; codex: ~/.codex/hooks.json and ~/.codex/config.toml; opencode: ~/.config/opencode/plugins/axeos-axi.ts; all: the three"},
		{Name: "flags", Value: output.Object{
			{Name: "agent", Value: "--agent claude|codex|opencode|all; required"},
			{Name: "json", Value: jsonFlagHelp},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: versionFlagHelp},
		}},
		{Name: "examples", Value: []any{"axeos-axi setup install --agent all", "axeos-axi setup check --agent claude", "axeos-axi setup uninstall --agent opencode"}},
	}
}

func sessionHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "session"},
		{Name: "description", Value: "Prints the short view that the session hook shows at the start of an agent session: hashrate, temperature, power and firmware version, then command hints; it prints no pool user, payout address, hostname, address, MAC address or Wi-Fi name; with a host saved by `host save` it sends one GET /api/system/info and no other request; without a host it sends no request and prints the hints only; when the miner does not answer, or answers with an error or a malformed answer, it prints the line `miner not reachable`, without a host or address, and the hints; it always exits with code 0"},
		{Name: "actions", Value: "dashboard"},
		{Name: "flags", Value: output.Object{
			{Name: "json", Value: jsonFlagHelp},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: versionFlagHelp},
		}},
		{Name: "timeout_s", Value: int(defaultSessionTimeout / time.Second)},
		{Name: "examples", Value: []any{"axeos-axi session dashboard", "axeos-axi session dashboard --json", "axeos-axi session --help"}},
	}
}
