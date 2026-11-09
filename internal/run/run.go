// Package run starts the platform tools `saw` wraps (adb, xcrun, pymobiledevice3).
package run

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Command and LookPath start and find tools. Tests replace them with fakes (see runtest).
var (
	Command  = exec.CommandContext
	LookPath = exec.LookPath
)

// Has reports whether a tool is on PATH.
func Has(name string) bool {
	_, err := LookPath(name)
	return err == nil
}

// Output runs a tool to completion and returns its stdout. On failure, the error carries its stderr.
func Output(ctx context.Context, name string, args ...string) (string, error) {
	cmd := Command(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fail(name, args, err, stderr.String())
	}
	return string(out), nil
}

// Lines runs a tool and calls fn with each line of its stdout until the tool exits or ctx ends.
// Ending through ctx (Ctrl-C) is not an error.
func Lines(ctx context.Context, fn func(string), name string, args ...string) error {
	cmd := Command(ctx, name, args...)
	// pymobiledevice3 is Python, which holds output back when it isn't writing to a terminal.
	cmd.Env = append(cmd.Environ(), "PYTHONUNBUFFERED=1")
	var stderr tail
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		fn(sc.Text())
	}
	err = cmd.Wait()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fail(name, args, err, stderr.String())
	}
	return nil
}

func fail(name string, args []string, err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, msg)
}

// tail keeps the last few KB a long-running tool wrote to stderr, for its error message.
type tail struct{ buf []byte }

func (t *tail) Write(p []byte) (int, error) {
	const max = 4096
	t.buf = append(t.buf, p...)
	if len(t.buf) > max {
		t.buf = t.buf[len(t.buf)-max:]
	}
	return len(p), nil
}

func (t *tail) String() string { return string(t.buf) }
