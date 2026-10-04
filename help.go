package main

import (
	"log"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// The help: the keys, and what the pointer does, in a dialog.

type (
	// ShowHelp opens the help.
	ShowHelp struct{}
	// HelpClosed says the help closed.
	HelpClosed struct{}
)

// helpGroups are the keys, in groups, each a key and what it does.
var helpGroups = []struct {
	title string
	keys  [][2]string
}{
	{"Playing", [][2]string{
		{"Space", "Play or pause"},
		{"Home", "Play the track from its start"},
		{"Left, Right", "Back or on 5 seconds"},
		{"1 to 9", "Pick a track, at the same moment"},
		{"Up, Down", "The track before or after"},
		{"A", "Autoplay next: play on into the next track"},
	}},
	{"Listening", [][2]string{
		{"M", "Match levels to the target"},
		{"B", "Bypass the chain and gains: the mix as it came"},
		{"L", "Loop, from a loop at the playhead"},
		{"I, O", "Set the loop's in or out at the playhead"},
	}},
	{"The editor", [][2]string{
		{"Wheel", "Zoom about the pointer, out to the whole track"},
		{"Shift + wheel", "Scroll across"},
		{"Alt + wheel", "Draw the waveform louder"},
		{"Drag the ruler", "Scroll; let go moving to fling it"},
		{"Double-click", "Fit the whole track"},
		{"Shift + drag", "Move a handle finely"},
	}},
	{"Help", [][2]string{
		{"F1, ?", "This help"},
		{"Ctrl + wheel", "Zoom the whole window"},
		{"Ctrl + +, -, 0", "Zoom the window in, out, or back"},
	}},
}

// helpBody draws the keys in columns, each group under its title.
type helpBody struct{ size geom.Size }

const helpRow, helpKeyW = 24, 150

// Layout implements [gunim.Node].
func (h *helpBody) Layout(c gunim.Constraints, _ gunim.Frame, _ gunim.Children) geom.Size {
	rows := float32(0)
	for _, g := range helpGroups {
		rows += float32(len(g.keys)) + 1.5
	}
	h.size = geom.Sz(c.Max.W, rows*helpRow)
	return h.size
}

// Paint implements [gunim.Node].
func (h *helpBody) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, _ gunim.Children) {
	y := float32(0)
	for _, g := range helpGroups {
		shaped(g.title, 10, true).Paint(p, geom.Pt(0, y+6), faded(teal, 0.9))
		y += helpRow
		for _, k := range g.keys {
			run := shaped(k[0], 12, true)
			p.RRect(geom.Rc(0, y+1, run.Advance+16, helpRow-4), 6, paint.Solid(faded(ink, 0.08)))
			run.Paint(p, geom.Pt(8, y+4), ink)
			paintFit(p, k[1], 12, false, geom.Pt(helpKeyW, y+4), box.W-helpKeyW, faded(ink, 0.75))
			y += helpRow
		}
		y += helpRow / 2
	}
}

// newHelp makes the help's dialog.
func newHelp(struct{}) *widget.Dialog {
	d := widget.NewDialog("Keyboard shortcuts")
	d.Width = 560
	d.Body = &helpBody{}
	d.SetButtons("Close", "")
	d.Accept, d.Dismiss = HelpClosed{}, HelpClosed{}
	return d
}

// showHelp opens the help, or closes it.
func (a *app) showHelp(open bool) {
	if a.c == nil || open == a.helping {
		return
	}
	a.helping = open
	var err error
	if open {
		err = a.c.Mount(gunim.Root, "help", "help", struct{}{})
	} else {
		err = a.c.Unmount("help")
	}
	if err != nil {
		log.Print(err)
	}
}
