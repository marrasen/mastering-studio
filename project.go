package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/driver"
)

// Albums are files of their own, of the extension albumExt, kept where
// the user puts them; the settings remember the albums opened last and
// the plugins added last, across them.

// albumExt is an album's file's extension.
const albumExt = ".mastering"

// albumFilter offers album files in the system's dialogs.
var albumFilter = []driver.FileFilter{{Name: "Mastering project", Patterns: []string{"*" + albumExt}}}

// recentAlbums is how many albums opened last the menu lists.
const recentAlbums = 8

// settings is what the program keeps across albums.
type settings struct {
	// Albums are the albums opened last, the latest first.
	Albums []string
	// Plugins are the plugins added last, the latest first.
	Plugins []PluginChoice `json:",omitempty"`
	// LAME is where LAME was located, for MP3s.
	LAME string `json:",omitempty"`
}

// configDir is where the program keeps its settings, and the untitled
// album.
func configDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "gunim-mastering")
}

// untitled is the album kept before one is saved anywhere.
func untitled() string {
	if d := configDir(); d != "" {
		return filepath.Join(d, "album.json")
	}
	return ""
}

func readSettings(path string) settings {
	var s settings
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

// writeSettings keeps the settings, if the app has a file for them.
func (a *app) writeSettings() {
	if a.settingsFile == "" {
		return
	}
	s := settings{Albums: a.RecentAlbums, Plugins: a.Recent, LAME: a.lame}
	b, err := json.MarshalIndent(s, "", "\t")
	if err == nil && os.MkdirAll(filepath.Dir(a.settingsFile), 0o755) == nil {
		_ = os.WriteFile(a.settingsFile, b, 0o644)
	}
}

// albumName is what an album's file is called, as the header shows it.
func albumName(path string) string {
	if path == "" || path == untitled() {
		return "Untitled project"
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// opened puts the album at path first among those opened last.
func (a *app) opened(path string) {
	a.AlbumFile, a.AlbumName = path, albumName(path)
	if path == "" || path == untitled() {
		return
	}
	a.RecentAlbums = slices.DeleteFunc(a.RecentAlbums, func(p string) bool { return p == path })
	a.RecentAlbums = slices.Insert(a.RecentAlbums, 0, path)
	if len(a.RecentAlbums) > recentAlbums {
		a.RecentAlbums = a.RecentAlbums[:recentAlbums]
	}
	a.writeSettings()
}

// withExt is path, with the album's extension where it has none.
func withExt(path string) string {
	if filepath.Ext(path) == "" {
		return path + albumExt
	}
	return path
}

// relative is the path as an album at album keeps it: from the album's
// folder, up out of it where need be, as "../Mixes/a.wav", so the album
// opens wherever its folders are found together, as a cloud folder
// mounted at another path on another computer. A path the album's
// cannot reach, as on another drive, stays whole.
func relative(album, path string) string {
	if album == "" || album == untitled() || path == "" {
		return path
	}
	rel, err := filepath.Rel(filepath.Dir(album), path)
	if err != nil || filepath.IsAbs(rel) {
		return path
	}
	return filepath.ToSlash(rel)
}

// absolute is the path an album at album keeps, as a path.
func absolute(album, path string) string {
	if path == "" || filepath.IsAbs(path) || album == "" {
		return path
	}
	return filepath.Join(filepath.Dir(album), filepath.FromSlash(path))
}

// found is the path an album at album keeps as rel, or, as it was
// whole, abs: the first that is there, or rel's.
func found(album, rel, abs string) string {
	p := absolute(album, rel)
	if _, err := os.Stat(p); err != nil && abs != "" {
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	return p
}

// switchTo leaves the album open, kept, for the one at path: opened from
// its file, or, fresh, made empty there.
func (a *app) switchTo(path string, fresh bool) {
	if path == a.file {
		return
	}
	a.save()
	a.d.stop()
	a.Playing = false
	for id, cancel := range a.measurer {
		cancel()
		delete(a.measurer, id)
	}
	clear(a.settle)
	a.closeRacks()
	clear(a.states)
	keep := a.Album
	a.Album = Album{Gap: keep.Gap, Target: keep.Target, Bits: keep.Bits, Dither: keep.Dither, Volume: keep.Volume,
		Plugins: keep.Plugins, Scanning: keep.Scanning, Recent: keep.Recent, RecentAlbums: keep.RecentAlbums,
		Follow: keep.Follow}
	a.queued = queuedKey{}
	a.file = path
	if fresh {
		a.dirty = true
		a.save()
	} else {
		a.load()
	}
	a.opened(path)
	a.applyLevel()
}

// saveAs keeps the album open at path from now on.
func (a *app) saveAs(path string) {
	a.file = path
	a.dirty = true
	a.save()
	a.opened(path)
}

// chooseAlbum asks, with the system's dialog, for an album: to open, or
// where to make one, or save this one as; then does it.
func (a *app) chooseAlbum(in gunim.Intent) {
	go func() {
		var path string
		var err error
		switch in.(type) {
		case OpenAlbum:
			var paths []string
			paths, err = a.choose(driver.ChooseOptions{Title: "Open project", Filters: albumFilter})
			if len(paths) > 0 {
				path = paths[0]
			}
		case NewAlbum:
			path, err = a.saveDialog(driver.SaveOptions{Title: "New project", Name: "Project" + albumExt, Filters: albumFilter})
		case SaveAlbumAs:
			path, err = a.saveDialog(driver.SaveOptions{Title: "Save project as", Name: a.AlbumName + albumExt,
				Filters: albumFilter})
		}
		if err != nil || path == "" {
			return
		}
		select {
		case a.albums <- albumChoice{in: in, path: withExt(path)}:
		case <-a.ctx.Done():
		}
	}()
}

// albumChoice is an album chosen in a dialog, for what.
type albumChoice struct {
	in   gunim.Intent
	path string
}

// chosenAlbum does what the album was chosen for.
func (a *app) chosenAlbum(c albumChoice) {
	switch c.in.(type) {
	case OpenAlbum:
		a.switchTo(c.path, false)
	case NewAlbum:
		a.switchTo(c.path, true)
	case SaveAlbumAs:
		a.saveAs(c.path)
	}
}
