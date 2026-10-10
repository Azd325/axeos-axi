package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	sessionHookMarker        = "axeos-axi-session-hook"
	openCodePluginDependency = "^1.18.30"
	defaultSessionTimeout    = time.Second
	sessionCommandTail       = " session dashboard 2>/dev/null || true # " + sessionHookMarker

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
}

type ownerRecord struct {
	Command            string `json:"command"`
	ShapeHash          string `json:"shape_hash"`
	OpenCodeDependency string `json:"opencode_dependency,omitempty"`
}

type hookLocation struct {
	agent        string
	path         string
	manifestPath string
	markerPath   string
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
		{Name: "firmware", Value: text(info, "version")},
		{Name: "help", Value: hints},
	})
	return 0
}

func (a *App) sessionHost() string {
	if host := a.getenv("AXEOS_HOST"); host != "" {
		return host
	}
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
	for _, agent := range agents {
		result, err := a.setupAgent(opts.action, agent)
		if err != nil {
			return failure(stdout, 1, "setup_failed", agent+": "+err.Error(), "axeos-axi setup check --agent "+agent)
		}
		results = append(results, output.Object{
			{Name: "agent", Value: result.agent},
			{Name: "state", Value: result.state},
			{Name: "path", Value: result.path},
		})
	}
	return write(stdout, output.Object{{Name: "setup", Value: results}})
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
		location.markerPath = filepath.Join(home, ".claude", "."+sessionHookMarker)
	case "codex":
		location.path = filepath.Join(home, ".codex", "hooks.json")
		location.markerPath = filepath.Join(home, ".codex", "."+sessionHookMarker)
	case "opencode":
		location.path = filepath.Join(home, ".config", "opencode", "plugins", "axeos-axi.ts")
		location.manifestPath = filepath.Join(home, ".config", "opencode", "package.json")
		location.markerPath = filepath.Join(home, ".config", "opencode", "plugins", "."+sessionHookMarker)
	}
	owner, err := readOwner(location.markerPath)
	if err != nil {
		return setupResult{}, err
	}
	command := ""
	if action == "install" {
		command, err = a.sessionCommand()
		if err != nil {
			return setupResult{}, err
		}
	}
	var result setupResult
	if agent == "opencode" {
		result, err = manageOpenCodePlugin(action, location, command, owner)
	} else {
		result, err = manageHookJSON(action, location, command, owner)
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

func manageHookJSON(action string, location hookLocation, command string, owner ownerRecord) (setupResult, error) {
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
	managed := make([]int, 0, 1)
	for i, entry := range entries {
		if isManagedHook(entry, owner) {
			managed = append(managed, i)
		}
	}
	if len(managed) > 1 {
		return setupResult{}, errors.New("multiple managed axeos-axi session hooks found; refuse ambiguous configuration")
	}
	state := "missing"
	if len(managed) == 1 {
		state = "installed"
	}
	switch action {
	case "check":
		return setupResult{agent: location.agent, state: state}, nil
	case "install":
		if owner.ShapeHash == "" && hasSessionMarker(entries) {
			return setupResult{}, errors.New("managed session hook has no owner record; refuse ambiguous configuration")
		}
		entry := map[string]any{"matcher": "", "hooks": []any{map[string]any{"type": "command", "command": command}}}
		if len(managed) == 1 {
			entries[managed[0]] = entry
		} else {
			entries = append(entries, entry)
		}
		state = "installed"
	case "uninstall":
		if len(managed) == 0 {
			removeMarker(location.markerPath)
			return setupResult{agent: location.agent, state: "missing"}, nil
		}
		entries = append(entries[:managed[0]], entries[managed[0]+1:]...)
		state = "removed"
	}
	hooks["SessionStart"] = entries
	var shapeHash string
	if action == "install" {
		if len(managed) == 1 {
			shapeHash, _ = hookShapeHash(entries[managed[0]])
		} else {
			shapeHash, _ = hookShapeHash(entries[len(entries)-1])
		}
	}
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return setupResult{}, err
	}
	if err := atomicWrite(location.path, append(encoded, '\n'), 0o600); err != nil {
		return setupResult{}, err
	}
	switch action {
	case "uninstall":
		removeMarker(location.markerPath)
	case "install":
		if err := writeOwner(location.markerPath, ownerRecord{Command: command, ShapeHash: shapeHash}); err != nil {
			if restoreErr := restoreFile(location.path, data, len(data) > 0); restoreErr != nil {
				return setupResult{}, fmt.Errorf("write managed session owner: %w; rollback configuration: %v", err, restoreErr)
			}
			return setupResult{}, err
		}
	}
	return setupResult{agent: location.agent, state: state}, nil
}

func isManagedHook(value any, owner ownerRecord) bool {
	entry, ok := value.(map[string]any)
	if !ok || len(entry) != 2 || entry["matcher"] != "" {
		return false
	}
	hooks, ok := entry["hooks"].([]any)
	if !ok || len(hooks) != 1 {
		return false
	}
	hook, ok := hooks[0].(map[string]any)
	if !ok || len(hook) != 2 || hook["type"] != "command" {
		return false
	}
	command, ok := hook["command"].(string)
	shapeHash, valid := hookShapeHash(entry)
	return ok && valid && shapeHash == owner.ShapeHash && isManagedCommand(command, owner)
}

func isManagedCommand(command string, owner ownerRecord) bool {
	return owner.ShapeHash != "" && (command == owner.Command || isSessionCommand(command))
}

func isSessionCommand(command string) bool {
	suffix := "'" + sessionCommandTail
	if !strings.HasPrefix(command, "'") || !strings.HasSuffix(command, suffix) {
		return false
	}
	path := strings.TrimSuffix(strings.TrimPrefix(command, "'"), suffix)
	return shellQuote(path)+sessionCommandTail == command
}

func hookShapeHash(value any) (string, bool) {
	entry, ok := value.(map[string]any)
	if !ok || entry["matcher"] != "" {
		return "", false
	}
	hooks, ok := entry["hooks"].([]any)
	if !ok || len(hooks) != 1 {
		return "", false
	}
	hook, ok := hooks[0].(map[string]any)
	if !ok || hook["type"] != "command" {
		return "", false
	}
	normalized := map[string]any{"matcher": "", "hooks": []any{map[string]any{"type": "command", "command": sessionHookMarker}}}
	data, err := json.Marshal(normalized)
	return contentHash(data), err == nil
}

func hasSessionMarker(entries []any) bool {
	for _, entry := range entries {
		value, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hooks, ok := value["hooks"].([]any)
		if !ok {
			continue
		}
		for _, hook := range hooks {
			item, ok := hook.(map[string]any)
			if !ok {
				continue
			}
			command, ok := item["command"].(string)
			if ok && strings.Contains(command, "# "+sessionHookMarker) {
				return true
			}
		}
	}
	return false
}

func readJSONObject(path string) (map[string]any, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	root := map[string]any{}
	if json.Unmarshal(data, &root) != nil || root == nil {
		return nil, data, errors.New("OpenCode package.json is not a JSON object")
	}
	return root, data, nil
}

func openCodeDependencyVersion(path string) string {
	root, _, err := readJSONObject(path)
	if err != nil {
		return ""
	}
	dependencies, _ := root["dependencies"].(map[string]any)
	version, _ := dependencies["@opencode-ai/plugin"].(string)
	return version
}

func ensureOpenCodeDependency(path string) (bool, error) {
	root := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if len(data) > 0 && (json.Unmarshal(data, &root) != nil || root == nil) {
		return false, errors.New("OpenCode package.json is not a JSON object")
	}
	dependencies, ok := root["dependencies"].(map[string]any)
	if !ok {
		if _, exists := root["dependencies"]; exists {
			return false, errors.New("OpenCode package.json has incompatible dependencies")
		}
		dependencies = map[string]any{}
		root["dependencies"] = dependencies
	}
	if dependency, exists := dependencies["@opencode-ai/plugin"]; exists {
		if _, ok := dependency.(string); ok {
			return false, nil
		}
		return false, errors.New("OpenCode package.json has incompatible @opencode-ai/plugin dependency")
	}
	dependencies["@opencode-ai/plugin"] = openCodePluginDependency
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return false, err
	}
	if err := atomicWrite(path, append(encoded, '\n'), 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func removeOwnedOpenCodeDependency(path string, owner ownerRecord) error {
	if owner.OpenCodeDependency == "" {
		return nil
	}
	root, _, err := readJSONObject(path)
	if err != nil {
		return nil
	}
	dependencies, ok := root["dependencies"].(map[string]any)
	if !ok || dependencies["@opencode-ai/plugin"] != owner.OpenCodeDependency {
		return nil
	}
	delete(dependencies, "@opencode-ai/plugin")
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(encoded, '\n'), 0o600)
}

func manageOpenCodePlugin(action string, location hookLocation, command string, owner ownerRecord) (setupResult, error) {
	data, err := os.ReadFile(location.path)
	if err != nil && !os.IsNotExist(err) {
		return setupResult{}, err
	}
	managed := isManagedOpenCodePlugin(data, owner)
	if len(data) > 0 && !managed {
		return setupResult{}, errors.New("OpenCode plugin path is occupied by unmanaged content")
	}
	switch action {
	case "check":
		state := "missing"
		if managed && openCodeDependencyVersion(location.manifestPath) != "" {
			state = "installed"
		}
		return setupResult{agent: location.agent, state: state}, nil
	case "uninstall":
		if !managed {
			return setupResult{agent: location.agent, state: "missing"}, nil
		}
		if err := removeOwnedOpenCodeDependency(location.manifestPath, owner); err != nil {
			return setupResult{}, err
		}
		if err := os.Remove(location.path); err != nil {
			return setupResult{}, err
		}
		removeMarker(location.markerPath)
		return setupResult{agent: location.agent, state: "removed"}, nil
	}
	manifestData, manifestErr := os.ReadFile(location.manifestPath)
	manifestExists := manifestErr == nil
	if manifestErr != nil && !os.IsNotExist(manifestErr) {
		return setupResult{}, manifestErr
	}
	dependencyAdded, err := ensureOpenCodeDependency(location.manifestPath)
	if err != nil {
		return setupResult{}, err
	}
	plugin := openCodePluginPrefix + jsonStringCommand(command) + openCodePluginSuffix
	if err := atomicWrite(location.path, []byte(plugin), 0o600); err != nil {
		if restoreErr := restoreFile(location.manifestPath, manifestData, manifestExists); restoreErr != nil {
			return setupResult{}, fmt.Errorf("write plugin: %w; rollback manifest: %v", err, restoreErr)
		}
		return setupResult{}, err
	}
	_, shapeHash, _ := openCodePluginParts([]byte(plugin))
	dependencyOwner := ""
	if dependencyAdded || owner.OpenCodeDependency == openCodePluginDependency && openCodeDependencyVersion(location.manifestPath) == openCodePluginDependency {
		dependencyOwner = openCodePluginDependency
	}
	if err := writeOwner(location.markerPath, ownerRecord{Command: command, ShapeHash: shapeHash, OpenCodeDependency: dependencyOwner}); err != nil {
		pluginErr := restoreFile(location.path, data, len(data) > 0)
		manifestErr := restoreFile(location.manifestPath, manifestData, manifestExists)
		if pluginErr != nil || manifestErr != nil {
			return setupResult{}, fmt.Errorf("write managed session owner: %w; rollback plugin: %v; rollback manifest: %v", err, pluginErr, manifestErr)
		}
		return setupResult{}, err
	}
	return setupResult{agent: location.agent, state: "installed"}, nil
}

func isManagedOpenCodePlugin(data []byte, owner ownerRecord) bool {
	command, shapeHash, ok := openCodePluginParts(data)
	return ok && shapeHash == owner.ShapeHash && isManagedCommand(command, owner)
}

func openCodePluginParts(data []byte) (string, string, bool) {
	content := string(data)
	if !strings.HasPrefix(content, openCodePluginPrefix) || !strings.HasSuffix(content, openCodePluginSuffix) {
		return "", "", false
	}
	encodedCommand := strings.TrimSuffix(strings.TrimPrefix(content, openCodePluginPrefix), openCodePluginSuffix)
	var command string
	if json.Unmarshal([]byte(encodedCommand), &command) != nil {
		return "", "", false
	}
	normalized := openCodePluginPrefix + jsonStringCommand("") + openCodePluginSuffix
	return command, contentHash([]byte(normalized)), true
}

func jsonStringCommand(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
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

func readOwner(path string) (ownerRecord, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		var owner ownerRecord
		if json.Unmarshal(data, &owner) != nil || owner.Command == "" || owner.ShapeHash == "" {
			return ownerRecord{}, nil
		}
		return owner, nil
	}
	if os.IsNotExist(err) {
		return ownerRecord{}, nil
	}
	return ownerRecord{}, err
}

func writeOwner(path string, owner ownerRecord) error {
	data, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), 0o600)
}

func contentHash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func removeMarker(path string) {
	_ = os.Remove(path)
}

func restoreFile(path string, data []byte, existed bool) error {
	if !existed {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return atomicWrite(path, data, 0o600)
}

func setupHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "setup"},
		{Name: "description", Value: "Installs, checks or removes an opt-in SessionStart hook that runs `axeos-axi session dashboard` at the start of each agent session; nothing else writes the hook; it needs no host, sends no request and reads no miner; a repeated install with the same executable path changes nothing, and an install from another path repairs the path; uninstall removes only the hook that install wrote, recorded by an owner marker next to it, and leaves the hooks of other tools unchanged"},
		{Name: "actions", Value: output.Object{
			{Name: "install", Value: "writes the hook; state installed"},
			{Name: "check", Value: "reads the hook; state installed or missing"},
			{Name: "uninstall", Value: "removes the owned hook; state removed, or missing when it was not installed"},
		}},
		{Name: "agents", Value: "claude: ~/.claude/settings.json; codex: ~/.codex/hooks.json; opencode: ~/.config/opencode/plugins/axeos-axi.ts and the @opencode-ai/plugin entry of ~/.config/opencode/package.json; all: the three"},
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
		{Name: "description", Value: "Prints the short view that the session hook shows at the start of an agent session: hashrate, temperature, power and firmware version, then command hints; it prints no pool user, payout address, hostname, address, MAC address or Wi-Fi name; with AXEOS_HOST or a saved host it sends one GET /api/system/info and no other request; without a host it sends no request and prints the hints only; when the miner does not answer, or answers with an error or a malformed answer, it prints the line `miner not reachable`, without a host or address, and the hints; it always exits with code 0"},
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
