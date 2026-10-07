package skill

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

const Name = "axeos-axi"

//go:embed SKILL.md
var content []byte

func Bytes() []byte { return bytes.Clone(content) }

func Install(path string) (written bool, err error) {
	if existing, readErr := os.ReadFile(path); readErr == nil && bytes.Equal(existing, content) {
		return false, nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(dir, ".axeos-axi-skill-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return false, err
	}
	tmpName = ""
	return true, nil
}
