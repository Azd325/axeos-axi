package app

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azd325/axeos-axi/internal/skill"
)

const (
	helpBeginMarker = "<!-- axeos-axi-help:begin -->"
	helpEndMarker   = "<!-- axeos-axi-help:end -->"
)

func noHost(string) string { return "" }

func TestSkillHelpNotStale(t *testing.T) {
	raw := string(skill.Bytes())
	begin := strings.Index(raw, helpBeginMarker+"\n")
	end := strings.Index(raw, helpEndMarker)
	if begin < 0 || end < begin {
		t.Fatalf("SKILL.md lacks well-formed %s and %s markers", helpBeginMarker, helpEndMarker)
	}
	block := raw[begin+len(helpBeginMarker)+1 : end]
	code, want := execute(t, New(noHost), "--help")
	if code != 0 || block != want {
		t.Fatalf("SKILL.md help block is stale; paste the output of axeos-axi --help between the markers\nwant:\n%s\ngot:\n%s", want, block)
	}
}

func parseSkillFrontmatter(raw []byte) (map[string]string, error) {
	text := string(raw)
	if !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("frontmatter must start with ---")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return nil, fmt.Errorf("frontmatter must end with ---")
	}
	fields := map[string]string{}
	for _, line := range strings.Split(text[4:4+end], "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			return nil, fmt.Errorf("frontmatter line %q is not key: value", line)
		}
		if _, dup := fields[key]; dup {
			return nil, fmt.Errorf("frontmatter key %s is repeated", key)
		}
		fields[key] = strings.TrimSpace(value)
	}
	if len(fields) != 2 || fields["name"] == "" || fields["description"] == "" {
		return nil, fmt.Errorf("frontmatter needs exactly non-empty name and description, got %v", fields)
	}
	return fields, nil
}

func TestSkillFrontmatter(t *testing.T) {
	fields, err := parseSkillFrontmatter(skill.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if fields["name"] != skill.Name {
		t.Fatalf("name = %q, want %q", fields["name"], skill.Name)
	}
}

func TestSkillFrontmatterRejectsInvalid(t *testing.T) {
	for _, raw := range []string{
		"name: axeos-axi\n",
		"---\nname: axeos-axi\n",
		"---\nname: axeos-axi\n---\n",
		"---\nname: axeos-axi\ndescription: \n---\n",
		"---\nname: axeos-axi\ndescription: d\nextra: v\n---\n",
		"---\nname: axeos-axi\nname: other\ndescription: d\n---\n",
	} {
		if _, err := parseSkillFrontmatter([]byte(raw)); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestSkillInstallLiteralTildePathIsAbsolute(t *testing.T) {
	setHome(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	code, out := execute(t, New(noHost), "skill", "install", "--path=~/.claude/skills")
	want := filepath.Join(cwd, "~", ".claude", "skills", "axeos-axi", "SKILL.md")
	resolved, _ := filepath.EvalSymlinks(cwd)
	if code != 0 || (!strings.Contains(out, "path: "+want) && !strings.Contains(out, "path: "+filepath.Join(resolved, "~", ".claude", "skills", "axeos-axi", "SKILL.md"))) {
		t.Fatalf("code=%d output=%s", code, out)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatal(err)
	}
}

func setHome(t *testing.T) string {
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func TestSkillInstallDefaultPath(t *testing.T) {
	home := setHome(t)
	code, out := execute(t, New(noHost), "skill", "install")
	target := filepath.Join(home, ".agents", "skills", "axeos-axi", "SKILL.md")
	if code != 0 || out != "skill:\n  path: ~/.agents/skills/axeos-axi/SKILL.md\n  written: true\n" {
		t.Fatalf("code=%d output=%s", code, out)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, skill.Bytes()) {
		t.Fatalf("installed bytes differ: %v", err)
	}
}

func TestSkillInstallPathAndIdempotence(t *testing.T) {
	home := setHome(t)
	dir := filepath.Join(t.TempDir(), "skills")
	target := filepath.Join(dir, "axeos-axi", "SKILL.md")
	code, out := execute(t, New(noHost), "skill", "install", "--path", dir)
	if code != 0 || !strings.Contains(out, "written: true") || !strings.Contains(out, "path: "+target) {
		t.Fatalf("code=%d output=%s", code, out)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	code, out = execute(t, New(noHost), "--path="+dir, "skill", "install")
	if code != 0 || !strings.Contains(out, "written: false") {
		t.Fatalf("second install: code=%d output=%s", code, out)
	}
	again, _ := os.Stat(target)
	if !again.ModTime().Equal(info.ModTime()) {
		t.Fatal("second install rewrote the file")
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); err == nil {
		t.Fatal("--path install wrote under the home directory")
	}
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 1 {
		t.Fatalf("temporary file left behind: %v", entries)
	}
}

func TestSkillInstallReplacesChangedFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "axeos-axi", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := execute(t, New(noHost), "skill", "install", "--path", dir)
	got, _ := os.ReadFile(target)
	if code != 0 || !strings.Contains(out, "written: true") || !bytes.Equal(got, skill.Bytes()) {
		t.Fatalf("code=%d output=%s", code, out)
	}
}

func TestSkillInstallFailureIsStructured(t *testing.T) {
	setHome(t)
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out := execute(t, New(noHost), "skill", "install", "--path", blocker)
	if code != 1 || !strings.Contains(out, "code: skill_install_failed") {
		t.Fatalf("code=%d output=%s", code, out)
	}
}

func TestSkillUsageErrorsWriteNothing(t *testing.T) {
	home := setHome(t)
	dir := t.TempDir()
	for _, args := range [][]string{
		{"skill"},
		{"skill", "remove"},
		{"skill", "install", "extra"},
		{"skill", "--path", dir},
		{"skill", "install", "--host", "192.0.2.10"},
		{"skill", "install", "--confirm", "--path", dir},
		{"skill", "install", "--fields", "a", "--path", dir},
		{"skill", "install", "--path", dir, "--path", dir},
		{"skill", "install", "--path"},
		{"info", "--path", dir},
		{"--path", dir},
	} {
		code, out := execute(t, New(noHost), args...)
		if code != 2 || !strings.Contains(out, "code: usage") {
			t.Errorf("%v: code=%d output=%s", args, code, out)
		}
	}
	for _, root := range []string{home, dir} {
		if entries, _ := os.ReadDir(root); len(entries) != 0 {
			t.Errorf("usage error wrote into %s: %v", root, entries)
		}
	}
}

func TestSkillHelp(t *testing.T) {
	home := setHome(t)
	for _, args := range [][]string{{"skill", "--help"}, {"skill", "install", "--help"}, {"skill", "--path", "d", "--help"}} {
		code, out := execute(t, New(noHost), args...)
		if code != 0 || !strings.HasPrefix(out, "command: skill\n") {
			t.Errorf("%v: code=%d output=%s", args, code, out)
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("help wrote into home: %v", entries)
	}
}
