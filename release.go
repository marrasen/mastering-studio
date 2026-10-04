package main

import (
	"log"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/widget"
)

// Release is what an album, an EP or a single is released as: its
// artist, its title, its year and its genre, as its exports are tagged.
type Release struct {
	Artist, Title string
	Year, Genre   string `json:",omitempty"`
}

type (
	// EditRelease opens the release's dialog.
	EditRelease struct{}
	// SetRelease sets the release, from its dialog.
	SetRelease struct{ Release Release }
	// ReleaseClosed says the release's dialog closed.
	ReleaseClosed struct{}
)

// newReleaseDialog makes the dialog that names the release.
func newReleaseDialog(r Release) *widget.Dialog {
	d := widget.NewDialog("Release")
	field := func(text, hint string) *widget.TextField {
		f := widget.NewTextField()
		f.SetText(text)
		f.Placeholder = hint
		return f
	}
	artist := field(r.Artist, "Who it is by")
	title := field(r.Title, "The album, EP or single")
	year := field(r.Year, "2026")
	genre := field(r.Genre, "Electronic")
	form := widget.NewForm()
	form.Add("Artist", artist).Add("Title", title).Add("Year", year).Add("Genre", genre)
	d.Body = form
	d.SetButtons("Save", "Cancel")
	d.Dismiss = ReleaseClosed{}
	d.OnAccept = func() gunim.Intent {
		trim := strings.TrimSpace
		return SetRelease{Release: Release{Artist: trim(artist.Text()), Title: trim(title.Text()),
			Year: trim(year.Text()), Genre: trim(genre.Text())}}
	}
	return d
}

// editRelease opens the release's dialog, or closes it.
func (a *app) editRelease(open bool) {
	if a.c == nil || open == a.releasing {
		return
	}
	a.releasing = open
	var err error
	if open {
		err = a.c.Mount(gunim.Root, "release", "release", a.Release)
	} else {
		err = a.c.Unmount("release")
	}
	if err != nil {
		log.Print(err)
	}
}
