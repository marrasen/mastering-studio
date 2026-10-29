package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestTheCrashLogKeepsOnlyRunsThatCrashed(t *testing.T) {
	heading := func(n string) string { return crashHeading + n + ", started then, process 1\n" }
	crash := "panic: runtime error: index out of range [-1]\n\ngoroutine 1 [running]:\nmain.main()\n"
	for _, c := range []struct {
		name, log, want string
	}{
		{"empty", "", ""},
		{"a quiet run", heading("a"), ""},
		{"a crash", heading("a") + crash, heading("a") + crash},
		{"a quiet run after a crash", heading("a") + crash + heading("b"), heading("a") + crash},
		{"a heading cut short", heading("a") + crash + strings.TrimSuffix(heading("b"), "\n"), heading("a") + crash + strings.TrimSuffix(heading("b"), "\n")},
		{"something else", "not the studio's\n", "not the studio's\n"},
		{"too big", strings.Repeat("x", mostCrashLog+1), ""},
	} {
		if got := c.log[:keptOfCrashLog([]byte(c.log))]; got != c.want {
			t.Errorf("%s: kept %q, want %q", c.name, got, c.want)
		}
	}
}

func TestALastRunIsFoundToHaveCrashedByWhatCameAfterItsHeading(t *testing.T) {
	heading := func(n string) string { return crashHeading + n + ", started then, process 1\n" }
	crash := "panic: oh no\n\ngoroutine 1 [running]:\nmain.main()\n"
	for _, c := range []struct {
		name, log, want string
	}{
		{"empty", "", ""},
		{"a quiet run", heading("a"), ""},
		{"a crash", heading("a") + crash, heading("a") + crash},
		{"a quiet run after a crash", heading("a") + crash + heading("b"), ""},
		{"a crash after a crash", heading("a") + crash + heading("b") + crash, heading("b") + crash},
		{"blank lines only", heading("a") + "\n\n", ""},
		{"something else", "not the studio's\n", ""},
		{"too big", heading("a") + strings.Repeat("x", mostCrashLog), ""},
	} {
		if got := lastRunCrash([]byte(c.log)); got != c.want {
			t.Errorf("%s: found %q, want %q", c.name, got, c.want)
		}
	}
}

func TestEachStartWritesItsHeadingUnderTheLastCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "studio", crashFile)
	then := time.Unix(0, 0)
	// The runtime holds the log open until told of no other, and
	// Windows will not delete a file held open.
	t.Cleanup(func() { _ = debug.SetCrashOutput(nil, debug.CrashOptions{}) })
	for range 2 {
		if crash, err := keepCrashesIn(path, "1.2.3", then); err != nil || crash != "" {
			t.Fatalf("a quiet start found the crash %q, %v", crash, err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); strings.Count(got, crashHeading) != 1 || !strings.HasPrefix(got, crashHeading+"1.2.3, started ") {
		t.Fatalf("after two quiet starts the log is %q, want one heading", got)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("panic: oh no\n")
	_ = f.Close()
	crash, err := keepCrashesIn(path, "1.2.3", then)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(crash, crashHeading+"1.2.3, started ") || !strings.HasSuffix(crash, "\npanic: oh no\n") {
		t.Fatalf("the start after a crash found %q, want the crash under its heading", crash)
	}
	b, _ = os.ReadFile(path)
	if got := string(b); strings.Count(got, crashHeading) != 2 || !strings.Contains(got, "panic: oh no\n"+crashHeading) {
		t.Fatalf("after a crash and a start the log is %q, want the crash under its heading and a new one", got)
	}
	// Shown once: the start after a quiet run finds none, though the
	// crash before it is still in the log.
	if crash, err := keepCrashesIn(path, "1.2.3", then); err != nil || crash != "" {
		t.Fatalf("the start after a quiet run found the crash %q, %v", crash, err)
	}
}

// A panic on a goroutine of its own reaches the log: the test binary
// run again, to keep its crashes there and panic.
func TestAPanicIsKeptInTheCrashLog(t *testing.T) {
	if path := os.Getenv("MASTERING_CRASH_TO"); path != "" {
		if _, err := keepCrashesIn(path, "1.2.3", time.Now()); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			rows := []int{}
			at := -1
			_ = rows[at]
		}()
		<-done
		return
	}
	path := filepath.Join(t.TempDir(), crashFile)
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestAPanicIsKeptInTheCrashLog$")
	cmd.Env = append(os.Environ(), "MASTERING_CRASH_TO="+path)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("the run meant to panic did not fail:\n%s", out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); !strings.HasPrefix(got, crashHeading) || !strings.Contains(got, "panic: runtime error: index out of range [-1]") {
		t.Fatalf("the crash log is %q, want the heading and the panic", got)
	}
}
