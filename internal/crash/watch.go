package crash

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/device"
	"github.com/davidamunga/saw/internal/run"
)

// PollEvery is how often Watch looks for new crash reports; tests shorten it.
var PollEvery = 3 * time.Second

// Watch calls fn with each crash report for the app that appears on dev from since on, until
// ctx ends. Each look is cheap until something new shows up: on Android, the dropbox's list of
// entries; on an iPhone, the list of report files; on a simulator, the reports' first lines.
// Failures (a device going away) are skipped; the next look tries again.
func Watch(ctx context.Context, dev device.Device, app config.App, since time.Time, fn func(Report)) {
	since = since.Truncate(time.Second) // Android's reports are stamped to the second
	seen := map[string]bool{}
	report := func(rs []Report) {
		sort.SliceStable(rs, func(i, j int) bool { return rs[i].Time.Before(rs[j].Time) }) // oldest first, as a stream reads
		for _, r := range rs {
			key := r.Time.String() + r.Kind + r.Summary
			if r.Time.Before(since) || seen[key] {
				continue
			}
			seen[key] = true
			r.Platform, r.Device = dev.Platform, dev.Name
			fn(r)
		}
	}
	var look func() error
	switch {
	case dev.Platform == "android" && app.Android != "":
		var last time.Time // the newest crash entry already read
		look = func() error {
			newest, err := newestDropboxEntry(ctx, dev)
			if err != nil || newest.Before(since) || !newest.After(last) {
				return err
			}
			rs, err := android(ctx, dev, app)
			if err == nil {
				last = newest
				report(rs)
			}
			return err
		}
	case dev.Kind == "simulator" && app.IOS != "":
		look = func() error {
			rs, err := simulator(dev, app)
			report(rs)
			return err
		}
	case dev.Platform == "ios" && app.IOS != "":
		pulled := map[string]bool{}
		look = func() error {
			_, names, err := deviceReportNames(ctx, dev, app)
			if err != nil {
				return err
			}
			var fresh []string
			for _, n := range names {
				if !pulled[n] {
					fresh = append(fresh, regexp.QuoteMeta(n))
				}
			}
			if len(fresh) == 0 {
				return nil
			}
			rs, err := pullReports(ctx, dev, app, "^("+strings.Join(fresh, "|")+")$")
			if err == nil {
				for _, n := range names {
					pulled[n] = true
				}
				report(rs)
			}
			return err
		}
	default:
		return
	}
	for {
		_ = look()
		select {
		case <-ctx.Done():
			return
		case <-time.After(PollEvery):
		}
	}
}

// 2026-11-10 15:02:12 data_app_native_crash (text, 2434 bytes)
var dropboxEntry = regexp.MustCompile(`(?m)^(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d) data_app_(?:crash|native_crash|anr) `)

// newestDropboxEntry is when the newest crash or ANR entry of any app was added to Android's
// dropbox, from its list of entries, which is short and quick to read.
func newestDropboxEntry(ctx context.Context, dev device.Device) (time.Time, error) {
	out, err := run.Output(ctx, "adb", "-s", dev.ID, "shell", "dumpsys", "dropbox")
	if err != nil {
		return time.Time{}, err
	}
	var newest time.Time
	for _, m := range dropboxEntry.FindAllStringSubmatch(out, -1) {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", m[1], time.Local); err == nil && t.After(newest) {
			newest = t
		}
	}
	return newest, nil
}
