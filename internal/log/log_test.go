package log

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoggerRotationAcrossProcesses(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprintf("concurrent=%t", concurrent), func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			t.Cleanup(cancel)
			type writer struct {
				input  io.WriteCloser
				output *bufio.Scanner
			}
			var writers []writer
			for i := range 3 {
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLogRotationProcess$")
				cmd.Env = append(os.Environ(), "UAM_LOG_TEST_PROCESS=1", "UAM_CACHE_DIR="+dir, fmt.Sprintf("UAM_LOG_TEST_WRITER=%d", i))
				input, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				output, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_ = input.Close()
					if err := cmd.Wait(); err != nil {
						t.Errorf("log writer: %v: %s", err, stderr.String())
					}
				})
				scanner := bufio.NewScanner(output)
				if !scanner.Scan() || scanner.Text() != "ready" {
					t.Fatal("writer failed to initialize")
				}
				writers = append(writers, writer{input, scanner})
			}
			ack := func(w writer) {
				t.Helper()
				if !w.output.Scan() || w.output.Text() != "written" {
					t.Fatal("writer failed to append")
				}
			}
			for range 5 {
				for _, w := range writers {
					if _, err := io.WriteString(w.input, "write\n"); err != nil {
						t.Fatal(err)
					}
					if !concurrent {
						ack(w)
					}
				}
				if concurrent {
					for _, w := range writers {
						ack(w)
					}
				}
			}
			for _, w := range writers {
				if err := w.input.Close(); err != nil {
					t.Fatal(err)
				}
			}
			var logs strings.Builder
			for i := range maxBackups + 1 {
				path := filepath.Join(dir, "uam.log")
				if i > 0 {
					path += fmt.Sprintf(".%d", i)
				}
				data, err := os.ReadFile(path)
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if len(data) > int(maxLogSize) {
					t.Errorf("%s size = %d, exceeds %d", path, len(data), maxLogSize)
				}
				logs.Write(data)
			}
			for i := range writers {
				for n := range 5 {
					marker := fmt.Sprintf("writer-%d-entry-%d:", i, n)
					if got := strings.Count(logs.String(), marker); got != 1 {
						t.Errorf("%s occurs %d times, want 1", marker, got)
					}
				}
			}
		})
	}
}

func TestLogRotationProcess(t *testing.T) {
	if os.Getenv("UAM_LOG_TEST_PROCESS") != "1" {
		return
	}
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})
	w := c.(io.Writer)
	fmt.Println("ready")
	scanner := bufio.NewScanner(os.Stdin)
	for n := 0; scanner.Scan(); n++ {
		marker := fmt.Sprintf("writer-%s-entry-%d:", os.Getenv("UAM_LOG_TEST_WRITER"), n)
		if _, err := w.Write([]byte(marker + strings.Repeat("x", (512<<10)-len(marker)-1) + "\n")); err != nil {
			t.Fatal(err)
		}
		fmt.Println("written")
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestInitAndLogging(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UAM_CACHE_DIR", dir)
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	Debug("debug")
	Info("info")
	Warn("warn")
	Error("error")
	if L() == nil {
		t.Fatal("nil logger")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "uam.log")); err != nil {
		t.Fatal(err)
	}
}

func TestInitError(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UAM_CACHE_DIR", filepath.Join(file, "child"))
	if _, err := Init(); err == nil {
		t.Fatal("expected init error")
	}
}

func TestInitEnforcesPrivateLogPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uam.log")
	if err := os.WriteFile(path, []byte("old log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UAM_CACHE_DIR", dir)
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("uam.log mode = %o, want 600", got)
	}
}

func TestInitRotatesOversizedLogAndRetainsThreePrivateBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uam.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, maxLogSize); err != nil {
		t.Fatal(err)
	}
	for i, body := range []string{"backup-one", "backup-two", "backup-three"} {
		if err := os.WriteFile(path+"."+string(rune('1'+i)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("UAM_CACHE_DIR", dir)
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	wantBodies := map[string]string{
		path + ".2": "backup-one",
		path + ".3": "backup-two",
	}
	for _, candidate := range []string{path, path + ".1", path + ".2", path + ".3"} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatalf("stat %s: %v", candidate, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s mode = %o, want 600", candidate, got)
		}
		if want, ok := wantBodies[candidate]; ok {
			got, err := os.ReadFile(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != want {
				t.Fatalf("%s = %q, want %q", candidate, got, want)
			}
		}
	}
	rotated, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Size() != maxLogSize {
		t.Fatalf("rotated size = %d, want %d", rotated.Size(), maxLogSize)
	}
}

func TestLoggerRotatesWhenActiveLogCrossesSizeLimit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UAM_CACHE_DIR", dir)
	c, err := Init()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})

	Info("fill", "data", strings.Repeat("x", int(maxLogSize-1024)))
	Info("after rotation", "marker", "new-log", "padding", strings.Repeat("y", 2048))

	rotated, err := os.ReadFile(filepath.Join(dir, "uam.log.1"))
	if err != nil {
		t.Fatalf("read rotated log: %v", err)
	}
	if !strings.Contains(string(rotated), "fill") {
		t.Fatal("rotated log does not contain the pre-limit entry")
	}
	current, err := os.ReadFile(filepath.Join(dir, "uam.log"))
	if err != nil {
		t.Fatalf("read current log: %v", err)
	}
	if !strings.Contains(string(current), "new-log") {
		t.Fatal("current log does not contain the post-rotation entry")
	}
}

func TestCacheDirFallbacks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("UAM_CACHE_DIR", "")
	t.Setenv("XDG_CACHE_HOME", dir)
	if got := cacheDir(); got != filepath.Join(dir, "uam") {
		t.Fatalf("%s", got)
	}
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", dir)
	if got := cacheDir(); got != filepath.Join(dir, ".cache", "uam") {
		t.Fatalf("home cache dir = %s", got)
	}
	UseStderr(nil)
	Debug("stderr fallback debug path")
}
