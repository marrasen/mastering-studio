package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/pprof"
	"time"

	"github.com/marrasen/gunim/install"
)

// Beta updates, and CPU profiles: what a tester needs to try a release
// before it is out, and to tell where the computer's time goes.

type (
	// SetBeta takes pre-releases as updates too, or not.
	SetBeta struct{ On bool }
	// RecordProfile records where the computer's time goes, a
	// profileLength, to a file to send.
	RecordProfile struct{}
	// ShowProfile shows the profile recorded last in its folder.
	ShowProfile struct{}
)

// profileLength is how long a CPU profile records.
const profileLength = time.Minute

// betaUpdates says the updates take pre-releases too, as the settings
// say. It is read as the updates look, so a change holds from the next.
func betaUpdates() bool {
	if d := configDir(); d != "" {
		return readSettings(filepath.Join(d, "settings.json")).Beta
	}
	return false
}

// checkNow looks for a newer release at once, as betas are taken up, and
// tells of one as the updates do.
func (a *app) checkNow() {
	if version == "dev" {
		return
	}
	go func() {
		r, newer, err := install.Check(a.ctx, installer())
		switch {
		case err != nil:
			log.Printf("mastering: looking for updates: %v", err)
		case newer:
			tell(news{release: r})
		}
	}()
}

// recordProfile starts a CPU profile, to stop a profileLength on.
func (a *app) recordProfile() {
	if a.Profiling || a.profileDir == "" {
		return
	}
	dir := a.profileDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.Note = err.Error()
		return
	}
	path := filepath.Join(dir, fmt.Sprintf("cpu-%s-%s.pprof", version, time.Now().Format("20060102-150405")))
	f, err := os.Create(path)
	if err != nil {
		a.Note = err.Error()
		return
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		a.Note = err.Error()
		return
	}
	a.Profiling, a.profile = true, path
	stop := func() {
		pprof.StopCPUProfile()
		_ = f.Close()
	}
	a.profiled = time.After(profileLength)
	a.stopProfile = stop
}

// profileDone stops the CPU profile, and tells where it is.
func (a *app) profileDone() {
	a.profiled = nil
	if a.stopProfile != nil {
		a.stopProfile()
		a.stopProfile = nil
	}
	a.Profiling = false
	a.Told, a.Tolds = "CPU profile saved: "+filepath.Base(a.profile), a.Tolds+1
}
