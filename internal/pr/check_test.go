package pr

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckWithFakeGH(t *testing.T) {
	dir := t.TempDir()
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\necho '{\"state\":\"OPEN\",\"isDraft\":false,\"mergedAt\":null}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := Check(context.Background(), "https://github.com/o/r/pull/1")
	if err != nil || got != Open {
		t.Fatalf("%s %v", got, err)
	}
}
