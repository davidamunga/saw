// Package runtest fakes the tools saw drives (adb, xcrun, pymobiledevice3) in tests.
//
// The test binary stands in for each tool: Install points run.Command at the test binary itself,
// and Main, called from a package's TestMain, answers as the tool from a list of rules.
//
//	func TestMain(m *testing.M) { runtest.Main(m) }
//
//	runtest.Install(t,
//		runtest.Rule{Cmd: "adb devices -l", Stdout: "List of devices attached\nSERIAL device model:Pixel_8\n"},
//		runtest.Rule{Cmd: "adb -s SERIAL shell getprop", Stdout: "14\n"},
//	)
package runtest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"testing"
	"time"

	"github.com/davidamunga/saw/internal/run"
)

const (
	envRules = "SAW_FAKE_RULES"
	marker   = "saw-fake-tool"
)

// Rule answers one command. Cmd matches the start of "name arg arg ...": the first matching
// rule wins, so list specific rules before general ones.
type Rule struct {
	Cmd    string
	Stdout string
	Stderr string
	Exit   int
	Hang   bool // after printing, wait to be killed (for streams cancelled by ctx)
	Deaf   bool // with Hang: ignore interrupts, so only a kill stops it
}

// Main runs the tests, or, when the binary was started as a fake tool, acts as that tool.
func Main(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == marker {
		os.Exit(fake(os.Getenv(envRules), os.Args[2:]))
	}
	os.Exit(m.Run())
}

func fake(rulesJSON string, args []string) int {
	var rules []Rule
	if err := json.Unmarshal([]byte(rulesJSON), &rules); err != nil {
		fmt.Fprintln(os.Stderr, "runtest: bad rules:", err)
		return 125
	}
	line := strings.Join(args, " ")
	for _, r := range rules {
		if line == r.Cmd || strings.HasPrefix(line, r.Cmd+" ") {
			fmt.Fprint(os.Stdout, r.Stdout)
			fmt.Fprint(os.Stderr, r.Stderr)
			if r.Deaf {
				signal.Ignore(os.Interrupt)
			}
			if r.Hang {
				time.Sleep(time.Minute)
			}
			return r.Exit
		}
	}
	fmt.Fprintln(os.Stderr, "runtest: no fake for:", line)
	return 127
}

// Install makes every tool named by a rule available, answering from the rules, for the
// rest of the test. Tools with no rule are missing, as if not installed.
func Install(t testing.TB, rules ...Rule) {
	t.Helper()
	data, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	oldCommand, oldLookPath := run.Command, run.LookPath
	t.Cleanup(func() { run.Command, run.LookPath = oldCommand, oldLookPath })

	run.Command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, self, append([]string{marker, name}, args...)...)
		cmd.Env = append(os.Environ(), envRules+"="+string(data))
		return cmd
	}
	run.LookPath = func(name string) (string, error) {
		for _, r := range rules {
			if r.Cmd == name || strings.HasPrefix(r.Cmd, name+" ") {
				return name, nil
			}
		}
		return "", exec.ErrNotFound
	}
}
