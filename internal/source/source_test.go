package source

import (
	"bufio"
	"os"
	"testing"

	"github.com/davidamunga/saw/internal/config"
	"github.com/davidamunga/saw/internal/event"
)

func lines(t *testing.T, name string) []string {
	t.Helper()
	f, err := os.Open("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

func TestParseLogcatFixture(t *testing.T) {
	ls := lines(t, "android-logcat.txt")
	for _, l := range ls {
		if _, _, ok := ParseLogcat(l); !ok {
			t.Errorf("did not parse: %s", l)
		}
	}
	e, uid, _ := ParseLogcat(ls[20])
	if uid != "10710" || e.Tag != "ExampleHttp" || e.Level != event.Debug || e.PID != 9926 || e.TID != 9987 ||
		e.Message != "GET /v1/appointments 200 (143 ms)" || e.Time.Format("15:04:05.000") != "11:50:02.118" {
		t.Errorf("got %+v uid %s", e, uid)
	}
}

func TestAndroidUIDFilter(t *testing.T) {
	uids := uidNames(10710)
	var kept []string
	for _, l := range lines(t, "android-logcat.txt") {
		if e, uid, ok := ParseLogcat(l); ok && uids[uid] {
			kept = append(kept, e.Tag)
		}
	}
	// Every line but the shell's AndroidRuntime one; u0_a710 counts as the app.
	if len(kept) != 23 || kept[21] != "ExampleAuth" || kept[22] != "AndroidRuntime" {
		t.Errorf("kept %d: %v", len(kept), kept)
	}
}

func TestTagFor(t *testing.T) {
	c := &config.Config{}
	c.Android.Tag = "Example{Area}"
	if got := c.TagFor()("api"); got != "ExampleApi" {
		t.Error(got)
	}
}
