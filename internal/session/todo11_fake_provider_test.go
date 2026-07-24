package session

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"

	"github.com/charmbracelet/x/term"

	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const todo11InputDelimiter = byte(0x1e)

var todo11ProviderExit = []byte("todo11-provider-exit")

type todo11ProviderEvent struct {
	Type   string   `json:"type"`
	TERM   string   `json:"term,omitempty"`
	Data   string   `json:"data,omitempty"`
	Signal string   `json:"signal,omitempty"`
	Cols   int      `json:"cols,omitempty"`
	Rows   int      `json:"rows,omitempty"`
	Modes  []string `json:"modes,omitempty"`
}

type todo11EventWriter struct {
	mu   sync.Mutex
	file *os.File
}

func (writer *todo11EventWriter) write(event todo11ProviderEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	_, err = writer.file.Write(append(data, '\n'))
	return err
}

func TestTodo11FakeProviderProcess(t *testing.T) {
	if os.Getenv("UAM_TODO11_FAKE_PROVIDER") != "1" {
		t.Skip("helper process")
	}
	report, err := os.OpenFile(os.Getenv("UAM_TODO11_PROVIDER_REPORT"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = report.Close() }()
	writer := &todo11EventWriter{file: report}
	output, err := hex.DecodeString(os.Getenv("UAM_TODO11_PROVIDER_OUTPUT_HEX"))
	if err != nil {
		t.Fatal(err)
	}
	oldState, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = term.Restore(os.Stdin.Fd(), oldState) }()
	cols, rows, err := term.GetSize(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.write(todo11ProviderEvent{
		Type: "startup", TERM: os.Getenv("TERM"), Cols: cols, Rows: rows,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stdout.Write(output); err != nil {
		t.Fatal(err)
	}
	if err := writer.write(todo11ProviderEvent{Type: "modes", Modes: todo11ProviderModes(output)}); err != nil {
		t.Fatal(err)
	}

	winch := make(chan os.Signal, 8)
	stop := make(chan struct{})
	var signals sync.WaitGroup
	signals.Add(1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		defer signals.Done()
		for {
			select {
			case <-stop:
				return
			case <-winch:
				width, height, sizeErr := term.GetSize(os.Stdin.Fd())
				if sizeErr == nil {
					_ = writer.write(todo11ProviderEvent{
						Type: "signal", Signal: "SIGWINCH", Cols: width, Rows: height,
					})
				}
			}
		}
	}()
	defer func() {
		signal.Stop(winch)
		close(stop)
		signals.Wait()
	}()

	var pending []byte
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := os.Stdin.Read(buffer)
		for _, value := range buffer[:n] {
			if value == todo11InputDelimiter {
				record := append([]byte(nil), pending...)
				if err := writer.write(todo11ProviderEvent{
					Type: "input", Data: base64.StdEncoding.EncodeToString(record),
				}); err != nil {
					t.Fatal(err)
				}
				pending = pending[:0]
				if bytes.Equal(record, todo11ProviderExit) {
					return
				}
				continue
			}
			pending = append(pending, value)
		}
		if readErr != nil {
			return
		}
	}
}

func TestTodo11HostProcess(t *testing.T) {
	if os.Getenv("UAM_TODO11_HOST_HELPER") != "1" {
		t.Skip("helper process")
	}
	diagnostics, err := os.OpenFile(
		os.Getenv("UAM_TODO11_HOST_DIAGNOSTICS"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = diagnostics.Close() }()
	previousLogger := uamlog.SetLogger(slog.New(slog.NewJSONHandler(diagnostics, nil)))
	defer uamlog.SetLogger(previousLogger)
	spec := hostLaunchSpec{
		name:  os.Getenv("UAM_TODO11_HOST_NAME"),
		cwd:   os.Getenv("UAM_TODO11_HOST_CWD"),
		label: "todo11",
		envs: []string{
			"UAM_TODO11_FAKE_PROVIDER=1",
			"UAM_TODO11_PROVIDER_REPORT=" + os.Getenv("UAM_TODO11_PROVIDER_REPORT"),
			"UAM_TODO11_PROVIDER_OUTPUT_HEX=" + os.Getenv("UAM_TODO11_PROVIDER_OUTPUT_HEX"),
		},
		command: []string{os.Getenv("UAM_TODO11_TEST_BINARY"), "-test.run=^TestTodo11FakeProviderProcess$"},
	}
	if err := runHost(os.Getenv("UAM_TODO11_RUNTIME_DIR"), spec, readyPipe()); err != nil {
		t.Fatal(err)
	}
}

func todo11ProviderModes(output []byte) []string {
	sequences := []struct {
		name string
		data []byte
	}{
		{name: "alternate_screen_enter", data: []byte("\x1b[?1049h")},
		{name: "alternate_screen_exit", data: []byte("\x1b[?1049l")},
		{name: "bracketed_paste_enable", data: []byte("\x1b[?2004h")},
		{name: "focus_enable", data: []byte("\x1b[?1004h")},
	}
	modes := make([]string, 0, len(sequences))
	for _, sequence := range sequences {
		if bytes.Contains(output, sequence.data) {
			modes = append(modes, sequence.name)
		}
	}
	return modes
}
