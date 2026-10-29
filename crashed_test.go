package main

import (
	"context"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/widget"
)

// What a crash shows: its trace without the words in memory, which
// studio it was and where the log is, all of it to copy, and the link.
func TestACrashIsShownTidiedWithItsRun(t *testing.T) {
	crash := crashHeading + "1.2.3, started 2026-10-29T13:04:42+01:00, process 7\n" +
		"panic: runtime error: index out of range [9] with length 9\n\n" +
		"goroutine 166 [running]:\n" +
		"github.com/marrasen/gunim/widget.(*Form).Layout(0x9c8d22a4108, {{0x0?, 0x0?}}, ...)\n" +
		"\tgithub.com/marrasen/gunim@v0.0.0/widget/form.go:77 +0xa05\n" +
		"github.com/marrasen/gunim/widget.Sync[...](0x2943c2c6a680, {0x2943c2fcc008, 0xa})\n" +
		"main.(*app).handle.func2(_, {0x2, 0x0})\n" +
		"panic({0x7ff7213be2e0?, 0x7ff720b53e60?})\n" +
		"created by github.com/marrasen/gunim.(*App).NewWindow in goroutine 62\n"
	c, link := crashShown(crash, "/where/crash.log")
	want := "panic: runtime error: index out of range [9] with length 9\n\n" +
		"goroutine 166 [running]:\n" +
		"github.com/marrasen/gunim/widget.(*Form).Layout(…)\n" +
		"\tgithub.com/marrasen/gunim@v0.0.0/widget/form.go:77\n" +
		"github.com/marrasen/gunim/widget.Sync[...](…)\n" +
		"main.(*app).handle.func2(…)\n" +
		"panic(…)\n" +
		"created by github.com/marrasen/gunim.(*App).NewWindow in goroutine 62"
	if c.Trace != want {
		t.Errorf("the trace shown is\n%s\nwant\n%s", c.Trace, want)
	}
	if c.Copy != crash {
		t.Errorf("Copy copies %q, want all of the run's part of the log", c.Copy)
	}
	if !strings.HasPrefix(c.About, appName+" 1.2.3, started 13:04 on 29 October,") || !strings.Contains(c.About, "/where/crash.log") {
		t.Errorf("under it, %q", c.About)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if body := u.Query().Get("body"); !strings.HasPrefix(link, newIssue+"?") || !strings.Contains(body, "widget.(*Form).Layout(…)") || !strings.Contains(body, appName+" 1.2.3 crashed") {
		t.Errorf("the issue's body is %q, want the version and the tidied trace", body)
	}
}

// A trace too long for a link is cut short to fit, and says so.
func TestACrashTooLongForALinkIsCutShort(t *testing.T) {
	trace := "panic: oh no\n\ngoroutine 1 [running]:\n" + strings.Repeat("main.deeper()\n\t/src/main.go:10\n", 2000)
	at := crashIssue("1.2.3", trace)
	if len(at) > mostIssueLink {
		t.Fatalf("the link is %d long, past %d", len(at), mostIssueLink)
	}
	u, err := url.Parse(at)
	if err != nil {
		t.Fatal(err)
	}
	if body := u.Query().Get("body"); !strings.Contains(body, "panic: oh no") || !strings.Contains(body, "Cut short to fit") {
		t.Fatalf("the issue's body is %q", body)
	}
	if got := u.Query().Get("title"); got != "Crash: oh no" {
		t.Fatalf("the issue's title is %q", got)
	}
}

// The crash the last run left is shown as the studio starts, once; its
// Report opens the issue's link, and Escape closes it.
func TestTheLastRunsCrashIsShownOnceAndReported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	w := gunim.NewOffscreen(geom.Sz(1000, 700), nil)
	var shown []Crash
	gunim.RegisterView(w, "crash", func(c Crash) *widget.Dialog {
		shown = append(shown, c)
		return newCrashDialog(c)
	}, nil)
	c := w.Client()
	a.c = &c
	var opened []string
	a.openLink = func(at string) error { opened = append(opened, at); return nil }
	lastCrash, crashPath = crashHeading+"1.2.3, started 2026-10-29T13:04:42+01:00, process 7\npanic: oh no\n", "/where/crash.log"
	t.Cleanup(func() { lastCrash, crashPath = "", "" })
	run := func(n int) {
		for range n {
			w.Frame(time.Second / 60)
		}
	}

	a.sayCrashed()
	run(30)
	if len(shown) != 1 || shown[0].Trace != "panic: oh no" {
		t.Fatalf("shown %+v, want the crash once", shown)
	}
	a.handle(ReportCrash{})
	if _, link := crashShown(shown[0].Copy, crashPath); len(opened) != 1 || opened[0] != link {
		t.Fatalf("Report opened %q, want %q", opened, link)
	}
	w.Input(input.KeyPress{Key: input.KeyEscape})
	run(30)
	if _, rest := edits(w); !reflect.DeepEqual(rest, []gunim.Intent{CrashClosed{}}) {
		t.Fatalf("Escape sent %v, want CrashClosed", rest)
	}
	a.handle(CrashClosed{})
	a.sayCrashed()
	run(30)
	a.handle(ReportCrash{})
	if len(shown) != 1 || len(opened) != 1 {
		t.Fatalf("after closing, shown %d times and reported %d, want once each", len(shown), len(opened))
	}
}
