package hostfile

import (
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
