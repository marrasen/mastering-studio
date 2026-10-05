package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/widget"
)

// Saving: the work is kept as it goes, a second after each change, but
// a change of the user's goes to the project's draft, beside the
// settings, and the project's file takes it only as the user saves.
// What changes no work, as the track picked, how it is listened to, and
// its measures, goes to the file itself while it holds no change, so the
// next run need not measure again. A project never saved is its own
// draft, the untitled album beside the settings. On closing with
// changes not saved, the user is asked to save them, keep the draft for
// the next run, or let them go.

type (
	// SaveAlbum saves the album in its file, or, never saved, asks where.
	SaveAlbum struct{}
	// CloseAnswer is the user's answer to closing with changes not
	// saved.
	CloseAnswer struct{ Choice CloseChoice }
	// Unsaved is what the dialog of changes not saved shows.
	Unsaved struct {
		Name     string
		Untitled bool
	}
)

// CloseChoice is what the user chose on closing with changes not saved.
type CloseChoice int

const (
	CloseCancel CloseChoice = iota
	CloseSave
	CloseKeep
	CloseDiscard
)

// untitled says the album is the untitled one, never saved.
func (a *app) untitled() bool { return a.file != "" && a.file == untitled() }

// draftPath is where the album's changes not saved are kept: its own
// file, for the untitled album; or "" where there are no drafts.
func (a *app) draftPath() string {
	if a.untitled() {
		return a.file
	}
	if a.drafts == "" || a.file == "" {
		return ""
	}
	abs, err := filepath.Abs(a.file)
	if err != nil {
		abs = a.file
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(a.drafts, hex.EncodeToString(sum[:8])+albumExt)
}

// project is the album as a project keeps it: the track A plays its
// current one.
func (a *app) project() project {
	cur, looping := a.sideA()
	p := project{Release: a.Release, Gap: a.Gap, Target: a.Target, Bits: a.Bits, Dither: a.Dither,
		ExportDir: relative(a.file, a.ExportDir), ExportAt: a.ExportDir, ExportWAV: &a.ExportWAV, ExportMP3: a.ExportMP3,
		MP3Rate: a.MP3Rate, Report: a.ExportReport,
		Volume: a.Volume, Match: a.Match, Current: a.place(cur), AlbumPlay: a.AlbumPlay,
		Follow: a.Follow, View: a.View, Curves: &a.Curves, Looping: looping}
	for i := range a.Tracks {
		p.Tracks = append(p.Tracks, a.kept(&a.Tracks[i]))
	}
	// Session B's track.
	b := Session{Current: a.Current, Looping: a.Looping}
	if a.Side == SideA {
		b = a.Away
	}
	if k := (keptSession{Track: a.place(b.Current), Ref: indexOf(a.References, b.Current), Looping: b.Looping}); k.Track >= 0 || k.Ref >= 0 {
		p.B = &k
	}
	return p
}

// readProject reads the project at path.
func readProject(path string) (project, bool) {
	var p project
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &p) != nil {
		return project{}, false
	}
	return p, true
}

// writeProject writes p to path, whole or not at all.
func writeProject(path string, p project) error {
	b, err := json.MarshalIndent(p, "", "\t")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o755)
	}
	if err == nil {
		tmp := path + ".tmp"
		if err = os.WriteFile(tmp, b, 0o644); err == nil {
			err = os.Rename(tmp, path)
		}
	}
	return err
}

// newer says the file at path is there, and no older than the one at
// than, or than is not: a draft is let go as its album is saved, so one
// as new as its album, as file times run in steps, still holds changes.
func newer(path, than string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	ti, err := os.Stat(than)
	return err != nil || !fi.ModTime().Before(ti.ModTime())
}

// save keeps the work, as it changed: the references beside the
// settings, and the album in its draft while it holds changes not
// saved, and else in its file.
func (a *app) save() {
	if !a.dirty {
		return
	}
	a.readStates()
	a.saveRefs()
	if a.file == "" {
		return
	}
	a.dirty = false
	to, draft := a.file, a.draftPath()
	switch {
	case a.Unsaved && draft != "":
		to = draft
	case draft != "" && draft != a.file:
		// Changes undone to the album as saved: no draft holds any.
		_ = os.Remove(draft)
	}
	if err := writeProject(to, a.project()); err != nil {
		log.Printf("mastering: keeping the album: %v", err)
	}
}

// saveNow saves the album in its file, and lets its draft go.
func (a *app) saveNow() error {
	a.readStates()
	a.saveRefs()
	if err := writeProject(a.file, a.project()); err != nil {
		return err
	}
	if d := a.draftPath(); d != "" && d != a.file {
		_ = os.Remove(d)
	}
	a.savedID, a.Unsaved = a.stateID, false
	return nil
}

// discard lets the changes not saved go: the draft, or, of the untitled
// album, all of it. Nothing more of the album is kept as the program
// ends.
func (a *app) discard() {
	a.readStates()
	a.saveRefs()
	if d := a.draftPath(); d != "" {
		_ = os.Remove(d)
	}
	a.Unsaved, a.dirty = false, false
}

// askToClose asks what to do with the changes not saved, closing.
func (a *app) askToClose() {
	if a.c == nil || a.asking {
		return
	}
	a.asking = true
	if err := a.c.Mount(gunim.Root, "unsaved", "unsaved", Unsaved{Name: a.AlbumName, Untitled: a.untitled()}); err != nil {
		log.Print(err)
	}
}

// answered takes the dialog of changes not saved down, and does what the
// user chose: it returns whether the window is to close now.
func (a *app) answered(c CloseChoice) bool {
	if a.asking {
		a.asking = false
		if a.c != nil {
			_ = a.c.Unmount("unsaved")
		}
	}
	switch c {
	case CloseKeep:
		a.dirty = true
		a.save()
		return true
	case CloseDiscard:
		a.discard()
		return true
	case CloseSave:
		if a.untitled() {
			// Saved where the user says, then closed.
			a.quitAfterSave = true
			a.chooseAlbum(SaveAlbumAs{})
			return false
		}
		if err := a.saveNow(); err != nil {
			a.Note = err.Error()
			return false
		}
		return true
	case CloseCancel:
	}
	return false
}

// newUnsavedDialog makes the dialog that asks what to do with the
// changes not saved, closing.
func newUnsavedDialog(s Unsaved) *widget.Dialog {
	d := widget.NewDialog("Unsaved changes")
	d.Body = widget.NewLabel("You have unsaved changes in " + s.Name + ".")
	save := "Save"
	if s.Untitled {
		save = "Save as…"
	}
	d.SetButtons(save, "Cancel")
	d.Accept = CloseAnswer{Choice: CloseSave}
	d.Dismiss = CloseAnswer{Choice: CloseCancel}
	d.AddButton("Discard changes", func() gunim.Intent { return CloseAnswer{Choice: CloseDiscard} })
	d.AddButton("Keep draft", func() gunim.Intent { return CloseAnswer{Choice: CloseKeep} })
	return d
}
