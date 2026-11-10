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
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/crash"
	"github.com/davidamunga/saw/internal/device"
	"github.com/davidamunga/saw/internal/doctor"
	"github.com/davidamunga/saw/internal/event"
	"github.com/davidamunga/saw/internal/output"
	"github.com/davidamunga/saw/internal/source"
)

var version = "dev" // set at release time

// now is the clock --since counts back from; tests set it.
var now = time.Now

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

const usage = `saw: a mobile app's logs and network requests, from any Android or iOS device.

Usage:
  saw devices [--all] [--json]
  saw stream  [--app <name|id>] [--scope a,b] [--level <level>] [--exclude-tag a,b] [--all] [--json]
              [watch flags]
  saw net     [--app <name|id>] [--full | --curl | --json] [--har <file>] [--connections] [watch flags]
  saw crashes [--app <name|id>] [--device <id|name|all>] [--platform android|ios] [--since <when>]
              [--last | --json]
  saw doctor
  saw version

Watch flags, for stream and net:
  --device <id|name>    a device's id, id prefix or name; several with commas, or all
  --platform <p>        android or ios
  --since <when>        start back in time: 5m, 90s, or a clock time like 09:40
  --grep <regexp>       only what matches (case-insensitive unless it has capitals)
  --until <regexp>      stop after the first match; fail if nothing matches
  --for <duration>      stop after this long: 10s, 2m

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
	case "net":
		return netCmd(ctx, args[1:])
	case "crashes":
		return crashesCmd(ctx, args[1:])
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
	var scopes, exclude listFlag
	fs.Var(&scopes, "scope", "only these scopes (comma list, repeatable)")
	fs.Var(&exclude, "exclude-tag", "hide these tags or categories (comma list, repeatable)")
	wf := addWatchFlags(fs)
	levelName := fs.String("level", "verbose", "lowest level to show: verbose, debug, info, warn, error, fatal")
	all := fs.Bool("all", false, "everything the app's process logs, not only its own lines")
	asJSON := fs.Bool("json", false, "print one JSON event per line")
	if err := parse(fs, args); err != nil {
		return err
	}
	minLevel, err := event.ParseLevel(*levelName)
	if err != nil {
		return usageError{err}
	}
	w, err := wf.parse(now())
	if err != nil {
		return err
	}
	cfg, app, err := loadApp(*cfgPath, *appName, *wf.platform)
	if err != nil {
		return err
	}
	selected, chosen, err := cfg.Scopes(scopes)
	if err != nil {
		return usageError{err}
	}
	if w.devs, err = pickDevices(ctx, *wf.device, *wf.platform, app); err != nil {
		return err
	}

	var printer output.Printer
	if *asJSON {
		printer = output.NewJSON(stdout)
	} else {
		p := output.NewPretty(stdout, wantColor(stdout))
		if len(w.devs) > 1 {
			p.ShowDevice(w.deviceWidth())
		}
		printer = p
	}

	warnedPrivate := false
	emit := func(e event.Event) {
		if e.Level < minLevel || hasTag(e, exclude) {
			return
		}
		w.emit(e.Time, e.Label()+" "+e.Message, func() {
			if !warnedPrivate && e.Platform == "ios" && strings.Contains(e.Message, "<private>") {
				warnedPrivate = true
				fmt.Fprintln(stderr, "saw: iOS hides interpolated values as <private>; mark them `privacy: .public` in your Logger calls to see them")
			}
			printer.Print(e)
		})
	}
	return w.run(ctx, app.Name, func(ctx context.Context, dev device.Device, since time.Time) error {
		// Crash reports show up in the stream too, since the app's own log misses native
		// crashes, ANRs and iOS crashes. After a reconnect, since is when the device went away.
		ctx, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		defer func() { stop(); <-done }()
		start := since
		if start.IsZero() {
			start = now()
		}
		go func() {
			defer close(done)
			crash.Watch(ctx, dev, app, start, func(r crash.Report) { emit(crashEvent(r)) })
		}()
		return source.Stream(ctx, source.Spec{
			Device: dev, App: app, Subsystem: cfg.Subsystem(app),
			Scopes: selected, Chosen: chosen, TagFor: cfg.TagFor(), All: *all, Since: since,
			OnStart: w.onStart(app.Name, dev), OnLive: w.onLive(dev),
		}, emit)
	})
}

// crashEvent is a crash report as one line of the stream, pointing to the full report.
func crashEvent(r crash.Report) event.Event {
	what := map[string]string{"crash": "crash", "native": "native crash", "anr": "not responding (ANR)"}[r.Kind]
	if what == "" {
		what = r.Kind
	}
	return event.Event{Time: r.Time, Platform: r.Platform, Device: r.Device, Process: r.Process, Level: event.Fatal,
		Tag: "CRASH", Message: fmt.Sprintf("%s: %s (saw crashes --last for the report)", what, r.Summary)}
}

// hasTag reports whether a line's tag, category or scope is one of tags.
func hasTag(e event.Event, tags []string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, e.Tag) || strings.EqualFold(t, e.Category) || strings.EqualFold(t, e.Scope) {
			return true
		}
	}
	return false
}

// loadApp reads saw.json and resolves --app, after checking --platform.
func loadApp(cfgPath, appName, platform string) (*config.Config, config.App, error) {
	if platform != "" && platform != "android" && platform != "ios" {
		return nil, config.App{}, usageError{fmt.Errorf("--platform must be android or ios")}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, config.App{}, err
	}
	app, err := cfg.App(appName)
	if err != nil {
		return nil, config.App{}, usageError{err}
	}
	return cfg, app, nil
}

// pickDevice finds the device to use, asking when several fit and stdin is a terminal.
func pickDevice(ctx context.Context, query, platform string) (device.Device, error) {
	devs, errs := device.List(ctx, false)
	dev, err := device.Pick(devs, query, platform)
	var amb *device.AmbiguousError
	// Ask only someone who can answer: input from a terminal, and the question shown on one.
	if errors.As(err, &amb) && isTerminal(stdin) && isTerminal(stderr) {
		dev, err = choose(amb.Candidates)
	}
	if err != nil {
		for _, e := range errs {
			fmt.Fprintln(stderr, "saw:", e)
		}
	}
	return dev, err
}

func netCmd(ctx context.Context, args []string) error {
	fs, cfgPath := newFlags("net")
	appName := fs.String("app", "", "app name from saw.json, or a package/bundle id")
	wf := addWatchFlags(fs)
	full := fs.Bool("full", false, "print requests the app logs as plain HTTP, with headers and bodies (.http file format)")
	curl := fs.Bool("curl", false, "print requests the app logs as curl commands")
	asJSON := fs.Bool("json", false, "print one JSON event per line")
	connections := fs.Bool("connections", false, "keep showing connections after the app logs its first request")
	harPath := fs.String("har", "", "also save the requests to this HAR file, for Chrome DevTools, Proxyman or Charles")
	if err := parse(fs, args); err != nil {
		return err
	}
	if n := countTrue(*full, *curl, *asJSON); n > 1 {
		return usageError{errors.New("pick one of --full, --curl and --json")}
	}
	w, err := wf.parse(now())
	if err != nil {
		return err
	}
	cfg, app, err := loadApp(*cfgPath, *appName, *wf.platform)
	if err != nil {
		return err
	}
	if w.devs, err = pickDevices(ctx, *wf.device, *wf.platform, app); err != nil {
		return err
	}
	var printer output.NetPrinter = output.NewNetPretty(stdout, wantColor(stdout), *full)
	switch {
	case *asJSON:
		printer = output.NewNetJSON(stdout)
	case *curl:
		printer = output.NewNetCurl(stdout)
	}
	if p, ok := printer.(interface{ ShowDevice(int) }); ok && len(w.devs) > 1 {
		p.ShowDevice(w.deviceWidth())
	}
	if !*connections && !*asJSON {
		printer = &quietConnections{next: printer}
	}
	var har *output.HAR
	var harErr error
	if *harPath != "" {
		har = output.NewHAR(*harPath, versionString())
		if err := har.Write(); err != nil { // fail now, not after the session
			return err
		}
	}
	emit := func(n event.Net) {
		w.emit(n.Time, netText(n), func() {
			printer.Print(n)
			if har != nil && harErr == nil {
				_, harErr = har.Add(n)
			}
		})
	}
	err = w.run(ctx, app.Name+" network", func(ctx context.Context, dev device.Device, since time.Time) error {
		return source.StreamNet(ctx, source.Spec{
			Device: dev, App: app, Subsystem: cfg.Subsystem(app), Since: since,
			OnStart: w.onStart(app.Name+" network", dev), OnLive: w.onLive(dev),
		}, emit)
	})
	if har != nil {
		if harErr != nil {
			return fmt.Errorf("saving %s: %w", *harPath, harErr)
		}
		fmt.Fprintf(stderr, "saw: saved %s to %s\n", plural(har.Count(), "request"), *harPath)
	}
	return err
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// netText is what --grep and --until match in a network event.
func netText(n event.Net) string {
	parts := []string{n.Method}
	if n.Status > 0 {
		parts = append(parts, strconv.Itoa(n.Status))
	}
	parts = append(parts, n.URL, n.Host, n.IP, n.Tag, n.Log, n.Error, n.RequestBody, n.ResponseBody)
	return strings.Join(parts, " ")
}

func countTrue(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// quietConnections drops a device's connection-level lines once the app on it has logged a
// request: by then its logged requests say more, and the connections are noise. Apps that log
// nothing keep them.
type quietConnections struct {
	next   output.NetPrinter
	logged map[string]bool // by device
}

func (q *quietConnections) Print(n event.Net) {
	switch n.Kind {
	case "logged":
		if q.logged == nil {
			q.logged = map[string]bool{}
		}
		q.logged[n.Device] = true
	case "tcp", "udp", "dns", "tls", "request":
		if q.logged[n.Device] {
			return
		}
	}
	q.next.Print(n)
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

// watchFlags are the flags stream and net share: which devices, how far back, which lines, and
// when to stop.
type watchFlags struct {
	device, platform, since, grep, until *string
	dur                                  *time.Duration
}

func addWatchFlags(fs *flag.FlagSet) *watchFlags {
	return &watchFlags{
		device:   fs.String("device", "", "device id, id prefix or name; several with commas, or all"),
		platform: fs.String("platform", "", "android or ios"),
		since:    fs.String("since", "", "start back in time: a duration (5m) or a clock time (09:40)"),
		grep:     fs.String("grep", "", "only what matches this regular expression (case-insensitive unless it has capitals)"),
		until:    fs.String("until", "", "stop after the first match of this regular expression; fail if nothing matches"),
		dur:      fs.Duration("for", 0, "stop after this long (10s, 2m)"),
	}
}

func (f *watchFlags) parse(now time.Time) (*watch, error) {
	w := &watch{dur: *f.dur}
	if w.dur < 0 {
		return nil, usageError{errors.New("--for must be positive")}
	}
	var err error
	if w.since, err = parseSince(*f.since, now); err != nil {
		return nil, usageError{err}
	}
	if w.grep, err = compileMatch("grep", *f.grep); err != nil {
		return nil, err
	}
	w.untilText = *f.until
	if w.until, err = compileMatch("until", *f.until); err != nil {
		return nil, err
	}
	return w, nil
}

// parseSince reads --since: a duration back from now, or a clock time today (yesterday, if
// that time hasn't come yet today).
func parseSince(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return time.Time{}, errors.New("--since must be positive")
		}
		return now.Add(-d), nil
	}
	for _, layout := range []string{"15:04:05", "15:04"} {
		if c, err := time.Parse(layout, s); err == nil {
			t := time.Date(now.Year(), now.Month(), now.Day(), c.Hour(), c.Minute(), c.Second(), 0, now.Location())
			if t.After(now) {
				t = t.AddDate(0, 0, -1)
			}
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("--since wants a duration like 5m or a clock time like 09:40, not %q", s)
}

// compileMatch compiles a --grep or --until pattern, ignoring case unless it has capitals.
func compileMatch(name, pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	if pattern == strings.ToLower(pattern) {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, usageError{fmt.Errorf("--%s: %v", name, err)}
	}
	return re, nil
}

// watch reads one or more devices at once, printing what --grep lets through, and stopping on
// --until's first match or after --for.
type watch struct {
	devs        []device.Device
	since       time.Time
	grep, until *regexp.Regexp
	untilText   string
	dur         time.Duration
	mu          sync.Mutex // one device's output at a time
	matched     bool
	expired     bool
	cancel      context.CancelFunc

	// With --since on several devices, each device's history arrives as one burst. Lines are
	// held until every device has started, then printed in time order.
	sorting bool
	live    map[string]bool // devices whose history is in (or that failed)
	held    []heldLine

	started map[string]bool // devices streaming now (see follow)
}

type heldLine struct {
	t     time.Time
	text  string
	print func()
}

// settleTime is how long after the last device goes live its history is waited for: logcat
// sends its history and live lines as one stream.
var settleTime = 1500 * time.Millisecond

// wentLive counts a device as live (or failed). Once all are, --for's clock starts, and the held
// lines are printed after settleTime. Call with w.mu held.
func (w *watch) wentLive(id string) {
	if w.live[id] {
		return
	}
	w.live[id] = true
	if len(w.live) < len(w.devs) {
		return
	}
	if w.dur > 0 {
		time.AfterFunc(w.dur, func() {
			w.mu.Lock()
			w.expired = true
			w.mu.Unlock()
			w.cancel()
		})
	}
	if w.sorting {
		time.AfterFunc(settleTime, func() {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.release()
		})
	}
}

// release prints the held lines in time order and stops holding. Call with w.mu held.
func (w *watch) release() {
	if !w.sorting {
		return
	}
	w.sorting = false
	sort.SliceStable(w.held, func(i, j int) bool { return w.held[i].t.Before(w.held[j].t) })
	for _, l := range w.held {
		w.show(l.text, l.print)
	}
	w.held = nil
}

func (w *watch) deviceWidth() int {
	names := make([]string, len(w.devs))
	for i, d := range w.devs {
		names[i] = d.Name
	}
	return output.DeviceWidth(names)
}

// hint tells how the stream ends.
func (w *watch) hint() string {
	if w.dur > 0 {
		return fmt.Sprintf(" (for %s)", w.dur)
	}
	return " (Ctrl-C to stop)"
}

// onStart reports a device ready.
func (w *watch) onStart(what string, dev device.Device) func() {
	return func() {
		w.mu.Lock()
		hint := ""
		if len(w.devs) == 1 {
			hint = w.hint()
		}
		fmt.Fprintf(stderr, "saw: %s on %s %s%s\n", what, dev.Name, dev.OS, hint)
		if w.started == nil {
			w.started = map[string]bool{}
		}
		w.started[dev.ID] = true
		w.mu.Unlock()
	}
}

// onLive notes a device has printed its history.
func (w *watch) onLive(dev device.Device) func() {
	return func() {
		w.mu.Lock()
		w.wentLive(dev.ID)
		w.mu.Unlock()
	}
}

// emit prints a line from time t, with text being what --grep and --until match.
func (w *watch) emit(t time.Time, text string, print func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sorting {
		w.held = append(w.held, heldLine{t, text, print})
		return
	}
	w.show(text, print)
}

// show prints a line unless --grep filters it out or --until already matched. Call with w.mu held.
func (w *watch) show(text string, print func()) {
	if w.matched || (w.grep != nil && !w.grep.MatchString(text)) {
		return
	}
	print()
	if w.until != nil && w.until.MatchString(text) {
		w.matched = true
		w.cancel()
	}
}

// run reads every device, each with read, until they end, --until matches or --for is up. One
// device failing doesn't stop the others.
func (w *watch) run(ctx context.Context, what string, read readFunc) error {
	ctx, w.cancel = context.WithCancel(ctx)
	defer w.cancel()
	w.live = map[string]bool{}
	if len(w.devs) == 1 {
		if err := w.follow(ctx, w.devs[0], read); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stderr, "saw: %s on %d devices%s\n", what, len(w.devs), w.hint())
		w.sorting = !w.since.IsZero()
		var wg sync.WaitGroup
		failed := 0
		for _, d := range w.devs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := w.follow(ctx, d, read); err != nil {
					w.mu.Lock()
					failed++
					fmt.Fprintf(stderr, "saw: %s: %v\n", d.Name, err)
					w.wentLive(d.ID)
					w.mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if failed == len(w.devs) {
			return errors.New("no device could be read")
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.release() // the devices ended before their history was printed
	if w.until != nil && !w.matched {
		if w.expired {
			return fmt.Errorf("nothing matched --until %q in %s", w.untilText, w.dur)
		}
		return fmt.Errorf("nothing matched --until %q", w.untilText)
	}
	return nil
}

// pickDevices finds the devices --device names: one as pickDevice does, several separated by
// commas, or all: every running device on a platform the app is configured for.
func pickDevices(ctx context.Context, query, platform string, app config.App) ([]device.Device, error) {
	if query != "all" && !strings.Contains(query, ",") {
		d, err := pickDevice(ctx, query, platform)
		if err != nil {
			return nil, err
		}
		return []device.Device{d}, nil
	}
	devs, errs := device.List(ctx, false)
	report := func() {
		for _, e := range errs {
			fmt.Fprintln(stderr, "saw:", e)
		}
	}
	var picked []device.Device
	if query == "all" {
		for _, d := range devs {
			if d.Ready && (platform == "" || d.Platform == platform) &&
				(d.Platform == "android" && app.Android != "" || d.Platform == "ios" && app.IOS != "") {
				picked = append(picked, d)
			}
		}
		if len(picked) == 0 {
			report()
			return nil, fmt.Errorf("no running device for %s (try `saw devices`)", app.Name)
		}
		return picked, nil
	}
	seen := map[string]bool{}
	for _, q := range strings.Split(query, ",") {
		if q = strings.TrimSpace(q); q == "" {
			continue
		}
		d, err := device.Pick(devs, q, platform)
		if err != nil {
			report()
			return nil, err
		}
		if !seen[d.ID] {
			seen[d.ID] = true
			picked = append(picked, d)
		}
	}
	return picked, nil
}

// crashesCmd lists the app's crash reports, newest first, or prints the newest in full.
func crashesCmd(ctx context.Context, args []string) error {
	fs, cfgPath := newFlags("crashes")
	appName := fs.String("app", "", "app name from saw.json, or a package/bundle id")
	deviceQuery := fs.String("device", "", "device id, id prefix or name; several with commas, or all")
	platform := fs.String("platform", "", "android or ios")
	sinceText := fs.String("since", "", "only crashes since: a duration (24h) or a clock time (09:40)")
	last := fs.Bool("last", false, "print the newest crash report in full")
	asJSON := fs.Bool("json", false, "print one JSON report per line, each in full")
	if err := parse(fs, args); err != nil {
		return err
	}
	if *last && *asJSON {
		return usageError{errors.New("pick one of --last and --json")}
	}
	since, err := parseSince(*sinceText, now())
	if err != nil {
		return usageError{err}
	}
	_, app, err := loadApp(*cfgPath, *appName, *platform)
	if err != nil {
		return err
	}
	devs, err := pickDevices(ctx, *deviceQuery, *platform, app)
	if err != nil {
		return err
	}

	var reports []crash.Report
	failed := 0
	for _, d := range devs {
		rs, err := crash.List(ctx, d, app)
		if err != nil {
			if len(devs) == 1 {
				return err
			}
			failed++
			fmt.Fprintf(stderr, "saw: %s: %v\n", d.Name, err)
		}
		for _, r := range rs {
			if since.IsZero() || !r.Time.Before(since) {
				reports = append(reports, r)
			}
		}
	}
	if failed == len(devs) {
		return errors.New("no device could be read")
	}
	sort.SliceStable(reports, func(i, j int) bool { return reports[i].Time.After(reports[j].Time) })

	where := devs[0].Name
	if len(devs) > 1 {
		where = fmt.Sprintf("%d devices", len(devs))
	}
	if len(reports) == 0 {
		fmt.Fprintf(stderr, "saw: no crashes for %s on %s\n", app.Name, where)
		return nil
	}
	switch {
	case *asJSON:
		enc := json.NewEncoder(stdout)
		for _, r := range reports {
			if err := enc.Encode(r); err != nil {
				return err
			}
		}
	case *last:
		r := reports[0]
		fmt.Fprintf(stdout, "# %s  %s  %s  %s\n\n%s\n", r.Time.Format("2006-01-02 15:04:05"), r.Device, r.Kind, r.Summary, r.Text)
		if r.File != "" {
			fmt.Fprintf(stdout, "\n# %s\n", r.File)
		}
	default:
		printCrashes(reports, len(devs) > 1, wantColor(stdout))
		fmt.Fprintf(stderr, "saw: %s for %s on %s; --last prints the newest in full\n", plural(len(reports), "report"), app.Name, where)
	}
	return nil
}

func printCrashes(reports []crash.Report, showDevice, color bool) {
	width := 0
	if showDevice {
		names := make([]string, len(reports))
		for i, r := range reports {
			names[i] = r.Device
		}
		width = output.DeviceWidth(names)
	}
	paint := func(code, s string) string {
		if !color {
			return s
		}
		return code + s + "\x1b[0m"
	}
	for _, r := range reports {
		line := paint("\x1b[2m", r.Time.Format("2006-01-02 15:04:05")) + "  "
		if width > 0 {
			name := r.Device
			if len(name) > width {
				name = name[:width-1] + "~"
			}
			line += fmt.Sprintf("%-*s  ", width, name)
		}
		kindColor := "\x1b[31m"
		if r.Kind == "anr" {
			kindColor = "\x1b[33m"
		}
		line += paint(kindColor, fmt.Sprintf("%-6s", r.Kind)) + "  " + r.Summary
		fmt.Fprintln(stdout, line)
	}
}
