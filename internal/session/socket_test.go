package session

import (
	"os"
	"testing"
)

func socketTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "uam-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("remove socket test directory: %v", err)
		}
	})
	return dir
}
