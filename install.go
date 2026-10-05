package main

import (
	"context"
	"embed"
	"fmt"
	"image"
	"strings"
	"sync/atomic"

	"github.com/marrasen/gunim/install"
)

// legal are the licence and the notices of the code the studio is made
// of, installed beside the program.
//
//go:embed LICENSE NOTICE
var legal embed.FS

// installer is how the studio installs itself. The program is all
// there is to download: started from anywhere but its own folder, it
// opens gunim's installer, which installs it for the user alone, with
// a Start menu or applications menu entry, an entry to uninstall it
// from, and projects that open in it with a double-click. Installed, it
// keeps itself up to date from the releases on GitHub, when the user
// chose that: by itself, or, unticked in the installer, asking first.
func installer() install.App {
	return install.App{
		Name:        appName,
		ID:          "mastering-studio",
		Version:     version,
		Publisher:   "Marcus Johansson",
		Description: "Masters an album, an EP or a single, through chains of VST3 plugins.",
		URL:         "https://github.com/marrasen/mastering-studio",
		IconFunc:    func() image.Image { return drawIcon(512) },
		Categories:  "AudioVideo;Audio;",
		Files:       legal,
		FileTypes:   []install.FileType{{Name: "Mastering project", Exts: []string{albumExt}, Default: true}},
		Updates:     install.GitHub{Repo: "marrasen/mastering-studio"},
		Available:   func(r install.Release) { tell(news{release: r}) },
		Updated:     func(r install.Release) { tell(news{release: r, ready: true}) },
		Data:        []string{configDir(), cacheDir()},
	}
}

// Update is the newer release the window tells of: one out, to ask
// whether to fetch, or one put in place, to offer the restart into. Seq
// counts the news, so the window tells each once.
type Update struct {
	Seq     int
	Version string
	Ready   bool
}

// news is what the updates tell the studio: a newer release, put in
// place already when ready, or what kept one from it.
type news struct {
	release install.Release
	ready   bool
	err     error
}

// updateNews carries the updates' news to the studio's own goroutine.
// The updates look on a goroutine of their own, from before the window
// opens.
var updateNews = make(chan news, 4)

// tell hands n to the studio, or drops it when news is already waiting:
// the next look tells again.
func tell(n news) {
	select {
	case updateNews <- n:
	default:
	}
}

// restartAfter says to start the release put in place once the studio
// has closed.
var restartAfter atomic.Bool

// stageUpdate fetches a release and puts it in place for the next start;
// a test sets it.
var stageUpdate = func(ctx context.Context, r install.Release) error {
	return install.Stage(ctx, installer(), r)
}

// newsOf shows what the updates told.
func (a *app) newsOf(n news) {
	if n.err != nil {
		a.Note = fmt.Sprintf("Couldn't update to %s: %v", strings.TrimPrefix(n.release.Version, "v"), n.err)
		return
	}
	a.offered = n.release
	a.Update = Update{Seq: a.Update.Seq + 1, Version: strings.TrimPrefix(n.release.Version, "v"), Ready: n.ready}
}

// fetchUpdate fetches the release told of, in the background, and tells
// again once it is in place.
func (a *app) fetchUpdate() {
	r := a.offered
	if r.Version == "" || a.Update.Ready {
		return
	}
	go func() {
		err := stageUpdate(a.ctx, r)
		updateNews <- news{release: r, ready: err == nil, err: err}
	}()
}
