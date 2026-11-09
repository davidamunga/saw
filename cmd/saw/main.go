// saw streams a mobile app's logs from any Android or iOS device, emulator or simulator.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/device"
	"github.com/davidamunga/saw/internal/doctor"
	"github.com/davidamunga/saw/internal/event"
	"github.com/davidamunga/saw/internal/output"
	"github.com/davidamunga/saw/internal/source"
)

var version = "dev" // set at release time

// Where commands read and write; tests swap them.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
	stdin  io.Reader = os.Stdin
)

// versionString is the release version, or for `go install` builds the module version Go
// recorded, or for local builds "dev" and the commit.
func versionString() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return strings.TrimPrefix(v, "v")
	}
	rev, dirty := "", ""
	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision" && len(s.Value) >= 7:
			rev = s.Value[:7]
		case s.Key == "vcs.modified" && s.Value == "true":
			dirty = ", modified"
		}
	}
	if rev == "" {
		return version
	}
	return fmt.Sprintf("%s (%s%s)", version, rev, dirty)
}

func isTerminal(v any) bool {
	f, ok := v.(*os.File)
	return ok && output.IsTerminal(f)
}

func wantColor(v any) bool {
	f, ok := v.(*os.File)
	return ok && output.WantColor(f)
}

const usage = `saw: stream a mobile app's logs from any Android or iOS device.

Usage:
  saw devices [--all] [--json]
  saw stream  [--app <name|id>] [--area a,b] [--device <id|name>] [--platform android|ios]
              [--level <level>] [--all] [--json]
  saw doctor
  saw version

Every command takes --config <path> (default: the nearest saw.json).
`

// usageError exits with code 2 instead of 1.
type usageError struct{ error }

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := runCLI(ctx, os.Args[1:])
	var ue usageError
	switch {
	case err == nil:
	case errors.As(err, &ue):
		fmt.Fprintln(os.Stderr, "saw:", err)
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, "saw:", err)
		os.Exit(1)
	}
}

func runCLI(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, usage)
		return nil
	}
	switch args[0] {
	case "devices":
		return devicesCmd(ctx, args[1:])
	case "stream":
		return streamCmd(ctx, args[1:])
	case "doctor":
		return doctorCmd(ctx, args[1:])
	case "version", "--version":
		fmt.Fprintln(stdout, "saw", versionString())
		return nil
	}
	return usageError{fmt.Errorf("unknown command %q (saw --help)", args[0])}
}

func newFlags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage) }
	return fs, fs.String("config", "", "path to saw.json")
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return usageError{err}
	}
	if fs.NArg() > 0 {
		return usageError{fmt.Errorf("unexpected argument %q", fs.Arg(0))}
	}
	return nil
}

func devicesCmd(ctx context.Context, args []string) error {
	fs, _ := newFlags("devices")
	all := fs.Bool("all", false, "include shut-down simulators")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := parse(fs, args); err != nil {
		return err
	}
	devs, errs := device.List(ctx, *all)
	for _, err := range errs {
		fmt.Fprintln(stderr, "saw:", err)
	}
	if *asJSON {
		if devs == nil {
			devs = []device.Device{}
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(devs)
	}
	if len(devs) == 0 {
		fmt.Fprintln(stderr, "no devices found (try saw doctor)")
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PLATFORM\tKIND\tNAME\tOS\tSTATE\tID")
	for _, d := range devs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", d.Platform, d.Kind, d.Name, d.OS, d.State, d.ID)
	}
	return tw.Flush()
}

// listFlag takes a comma list, and can be repeated.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }

func (l *listFlag) Set(v string) error {
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			*l = append(*l, s)
		}
	}
	return nil
}

func streamCmd(ctx context.Context, args []string) error {
	fs, cfgPath := newFlags("stream")
	appName := fs.String("app", "", "app name from saw.json, or a package/bundle id")
	var areas listFlag
	fs.Var(&areas, "area", "only these areas (comma list, repeatable)")
	deviceQuery := fs.String("device", "", "device or simulator id, id prefix or name")
	platform := fs.String("platform", "", "android or ios")
	levelName := fs.String("level", "verbose", "lowest level to show: verbose, debug, info, warn, error, fatal")
	all := fs.Bool("all", false, "everything the app's process logs, not only its own lines")
	asJSON := fs.Bool("json", false, "print one JSON event per line")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *platform != "" && *platform != "android" && *platform != "ios" {
		return usageError{fmt.Errorf("--platform must be android or ios")}
	}
	minLevel, err := event.ParseLevel(*levelName)
	if err != nil {
		return usageError{err}
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	app, err := cfg.App(*appName)
	if err != nil {
		return usageError{err}
	}
	selected, chosen, err := cfg.Areas(areas)
	if err != nil {
		return usageError{err}
	}

	devs, errs := device.List(ctx, false)
	dev, err := device.Pick(devs, *deviceQuery, *platform)
	var amb *device.AmbiguousError
	if errors.As(err, &amb) && isTerminal(stdin) {
		dev, err = choose(amb.Candidates)
	}
	if err != nil {
		for _, e := range errs {
			fmt.Fprintln(stderr, "saw:", e)
		}
		return err
	}

	var printer output.Printer = output.NewPretty(stdout, wantColor(stdout))
	if *asJSON {
		printer = output.NewJSON(stdout)
	}

	warnedPrivate := false
	emit := func(e event.Event) {
		if e.Level < minLevel {
			return
		}
		if !warnedPrivate && e.Platform == "ios" && strings.Contains(e.Message, "<private>") {
			warnedPrivate = true
			fmt.Fprintln(stderr, "saw: iOS hides interpolated values as <private>; mark them `privacy: .public` in your Logger calls to see them")
		}
		printer.Print(e)
	}
	return source.Stream(ctx, source.Spec{
		Device: dev, App: app, Subsystem: cfg.Subsystem(app),
		Areas: selected, Chosen: chosen, TagFor: cfg.TagFor(), All: *all,
		OnStart: func() {
			fmt.Fprintf(stderr, "saw: %s on %s %s (Ctrl-C to stop)\n", app.Name, dev.Name, dev.OS)
		},
	}, emit)
}

// choose asks which device to use when several fit.
func choose(devs []device.Device) (device.Device, error) {
	fmt.Fprintln(stderr, "Which device?")
	for i, d := range devs {
		fmt.Fprintf(stderr, "  %d) %s  %s  %s\n", i+1, d.Name, d.OS, d.ID)
	}
	fmt.Fprint(stderr, "> ")
	line, _ := bufio.NewReader(stdin).ReadString('\n')
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(devs) {
		return device.Device{}, usageError{errors.New("no device chosen")}
	}
	return devs[n-1], nil
}

func doctorCmd(ctx context.Context, args []string) error {
	fs, cfgPath := newFlags("doctor")
	if err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if !doctor.Run(ctx, cfg, stdout) {
		return errors.New("some checks failed")
	}
	return nil
}
