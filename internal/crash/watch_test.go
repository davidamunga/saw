package crash

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/device"
	"github.com/davidamunga/saw/internal/runtest"
)

// watchFor runs Watch until it has reported want reports, then a little longer to catch any
// reported twice, and returns their summaries. Wanting none, it watches briefly.
func watchFor(t *testing.T, dev device.Device, app config.App, since time.Time, want int) []string {
	t.Helper()
	old := PollEvery
	PollEvery = 10 * time.Millisecond
	t.Cleanup(func() { PollEvery = old })
	limit := 30 * time.Second
	if want == 0 {
		limit = 500 * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	var mu sync.Mutex
	var got []string
	Watch(ctx, dev, app, since, func(r Report) {
		mu.Lock()
		defer mu.Unlock()
		if r.Device != dev.Name {
			t.Errorf("device: %+v", r)
		}
		got = append(got, r.Kind+" "+r.Summary)
		if len(got) == want {
			time.AfterFunc(300*time.Millisecond, cancel)
		}
	})
	return got
}

func TestWatchAndroid(t *testing.T) {
	runtest.Install(t,
		runtest.Rule{Cmd: "adb -s S1 shell dumpsys dropbox --print data_app_crash", Stdout: fixture(t, "dropbox-crash.txt")},
		runtest.Rule{Cmd: "adb -s S1 shell dumpsys dropbox --print data_app_native_crash", Stdout: fixture(t, "dropbox-native.txt")},
		runtest.Rule{Cmd: "adb -s S1 shell dumpsys dropbox --print data_app_anr", Stdout: fixture(t, "dropbox-anr.txt")},
		runtest.Rule{Cmd: "adb -s S1 shell dumpsys dropbox", Stdout: "Drop box contents: 3 entries\n" +
			"2026-11-09 14:20:05 data_app_anr (compressed text, 2400 bytes)\n" +
			"2026-11-10 15:02:07 data_app_crash (text, 954 bytes)\n" +
			"2026-11-10 15:02:12 data_app_native_crash (text, 2434 bytes)\n" +
			"2026-11-10 15:03:00 system_server_wtf (text, 100 bytes)\n"},
	)
	// Reports from since on, each once however many times it looks.
	got := watchFor(t, pixel, app, time.Date(2026, 11, 10, 15, 2, 7, 600e6, time.Local), 2)
	if len(got) != 2 || got[0] != "crash android.app.RemoteServiceException: shell-induced crash" || got[1] != "native SIGSEGV in libc.so (__epoll_pwait+8)" {
		t.Errorf("got %q", got)
	}
	// Nothing new since then: nothing.
	if got := watchFor(t, pixel, app, time.Date(2026, 11, 10, 16, 0, 0, 0, time.Local), 0); len(got) != 0 {
		t.Errorf("got %q", got)
	}
}

func TestWatchSimulator(t *testing.T) {
	dir := t.TempDir()
	old := reportDirs
	reportDirs = func() []string { return []string{dir} }
	t.Cleanup(func() { reportDirs = old })
	if err := os.WriteFile(filepath.Join(dir, "Example-1.ips"), []byte(fixture(t, "sim-crash.ips")), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := watchFor(t, sim, app, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 1); len(got) != 1 {
		t.Errorf("got %q", got)
	}
	if got := watchFor(t, sim, app, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), 0); len(got) != 0 {
		t.Errorf("an old report: got %q", got)
	}
}

func TestWatchIOSDevice(t *testing.T) {
	src := filepath.Join(t.TempDir(), "Example-2026-11-10-150334.ips")
	if err := os.WriteFile(src, []byte(fixture(t, "sim-crash.ips")), 0o644); err != nil {
		t.Fatal(err)
	}
	runtest.Install(t,
		runtest.Rule{Cmd: "pymobiledevice3 apps query --udid U1 com.example.app", Stdout: `{"com.example.app": {"CFBundleExecutable": "Example"}}`},
		runtest.Rule{Cmd: "pymobiledevice3 crash ls --udid U1", Stdout: "/Other-2026-11-10-150000.ips\n/Example-2026-11-10-150334.ips\n"},
		runtest.Rule{Cmd: `pymobiledevice3 crash pull --udid U1 --match ^(Example-2026-11-10-150334\.ips)$`, Copy: src},
	)
	if got := watchFor(t, iphone, app, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), 1); len(got) != 1 {
		t.Errorf("got %q", got)
	}
}

func TestWatchNothingToWatch(t *testing.T) {
	// The app isn't configured for the device's platform: Watch returns at once.
	done := make(chan struct{})
	go func() {
		Watch(context.Background(), pixel, config.App{Name: "x", IOS: "com.x"}, time.Now(), func(Report) {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("Watch should return when there's nothing to watch")
	}
}
