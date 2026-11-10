package crash

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// An .ips crash report is a line of JSON about the report, then the report as JSON.
type ipsHeader struct {
	AppName      string `json:"app_name"`
	Timestamp    string `json:"timestamp"`
	AppVersion   string `json:"app_version"`
	BuildVersion string `json:"build_version"`
	BundleID     string `json:"bundleID"`
	BugType      string `json:"bug_type"`
	OSVersion    string `json:"os_version"`
}

type ipsFrame struct {
	ImageIndex     int    `json:"imageIndex"`
	ImageOffset    int    `json:"imageOffset"`
	Symbol         string `json:"symbol"`
	SymbolLocation int    `json:"symbolLocation"`
}

type ipsBody struct {
	ProcPath  string `json:"procPath"`
	ProcName  string `json:"procName"`
	ModelCode string `json:"modelCode"`
	Exception struct {
		Type    string `json:"type"`
		Signal  string `json:"signal"`
		Subtype string `json:"subtype"`
	} `json:"exception"`
	Termination struct {
		Namespace string `json:"namespace"`
		Indicator string `json:"indicator"`
		ByProc    string `json:"byProc"`
	} `json:"termination"`
	ASI            map[string][]string `json:"asi"` // messages the app's libraries left: uncaught exceptions, Swift errors
	FaultingThread int                 `json:"faultingThread"`
	Threads        []struct {
		Queue  string     `json:"queue"`
		Name   string     `json:"name"`
		Frames []ipsFrame `json:"frames"`
	} `json:"threads"`
	LastExceptionBacktrace []ipsFrame `json:"lastExceptionBacktrace"`
	UsedImages             []struct {
		Name string `json:"name"`
	} `json:"usedImages"`
}

// ipsHeaderMatches reads only an .ips file's first line, to skip other apps' reports cheaply.
func ipsHeaderMatches(path, bundle string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	line, err := bufio.NewReader(f).ReadBytes('\n')
	if err != nil {
		return false
	}
	var h ipsHeader
	return json.Unmarshal(line, &h) == nil && h.BugType == "309" && h.BundleID == bundle
}

// ParseIPS reads an app crash report (bug type 309) for bundle, returning it and the crashed
// process's path (on a simulator, the path names the simulator).
func ParseIPS(data []byte, bundle string) (Report, string, bool) {
	first, rest, ok := bytes.Cut(data, []byte("\n"))
	var h ipsHeader
	if !ok || json.Unmarshal(first, &h) != nil || h.BugType != "309" || h.BundleID != bundle {
		return Report{}, "", false
	}
	var b ipsBody
	if json.Unmarshal(rest, &b) != nil {
		return Report{}, "", false
	}
	t, err := time.Parse("2006-01-02 15:04:05.00 -0700", h.Timestamp)
	if err != nil {
		return Report{}, "", false
	}
	process := b.ProcName
	if process == "" {
		process = h.AppName
	}
	return Report{Time: t, Kind: "crash", Process: process, Summary: b.summary(), Text: b.text(h, t)}, b.ProcPath, true
}

// messages are the app's last words, from its libraries: an uncaught exception's reason, a Swift
// fatal error.
func (b ipsBody) messages() []string {
	keys := make([]string, 0, len(b.ASI))
	for k := range b.ASI {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		for _, m := range b.ASI[k] {
			if m = strings.TrimSpace(m); m != "" {
				out = append(out, m)
			}
		}
	}
	return out
}

func (b ipsBody) exceptionName() string {
	s := b.Exception.Type
	if b.Exception.Signal != "" {
		s += " (" + b.Exception.Signal + ")"
	}
	return s
}

// summary is the app's last message, else the exception and where it happened.
func (b ipsBody) summary() string {
	if msgs := b.messages(); len(msgs) > 0 {
		m := msgs[0]
		// *** Terminating app due to uncaught exception 'NSRangeException', reason: '…'
		if _, after, ok := strings.Cut(m, "uncaught exception '"); ok {
			if name, reason, ok := strings.Cut(after, "', reason: '"); ok {
				return name + ": " + strings.TrimSuffix(strings.SplitN(reason, "\n", 2)[0], "'")
			}
		}
		return strings.SplitN(m, "\n", 2)[0]
	}
	s := b.exceptionName()
	if ns := b.Termination.Namespace; ns != "" && ns != "SIGNAL" && b.Termination.Indicator != "" {
		s += ": " + b.Termination.Indicator // killed by iOS, e.g. a watchdog
	}
	if f, ok := b.topFrame(); ok {
		s += " in " + b.frameName(f)
	}
	return s
}

// topFrame is the crashed thread's first frame in the app's own code, else its first frame.
func (b ipsBody) topFrame() (ipsFrame, bool) {
	frames := b.LastExceptionBacktrace
	if len(frames) == 0 && b.FaultingThread < len(b.Threads) {
		frames = b.Threads[b.FaultingThread].Frames
	}
	if len(frames) == 0 {
		return ipsFrame{}, false
	}
	for _, f := range frames {
		// Debug builds put the app's code in <App>.debug.dylib.
		if name := b.image(f); name == b.ProcName || name == b.ProcName+".debug.dylib" {
			return f, true
		}
	}
	return frames[0], true
}

func (b ipsBody) image(f ipsFrame) string {
	if f.ImageIndex < len(b.UsedImages) && b.UsedImages[f.ImageIndex].Name != "" {
		return b.UsedImages[f.ImageIndex].Name
	}
	return "???"
}

func (b ipsBody) frameName(f ipsFrame) string {
	if f.Symbol != "" {
		return f.Symbol
	}
	return fmt.Sprintf("%s + %d", b.image(f), f.ImageOffset)
}

// text is the report the way Xcode's crash logs read: what crashed, why, and where.
func (b ipsBody) text(h ipsHeader, t time.Time) string {
	var w strings.Builder
	fmt.Fprintf(&w, "%s %s (%s), %s\n", h.AppName, h.AppVersion, h.BuildVersion, h.BundleID)
	fmt.Fprintf(&w, "Crashed: %s, %s", t.Format("2006-01-02 15:04:05"), h.OSVersion)
	if b.ModelCode != "" {
		fmt.Fprintf(&w, ", %s", b.ModelCode)
	}
	fmt.Fprintf(&w, "\nException: %s", b.exceptionName())
	if b.Exception.Subtype != "" {
		fmt.Fprintf(&w, ", %s", b.Exception.Subtype)
	}
	if b.Termination.Indicator != "" {
		fmt.Fprintf(&w, "\nTermination: %s", b.Termination.Indicator)
		if b.Termination.ByProc != "" {
			fmt.Fprintf(&w, " (by %s)", b.Termination.ByProc)
		}
	}
	w.WriteString("\n")
	for _, m := range b.messages() {
		w.WriteString("\n" + m + "\n")
	}
	if len(b.LastExceptionBacktrace) > 0 {
		w.WriteString("\nLast exception backtrace:\n")
		b.writeFrames(&w, b.LastExceptionBacktrace)
	}
	for i, th := range b.Threads {
		if i != b.FaultingThread {
			continue
		}
		fmt.Fprintf(&w, "\nThread %d crashed", i)
		if name := th.Name + th.Queue; name != "" {
			fmt.Fprintf(&w, " (%s)", strings.TrimSpace(th.Name+" "+th.Queue))
		}
		w.WriteString(":\n")
		b.writeFrames(&w, th.Frames)
	}
	return strings.TrimRight(w.String(), "\n")
}

func (b ipsBody) writeFrames(w *strings.Builder, frames []ipsFrame) {
	width := 0
	for _, f := range frames {
		width = max(width, len(b.image(f)))
	}
	for i, f := range frames {
		where := fmt.Sprintf("+ %d", f.ImageOffset)
		if f.Symbol != "" {
			where = fmt.Sprintf("%s + %d", f.Symbol, f.SymbolLocation)
		}
		fmt.Fprintf(w, "%3d  %-*s  %s\n", i, width, b.image(f), where)
	}
}
