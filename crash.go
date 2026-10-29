package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"
)

// Crashes: what the runtime says as the studio dies, kept on disk. The
// studio logs to stderr, which a studio started from the Start menu or
// a project's double-click has nowhere to send, so a panic's stack would
// be lost with it. The next studio to start shows what the last one
// said, to copy or report.

// crashFile is the file in configDir crashes are kept in.
const crashFile = "crash.log"

// mostCrashLog is how big the crash log may grow before it is started
// again.
const mostCrashLog = 1 << 20

// crashHeading starts the line each run of the studio writes as it
// starts, for a crash under it to say which studio it was and when it
// started.
const crashHeading = "--- " + appName + " "

// lastCrash is what the run of the studio before this one said as it
// crashed, under its heading, or "" when it did not; crashPath is the
// log it is in. keepCrashes finds both, before the window opens.
var lastCrash, crashPath string

// keepCrashes has the runtime write a fatal panic or error into the
// crash log as well as to stderr, from any goroutine.
func keepCrashes() {
	dir := configDir()
	if dir == "" {
		log.Print("mastering: crashes will not be kept: no folder for the settings")
		return
	}
	crashPath = filepath.Join(dir, crashFile)
	var err error
	if lastCrash, err = keepCrashesIn(crashPath, version, time.Now()); err != nil {
		log.Printf("mastering: crashes will not be kept: %v", err)
	}
}

// keepCrashesIn points crash output at the log at path, below a heading
// for this run of version, and returns the last run's crash when it had
// one.
func keepCrashesIn(path, version string, now time.Time) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	crash, err := forgetQuietRun(path)
	if err != nil {
		return "", err
	}
	// Appending, so what the runtime writes goes at the end whatever
	// else has written to the file.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return "", err
	}
	// SetCrashOutput keeps a copy of its own.
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "%s%s, started %s, process %d\n", crashHeading, version, now.Format(time.RFC3339), os.Getpid()); err != nil {
		return "", err
	}
	return crash, debug.SetCrashOutput(f, debug.CrashOptions{})
}

// forgetQuietRun takes the last run's heading off the log at path when
// nothing came after it, so only the runs that crashed are left, and
// returns the last run's crash when it had one.
func forgetQuietRun(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, mostCrashLog+1))
	if err != nil {
		return "", err
	}
	// Found before the log is cut: a log too big goes whole.
	crash := lastRunCrash(b)
	if n := keptOfCrashLog(b); n < len(b) {
		return crash, f.Truncate(int64(n))
	}
	return crash, nil
}

// keptOfCrashLog is how much of a crash log to keep: all of it but a
// last heading with nothing after it, or none once it is too big.
func keptOfCrashLog(b []byte) int {
	if len(b) > mostCrashLog {
		return 0
	}
	i := lastHeading(b)
	if i < 0 {
		return len(b)
	}
	if end := bytes.IndexByte(b[i:], '\n'); end >= 0 && i+end+1 == len(b) {
		return i
	}
	return len(b)
}

// lastRunCrash is the last run's part of a crash log, its heading and
// what came after it, when something did: what it said as it crashed.
// A log too big to have been read whole has none, its end unread.
func lastRunCrash(b []byte) string {
	if len(b) > mostCrashLog {
		return ""
	}
	i := lastHeading(b)
	if i < 0 {
		return ""
	}
	end := bytes.IndexByte(b[i:], '\n')
	if end < 0 || len(bytes.TrimSpace(b[i+end:])) == 0 {
		return ""
	}
	return string(b[i:])
}

// lastHeading is where the last run's heading starts in a crash log, at
// the start of the log when it is the only one, or -1 when there is
// none.
func lastHeading(b []byte) int {
	i := bytes.LastIndex(b, []byte("\n"+crashHeading)) + 1
	if !bytes.HasPrefix(b[i:], []byte(crashHeading)) {
		return -1
	}
	return i
}
