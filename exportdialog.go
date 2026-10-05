package main

import (
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

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
		// Report writes a report of the export beside its files.
		Report bool
		// Release names the album, heading its notes as they are copied.
		Release Release
	}
	// ExportTrack is a track as the dialog lists it, ticked to export.
	ExportTrack struct {
		ID, Number int
		Title      string
		Ticked     bool
		// Length is how long the track plays, and LUFS its loudness, of
		// the Target, where it is Measured, Stale where it changed since.
		Length          time.Duration
		LUFS, Target    float32
		Measured, Stale bool
		// Note is the track's note, and Marks its notes at times.
		Note  string
		Marks []Mark
	}

	// OpenExport opens the export's dialog, the tracks named ticked, or
	// every one.
	OpenExport struct{ IDs []int }
	// ExportClosed says the export's dialog closed.
	ExportClosed struct{}
	// LocateLAME asks, with the system's dialog, where LAME is.
	LocateLAME struct{}
	// CancelExport stops the export running, its files half written
	// taken away.
	CancelExport struct{}
	// StartExport exports the tracks ticked, as the dialog says.
	StartExport struct {
		IDs      []int
		Bits     int
		Dither   bool
		WAV, MP3 bool
		MP3Rate  int
		Report   bool
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

// exportDialog is the export's dialog: where the files go, as what,
// and which tracks.
type exportDialog struct {
	*widget.Dialog
	body *exportBody
}

func newExportDialog(d ExportDraft) *exportDialog {
	dlg := widget.NewDialog("Export")
	dlg.Width = 680
	b := newExportBody(d)
	e := &exportDialog{Dialog: dlg, body: b}
	dlg.Body = b
	dlg.SetButtons("Export", "Cancel")
	dlg.Dismiss = ExportClosed{}
	dlg.Check = func() string {
		switch {
		case len(b.list.ticked()) == 0:
			return "Tick a track to export."
		case !b.wav.On && !b.mp3.On:
			return "Choose WAV, MP3 or both."
		}
		return ""
	}
	dlg.OnAccept = func() gunim.Intent {
		s := StartExport{IDs: b.list.ticked(), Bits: bitsChoices[b.bits.Selected()].bits, Dither: b.dither.On,
			WAV: b.wav.On, MP3: b.mp3.On, Report: b.report.On}
		if len(d.MP3Rates) > 0 {
			s.MP3Rate = d.MP3Rates[max(0, min(b.rate.Selected, len(d.MP3Rates)-1))]
		}
		return s
	}
	return e
}

// show takes the draft anew, as the folder changes, or LAME is found.
func (e *exportDialog) show(d ExportDraft, u *gunim.UI) {
	e.body.dir = d.Dir
	e.body.lame(d.LAME)
	u.Invalidate()
}

// notesText is the tracks' notes, to send to the artist: under the
// release's name, each track with any, by its number and title, its
// note, then its notes at times, by the time in the mix.
func notesText(d ExportDraft) string {
	var b strings.Builder
	head := d.Release.Title
	if d.Release.Artist != "" {
		head = strings.TrimPrefix(head+" · "+d.Release.Artist, " · ")
	}
	if head != "" {
		b.WriteString(head + "\n")
	}
	for _, t := range d.Tracks {
		note := strings.TrimSpace(t.Note)
		if note == "" && len(t.Marks) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "%02d %s\n", t.Number, t.Title)
		if note != "" {
			b.WriteString(note + "\n")
		}
		for _, m := range t.Marks {
			fmt.Fprintf(&b, "  %s  %s\n", short(m.At), m.Text)
		}
	}
	return b.String()
}

// The app's half of the dialog.

// mp3Rates are the bitrates offered for MP3.
var mp3Rates = []int{320, 256, 192}

// exportDraft is the export's dialog's state, the tracks ids ticked, or
// every one.
func (a *app) exportDraft(ids []int) ExportDraft {
	d := ExportDraft{Dir: a.exportDir(), Bits: a.Bits, Dither: a.Dither, WAV: a.ExportWAV, MP3: a.ExportMP3,
		MP3Rate: a.MP3Rate, HaveMP3: encodeMP3 != nil, MP3Rates: mp3Rates, LAME: lamePath, Report: a.ExportReport,
		Release: a.Release}
	for i, t := range a.Tracks {
		length := t.Measure.Length
		if length == 0 && t.Format.SampleRate > 0 {
			length = duration(t.Frames, t.Format.SampleRate)
		}
		d.Tracks = append(d.Tracks, ExportTrack{ID: t.ID, Number: i + 1, Title: t.Title,
			Ticked: len(ids) == 0 || slices.Contains(ids, t.ID), Length: length, LUFS: t.Measure.LUFS,
			Target: a.Target, Measured: t.Measured && t.Measure.Loud, Stale: t.Stale, Note: t.Note, Marks: t.Marks})
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
