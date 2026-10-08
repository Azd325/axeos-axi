package hostfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "axeos-axi", "host")
	if host, err := Read(path); host != "" || err != nil {
		t.Fatalf("no file: host=%q error=%v", host, err)
	}
	if removed, err := Remove(path); removed || err != nil {
		t.Fatalf("no file: removed=%v error=%v", removed, err)
	}
	for range 2 {
		if err := Write(path, "http://192.0.2.10"); err != nil {
			t.Fatalf("error=%v", err)
		}
	}
	if host, err := Read(path); host != "http://192.0.2.10" || err != nil {
		t.Fatalf("host=%q error=%v", host, err)
	}
	if content, _ := os.ReadFile(path); string(content) != "http://192.0.2.10\n" {
		t.Fatalf("file=%q", content)
	}
	if removed, err := Remove(path); !removed || err != nil {
		t.Fatalf("removed=%v error=%v", removed, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
}

func TestWriteRefusesAnAddressThatReadWouldRefuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "axeos-axi", "host")
	for _, host := range []string{"", "192.0.2.10", "http://192.0.2.10/", "http://192.0.2.10:80", "http://192.0.2.10\nhttp://192.0.2.11"} {
		if err := Write(path, host); !errors.Is(err, ErrMalformed) {
			t.Errorf("%q: error=%v", host, err)
		}
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("a refused address created the directory: %v", err)
	}
}
