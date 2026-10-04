package main

import (
	"fmt"
	"log"
	"slices"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/widget"
)

// The export's dialog: where to, which tracks, and as what.

type (
	// ExportDraft is what the export's dialog shows.
	ExportDraft struct {
		Dir    string
		Tracks []ExportTrack
		// Bits and Dither are the WAV's samples; WAV and MP3 say which
		// files to write, MP3 at MP3Rate kbps where HaveMP3 says an
		// encoder is at hand.
		Bits         int
		Dither       bool
		WAV, MP3     bool
		MP3Rate      int
		HaveMP3      bool
		LAME         string
		MP3Rates     []int
		Measuring    int
		ExportingNow bool
	}
	// ExportTrack is a track as the dialog lists it, ticked to export.
	ExportTrack struct {
		ID, Number int
		Title      string
		Ticked     bool
	}

	// OpenExport opens the export's dialog, the tracks named ticked, or
	// every one.
	OpenExport struct{ IDs []int }
	// ExportClosed says the export's dialog closed.
	ExportClosed struct{}
	// LocateLAME asks, with the system's dialog, where LAME is.
	LocateLAME struct{}
	// StartExport exports the tracks ticked, as the dialog says.
	StartExport struct {
		IDs      []int
		Bits     int
		Dither   bool
		WAV, MP3 bool
		MP3Rate  int
	}
)

// exportTopic is what the export's dialog watches.
const exportTopic = "export"

// bitsChoices are the WAV's samples the dialog offers, as it names them.
var bitsChoices = []bitsChoice{{16, "16-bit"}, {24, "24-bit"}, {32, "32-bit float"}}

type bitsChoice struct {
	bits int
	name string
}

// exportDialog is the export's dialog, with what it holds.
type exportDialog struct {
	*widget.Dialog
	dir  *widget.Label
	bits *widget.Dropdown
	rate *widget.Dropdown
	mp3  *widget.Checkbox
	// note says MP3s need LAME, with locate to say where it is, while
	// it is not found.
	note   *widget.Label
	locate *widget.Button
}

func newExportDialog(d ExportDraft) *exportDialog {
	dlg := widget.NewDialog("Export")
	e := &exportDialog{Dialog: dlg, dir: widget.NewLabel(d.Dir)}
	e.dir.MaxLines = 2
	change := widget.NewButton("Change…")
	change.On = ChooseExportDir{}
	ticks := make([]*widget.Checkbox, len(d.Tracks))
	col := make([]gunim.Node, 0, len(d.Tracks)+1)
	for i, t := range d.Tracks {
		ticks[i] = widget.NewCheckbox(fmt.Sprintf("%02d  %s", t.Number, t.Title))
		ticks[i].On = t.Ticked
		col = append(col, ticks[i])
	}
	all, none := widget.NewButton("All"), widget.NewButton("None")
	set := func(on bool) func(u *gunim.UI) {
		return func(u *gunim.UI) {
			for _, c := range ticks {
				c.On = on
			}
			u.Invalidate()
		}
	}
	all.OnActivate(set(true))
	none.OnActivate(set(false))
	col = append(col, widget.Row(all, none))

	wav := widget.NewCheckbox("WAV")
	wav.On = d.WAV
	names := make([]string, len(bitsChoices))
	for i, b := range bitsChoices {
		names[i] = b.name
	}
	e.bits = widget.NewDropdown(names...)
	e.bits.Selected = max(0, slices.IndexFunc(bitsChoices, func(b bitsChoice) bool { return b.bits == d.Bits }))
	dither := widget.NewCheckbox("Dither")
	dither.On = d.Dither
	mp3 := widget.NewCheckbox("MP3")
	e.mp3 = mp3
	mp3.On = d.MP3 && d.HaveMP3
	rates := make([]string, len(d.MP3Rates))
	for i, r := range d.MP3Rates {
		rates[i] = fmt.Sprintf("%d kbps", r)
	}
	e.rate = widget.NewDropdown(rates...)
	e.rate.Selected = max(0, slices.Index(d.MP3Rates, d.MP3Rate))
	e.note = widget.NewLabel("MP3s are encoded by LAME, which is not found.")
	e.note.Color, e.note.MaxLines = widget.PaletteHint, 2
	e.locate = widget.NewButton("Locate LAME…")
	e.locate.On = LocateLAME{}
	e.lame(d.LAME)
	mp3Row := widget.Row(mp3, e.rate)
	form := widget.NewForm()
	form.Add("Folder", widget.Column(e.dir, change)).
		Add("Tracks", widget.Column(col...)).
		Add("Format", widget.Row(wav, e.bits, dither)).
		Add("", mp3Row).
		Add("", widget.Column(e.note, e.locate))
	dlg.Body = form
	dlg.SetButtons("Export", "Cancel")
	dlg.Dismiss = ExportClosed{}
	ticked := func() []int {
		var ids []int
		for i, c := range ticks {
			if c.On {
				ids = append(ids, d.Tracks[i].ID)
			}
		}
		return ids
	}
	dlg.Check = func() string {
		switch {
		case len(ticked()) == 0:
			return "Tick a track to export."
		case !wav.On && !mp3.On:
			return "Choose WAV, MP3 or both."
		}
		return ""
	}
	dlg.OnAccept = func() gunim.Intent {
		s := StartExport{IDs: ticked(), Bits: bitsChoices[max(0, e.bits.Selected)].bits, Dither: dither.On,
			WAV: wav.On, MP3: mp3.On}
		if len(d.MP3Rates) > 0 {
			s.MP3Rate = d.MP3Rates[max(0, min(e.rate.Selected, len(d.MP3Rates)-1))]
		}
		return s
	}
	return e
}

// show takes the draft anew, as the folder changes, or LAME is found.
func (e *exportDialog) show(d ExportDraft, u *gunim.UI) {
	e.dir.SetText(d.Dir)
	e.lame(d.LAME)
	u.Invalidate()
}

// lame shows MP3 as LAME is found, at path, or not.
func (e *exportDialog) lame(path string) {
	found := path != ""
	e.mp3.Disabled, e.rate.Disabled = !found, !found
	if found {
		e.note.SetText("Encoded by LAME, at " + path)
		return
	}
	e.mp3.On = false
	e.note.SetText("MP3s are encoded by LAME, which is not found.")
}

// The app's half of the dialog.

// mp3Rates are the bitrates offered for MP3.
var mp3Rates = []int{320, 256, 192}

// exportDraft is the export's dialog's state, the tracks ids ticked, or
// every one.
func (a *app) exportDraft(ids []int) ExportDraft {
	d := ExportDraft{Dir: a.exportDir(), Bits: a.Bits, Dither: a.Dither, WAV: a.ExportWAV, MP3: a.ExportMP3,
		MP3Rate: a.MP3Rate, HaveMP3: encodeMP3 != nil, MP3Rates: mp3Rates, LAME: lamePath}
	for i, t := range a.Tracks {
		d.Tracks = append(d.Tracks, ExportTrack{ID: t.ID, Number: i + 1, Title: t.Title,
			Ticked: len(ids) == 0 || slices.Contains(ids, t.ID)})
	}
	return d
}

// openExport opens the export's dialog, or closes it.
func (a *app) openExport(open bool, ids []int) {
	if a.c == nil || open == a.exportOpen {
		return
	}
	a.exportOpen = open
	var err error
	if open {
		err = a.c.Mount(gunim.Root, "export", "export", a.exportDraft(ids), exportTopic)
	} else {
		err = a.c.Unmount("export")
	}
	if err != nil {
		log.Print(err)
	}
}
