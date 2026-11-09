package run_test

import (
	"context"
	"strings"
	"testing"

	"github.com/davidamunga/saw/internal/run"
	"github.com/davidamunga/saw/internal/runtest"
)

func TestMain(m *testing.M) { runtest.Main(m) }

func TestOutput(t *testing.T) {
	runtest.Install(t,
		runtest.Rule{Cmd: "tool ok", Stdout: "hello\n"},
		runtest.Rule{Cmd: "tool fail", Stderr: "boom\n", Exit: 3},
		runtest.Rule{Cmd: "tool quiet", Exit: 1},
	)
	out, err := run.Output(context.Background(), "tool", "ok")
	if err != nil || out != "hello\n" {
		t.Errorf("got %q, %v", out, err)
	}
	_, err = run.Output(context.Background(), "tool", "fail")
	if err == nil || !strings.Contains(err.Error(), "tool fail") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("want the command and its stderr in the error, got %v", err)
	}
	_, err = run.Output(context.Background(), "tool", "quiet")
	if err == nil || strings.Contains(err.Error(), "\n") {
		t.Errorf("want a one-line error without stderr, got %q", err)
	}
}

func TestHas(t *testing.T) {
	runtest.Install(t, runtest.Rule{Cmd: "adb version"})
	if !run.Has("adb") || run.Has("xcrun") {
		t.Error("adb should be found and xcrun not")
	}
}

func TestLines(t *testing.T) {
	runtest.Install(t,
		runtest.Rule{Cmd: "tool stream", Stdout: "a\nb\nc\n"},
		runtest.Rule{Cmd: "tool broken", Stdout: "a\n", Stderr: strings.Repeat("x", 5000) + "END", Exit: 2},
		runtest.Rule{Cmd: "tool forever", Stdout: "first\n", Hang: true},
	)
	var got []string
	if err := run.Lines(context.Background(), func(l string) { got = append(got, l) }, "tool", "stream"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "a,b,c" {
		t.Errorf("got %v", got)
	}

	err := run.Lines(context.Background(), func(string) {}, "tool", "broken")
	if err == nil || !strings.HasSuffix(err.Error(), "END") || len(err.Error()) > 4200 {
		t.Errorf("want the tail of stderr in the error, got %d bytes", len(err.Error()))
	}

	// Cancelling (Ctrl-C) ends a stream without an error.
	ctx, cancel := context.WithCancel(context.Background())
	if err := run.Lines(ctx, func(string) { cancel() }, "tool", "forever"); err != nil {
		t.Errorf("cancelled stream: %v", err)
	}
}

func TestLinesMissingTool(t *testing.T) {
	if err := run.Lines(context.Background(), func(string) {}, "saw-no-such-tool-xyz"); err == nil {
		t.Error("want an error for a missing tool")
	}
}
