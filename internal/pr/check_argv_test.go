package pr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRecordingGH installs a fake gh at dir/gh that records each argv element
// (one per line) to argvFile, then emits a valid OPEN-PR JSON so Check succeeds.
func writeRecordingGH(t *testing.T, dir, argvFile string) string {
	t.Helper()
	gh := filepath.Join(dir, "gh")
	script := "#!/bin/sh\n" +
		": > " + argvFile + "\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> " + argvFile + "; done\n" +
		"echo '{\"state\":\"OPEN\",\"isDraft\":false,\"mergedAt\":null}'\n"
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return gh
}

func readArgv(t *testing.T, argvFile string) []string {
	t.Helper()
	data, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimRight(string(data), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func TestCheckPassesURLAfterEndOfOptionsSeparator(t *testing.T) {
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	gh := writeRecordingGH(t, dir, argvFile)
	t.Setenv("UAM_GH_BIN", gh)

	const url = "https://github.com/o/r/pull/1"
	got, err := Check(context.Background(), url)
	if err != nil || got != Open {
		t.Fatalf("Check: got %s err %v", got, err)
	}

	argv := readArgv(t, argvFile)
	if len(argv) == 0 {
		t.Fatal("fake gh recorded no argv")
	}
	if argv[len(argv)-1] != url {
		t.Fatalf("url must be the last arg; got argv=%v", argv)
	}
	if len(argv) < 2 || argv[len(argv)-2] != "--" {
		t.Fatalf("`--` must immediately precede the url; got argv=%v", argv)
	}
}

func TestCheckTreatsFlagLikeURLAsPositional(t *testing.T) {
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	gh := writeRecordingGH(t, dir, argvFile)
	t.Setenv("UAM_GH_BIN", gh)

	// A URL-shaped string whose host segment begins with '-'. It still matches
	// the github PR shape but would be parsed as a flag by gh without `--`.
	const url = "https://github.com/-o/r/pull/1"
	if _, err := Check(context.Background(), url); err != nil {
		t.Fatalf("Check: %v", err)
	}

	argv := readArgv(t, argvFile)
	sep := -1
	for i, a := range argv {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep == -1 {
		t.Fatalf("expected `--` separator; got argv=%v", argv)
	}
	if argv[len(argv)-1] != url {
		t.Fatalf("flag-like url must be delivered after `--` as the last arg; got argv=%v", argv)
	}
}

func TestCheckRejectsNonGitHubURL(t *testing.T) {
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	gh := writeRecordingGH(t, dir, argvFile)
	t.Setenv("UAM_GH_BIN", gh)

	if _, err := Check(context.Background(), "https://evil.example.com/o/r/pull/1"); err == nil {
		t.Fatal("non-github URL should be rejected before exec")
	}
	if _, err := os.Stat(argvFile); !os.IsNotExist(err) {
		t.Fatalf("fake gh must not be invoked for a rejected URL (argv file exists: %v)", err)
	}
}
