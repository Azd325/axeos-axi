package app

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azd325/axeos-axi/internal/output"
	"github.com/Azd325/axeos-axi/internal/skill"
)

const skillUsage = "usage: axeos-axi skill install [--path <directory>]"

func tildePath(path string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func installSkill(opts options, stdout io.Writer) int {
	dir := opts.path
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return failure(stdout, 1, "skill_install_failed", "could not determine the user home directory", "axeos-axi skill install --path <directory> with the agent skills parent directory")
		}
		dir = filepath.Join(home, ".agents", "skills")
	}
	path := filepath.Join(dir, skill.Name, "SKILL.md")
	written, err := skill.Install(path)
	if err != nil {
		return failure(stdout, 1, "skill_install_failed", "the skill file could not be written to "+tildePath(path), "check the directory and its permissions")
	}
	return write(stdout, output.Object{{Name: "skill", Value: output.Object{{Name: "path", Value: tildePath(path)}, {Name: "written", Value: written}}}})
}

func skillHelp() output.Object {
	return output.Object{
		{Name: "command", Value: "skill"},
		{Name: "description", Value: "Installs the agent skill (SKILL.md) that is built into the binary; it needs no host, sends no request and reads no miner; the default file is ~/.agents/skills/axeos-axi/SKILL.md; a repeated install with the same content writes nothing"},
		{Name: "actions", Value: "install"},
		{Name: "flags", Value: output.Object{
			{Name: "path", Value: "--path <directory>; parent directory of the axeos-axi directory; default ~/.agents/skills"},
			{Name: "help", Value: "--help; no network request"},
			{Name: "version", Value: "-v, -V, --version; bare version; no network request"},
		}},
		{Name: "examples", Value: []any{"axeos-axi skill install", "axeos-axi skill install --path ~/.claude/skills", "axeos-axi skill --help"}},
	}
}
