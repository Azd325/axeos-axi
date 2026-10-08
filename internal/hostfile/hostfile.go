package hostfile

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Azd325/axeos-axi/internal/axeos"
)

const maxBytes = 2048

var (
	ErrUnreadable = errors.New("could not be read")
	ErrMalformed  = errors.New("does not hold one address in the form that `host save` writes")
)

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "axeos-axi", "host"), nil
}

// Read returns the saved host, or "" when the file does not exist.
func Read(path string) (string, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", ErrUnreadable
	}
	defer func() { _ = f.Close() }()
	content, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return "", ErrUnreadable
	}
	host := strings.TrimSuffix(string(content), "\n")
	if !valid(host) {
		return "", ErrMalformed
	}
	return host, nil
}

func valid(host string) bool {
	client, err := axeos.New(host)
	return err == nil && client.Base() == host && len(host) < maxBytes
}

// Write stores the base address of a client (axeos.Client.Base).
func Write(path, host string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".host-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.WriteString(host + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = ""
	return nil
}

func Remove(path string) (removed bool, err error) {
	err = os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}
