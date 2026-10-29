package main

import (
	"log"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// The last crash, shown as the studio starts: what the studio said as it
// died, in a dialog, to copy or to report on GitHub.

type (
	// Crash is what the crash's dialog shows: the trace, tidied; which
	// studio it was and where the log is; and all of the run's part of
	// the log, to copy.
	Crash struct{ Trace, About, Copy string }
	// ReportCrash opens a new GitHub issue about the crash shown.
	ReportCrash struct{}
	// CrashClosed says the crash's dialog closed.
	CrashClosed struct{}
)

// newIssue is where a GitHub issue about the studio is written.
const newIssue = "https://github.com/marrasen/mastering-studio/issues/new"

// mostIssueLink is how long the link to a new issue may be. GitHub turns
// away a much longer one, and some browsers cut it.
const mostIssueLink = 7000

// crashGap sets the line under the trace apart from it.
var crashGap = theme.Length("studio.crash.gap", 16)

// newCrashDialog makes the dialog that shows the last run's crash.
func newCrashDialog(c Crash) *widget.Dialog {
	d := widget.NewDialog(appName + " crashed")
	d.Width = 760
	trace := widget.NewLabel(c.Trace)
	trace.Face = widget.MonoFont
	trace.NoWrap = true
	about := widget.NewLabel(c.About)
	about.Color = widget.MenuHint
	body := widget.Column(trace, about)
	body.Gap = crashGap
	d.Body = body
	d.SetButtons("Close", "")
	d.OnAccept, d.OnDismiss = widget.Sends(CrashClosed{}), widget.Sends(CrashClosed{})
	d.AddAction("Copy", func(u *gunim.UI) gunim.Intent {
		u.SetClipboard(c.Copy)
		return nil
	})
	d.AddAction("Report on GitHub", widget.Sends(ReportCrash{}))
	return d
}

// sayCrashed shows what the last run said as it crashed, once.
func (a *app) sayCrashed() {
	crash := lastCrash
	lastCrash = ""
	if crash == "" || a.c == nil {
		return
	}
	c, link := crashShown(crash, crashPath)
	a.crashLink = link
	if err := a.c.Mount(gunim.Root, "crash", "crash", c); err != nil {
		log.Print(err)
	}
}

// crashClosed closes the crash's dialog.
func (a *app) crashClosed() {
	a.crashLink = ""
	if a.c == nil {
		return
	}
	if err := a.c.Unmount("crash"); err != nil {
		log.Print(err)
	}
}

// reportCrash opens a new GitHub issue about the crash shown.
func (a *app) reportCrash() {
	if a.crashLink == "" || a.openLink == nil {
		return
	}
	if err := a.openLink(a.crashLink); err != nil {
		a.Note = err.Error()
	}
}

// crashShown is what the dialog shows of a crash, the last run's part
// of the crash log at path, and the link to report it.
func crashShown(crash, path string) (shown Crash, link string) {
	heading, trace, _ := strings.Cut(crash, "\n")
	ran, started := crashRun(heading)
	about := appName + " " + ran
	if !started.IsZero() {
		about += ", started " + started.Format("15:04 on 2 January")
	}
	about += ", stopped saying this. All of it is in " + path + "."
	return Crash{Trace: tidyTrace(strings.TrimSpace(trace)), About: about, Copy: crash}, crashIssue(ran, trace)
}

// crashRun reads a run's heading: the version it was, and when it
// started, zero when the heading does not say.
func crashRun(heading string) (version string, started time.Time) {
	rest := strings.TrimPrefix(heading, crashHeading)
	version, rest, _ = strings.Cut(rest, ", started ")
	at, _, _ := strings.Cut(rest, ", process ")
	started, _ = time.Parse(time.RFC3339, at)
	return version, started
}

// crashIssue is the link to a new GitHub issue about a crash, written
// for the user to say what they were doing: the version, the system,
// and the trace, cut short to fit in a link.
func crashIssue(version, trace string) string {
	lines := strings.Split(tidyTrace(strings.TrimSpace(trace)), "\n")
	title := "Crash: " + strings.TrimPrefix(lines[0], "panic: ")
	link := func(lines []string, cut bool) string {
		body := appName + " " + version + " crashed, on " + runtime.GOOS + "/" + runtime.GOARCH + ".\n\n" +
			"**What I was doing:**\n\n\n\n" +
			"**What the studio said:**\n\n```\n" + strings.Join(lines, "\n") + "\n```\n"
		if cut {
			body += "\nCut short to fit. The rest is in the studio's " + crashFile + ".\n"
		}
		return newIssue + "?" + url.Values{"title": {title}, "body": {body}, "labels": {"bug"}}.Encode()
	}
	at := link(lines, false)
	for n := len(lines) - 1; len(at) > mostIssueLink && n > 1; n-- {
		at = link(lines[:n], true)
	}
	return at
}

// tidyTrace takes from a Go stack trace what says nothing to its
// reader: each call's arguments, as words in memory, and each line's
// offset into its function's code.
func tidyTrace(trace string) string {
	lines := strings.Split(trace, "\n")
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "\t"):
			// A file and line, then " +0x1a2".
			if at := strings.LastIndex(l, " +0x"); at >= 0 {
				lines[i] = l[:at]
			}
		case strings.HasSuffix(l, ")") && !strings.HasPrefix(l, "goroutine ") && !strings.HasPrefix(l, "panic: "):
			lines[i] = withoutArgs(l)
		}
	}
	return strings.Join(lines, "\n")
}

// withoutArgs is a call's line, as pkg.(*T).F(0x1, 0x2), with what is
// between its last parentheses taken out. A method's receiver is in
// parentheses too, as are a closure's, so the arguments are those after
// the last ")." or, with none, after the last "/".
func withoutArgs(l string) string {
	from := strings.LastIndex(l, ").")
	if from < 0 {
		from = strings.LastIndex(l, "/")
	}
	open := strings.IndexByte(l[from+1:], '(')
	if open < 0 {
		return l
	}
	return l[:from+1+open] + "(…)"
}
