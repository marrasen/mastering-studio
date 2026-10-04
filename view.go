package main

import (
	"fmt"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/widget"
)

// registerViews is the window half.
func registerViews(w *gunim.Window, d *deck) {
	gunim.RegisterView(w, "album",
		func(Album) *root { return newRoot(d) },
		func(r *root, s Album, u *gunim.UI) { r.show(s, u) })
}

// root is the whole window: the header along the top; the tracks down
// the left; the meters down the right; and between them the editor of
// the track picked, its fades and cut, the transport, and the album
// laid out end to end.
type root struct {
	anim.Group
	d      *deck
	state  Album
	header *header
	list   *trackList
	scroll *widget.Scroll
	drop   *widget.DropTarget
	editor *editor
	// edDrop takes a file dropped on the editor, as the track's new
	// file.
	edDrop *widget.DropTarget
	tools  *editTools
	trans  *transport
	strip  *strip
	meters *meters
	chain  *chainRow
	head   *trackHead
	// chainMenu is the chain's menus, around it.
	chainMenu *widget.ContextMenu
	size      geom.Size
}

func newRoot(d *deck) *root {
	r := &root{d: d}
	r.header = newHeader(r)
	r.list = newTrackList(r)
	r.list.menu = widget.NewContextMenu(r.list)
	r.scroll = widget.NewScroll(r.list.menu)
	r.drop = widget.NewDropTarget(r.scroll)
	r.drop.Accept = func(_ any, paths []string) bool { return len(paths) > 0 }
	r.drop.OnDrop = func(d input.Drop) gunim.Intent { return AddFiles{Paths: d.Paths} }
	r.editor = newEditor(r)
	r.edDrop = widget.NewDropTarget(r.editor)
	r.edDrop.Accept = func(_ any, paths []string) bool { return len(paths) == 1 && r.state.Current != 0 }
	r.edDrop.Hint = func(input.DragOver) any {
		t, _ := r.track()
		return widget.DropHint{Text: "Replace the file of " + t.Title, Effect: widget.DropCopy}
	}
	r.edDrop.OnDrop = func(d input.Drop) gunim.Intent {
		return ReplaceFile{ID: r.state.Current, Path: d.Paths[0]}
	}
	r.tools = newEditTools(r)
	r.trans = newTransport(r)
	r.strip = newStrip(r)
	r.meters = newMeters(r)
	r.chain = newChainRow(r)
	r.head = newTrackHead(r)
	r.chainMenu = widget.NewContextMenu(r.chain)
	r.chain.menu = r.chainMenu
	return r
}

// track returns the track picked, and false for none.
func (r *root) track() (Track, bool) {
	for _, t := range r.state.Tracks {
		if t.ID == r.state.Current {
			return t, true
		}
	}
	return Track{}, false
}

func (r *root) show(s Album, u *gunim.UI) {
	was := r.state
	r.state = s
	r.header.show(s)
	r.list.show(s, u)
	t, _ := r.track()
	r.editor.show(was, t)
	r.tools.show(t)
	r.chain.show(t, s)
	r.head.show(t, s, u)
	r.trans.show(s)
	r.strip.show(s)
	r.meters.show(was, s)
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (r *root) Children() []gunim.Node {
	return []gunim.Node{r.header, r.drop, r.edDrop, r.tools, r.trans, r.strip, r.meters, r.chainMenu, r.head}
}

// Focusable implements [gunim.Focusable]: the window's keys come here.
func (r *root) Focusable() bool { return true }

// The parts' sizes.
const (
	headerH = 64
	listW   = 340
	metersW = 330
	toolsH  = 64
	chainH  = 60
	headH   = 44
	transH  = 76
	stripH  = 92
	gutter  = 12
)

// Layout implements [gunim.Node].
func (r *root) Layout(c gunim.Constraints, f gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	r.size = size
	area := geom.Rect{Max: size.Point()}.Inset(f.Safe)
	place := func(i int, rc geom.Rect) {
		kids.At(i).Layout(gunim.Tight(rc.Size()))
		kids.At(i).Place(rc.Min)
	}
	place(0, geom.Rect{Min: area.Min, Max: geom.Pt(area.Max.X, area.Min.Y+headerH)})
	top := area.Min.Y + headerH
	place(1, geom.Rect{Min: geom.Pt(area.Min.X, top), Max: geom.Pt(area.Min.X+listW, area.Max.Y)})
	place(6, geom.Rect{Min: geom.Pt(area.Max.X-metersW, top), Max: area.Max})
	x0, x1 := area.Min.X+listW+gutter, area.Max.X-metersW-gutter
	bottom := area.Max.Y - gutter
	place(5, geom.Rc(x0, bottom-stripH, x1-x0, stripH))
	place(4, geom.Rc(x0, bottom-stripH-gutter-transH, x1-x0, transH))
	place(7, geom.Rc(x0, bottom-stripH-gutter-transH-gutter-chainH, x1-x0, chainH))
	place(8, geom.Rc(x0, top+gutter, x1-x0, headH))
	edTop := top + gutter + headH
	edBottom := bottom - stripH - gutter - transH - gutter - chainH - toolsH
	place(2, geom.Rc(x0, edTop, x1-x0, max(edBottom-edTop, 80)))
	place(3, geom.Rc(x0, edBottom, x1-x0, toolsH))
	return size
}

// Paint implements [gunim.Node].
func (r *root) Paint(p *paint.Painter, _ gunim.Frame, box geom.Size, kids gunim.Children) {
	p.RRect(geom.Rect{Max: box.Point()}, 0, paint.Solid(night))
	// A faint light from the top, in what plays' colour.
	p.RRect(geom.Rc(0, 0, box.W, headerH), 0, paint.Solid(panel))
	for k := range kids.All {
		k.Paint(p)
	}
}

// Handle implements [gunim.Handler]: the window's keys.
func (r *root) Handle(e input.Event, u *gunim.UI) bool {
	k, ok := e.(input.KeyPress)
	if !ok {
		return false
	}
	at, _, _ := r.d.position()
	switch {
	case k.Key == input.KeySpace:
		u.Send(r, TogglePlay{})
	case k.Key >= input.Key1 && k.Key <= input.Key9:
		// A track by its number: the same moment of it, to compare.
		if i := int(k.Key - input.Key1); i < len(r.state.Tracks) {
			u.Send(r, Pick{ID: r.state.Tracks[i].ID})
		}
	case k.Key == input.KeyUp || k.Key == input.KeyDown:
		r.step(map[bool]int{false: 1, true: -1}[k.Key == input.KeyUp], u)
	case k.Key == input.KeyLeft:
		u.Send(r, SeekTo{At: max(0, at-5*time.Second)})
	case k.Key == input.KeyRight:
		u.Send(r, SeekTo{At: at + 5*time.Second})
	case k.Key == input.KeyHome:
		u.Send(r, PlayFromStart{})
	case k.Key == input.KeyM:
		u.Send(r, SetMatch{On: !r.state.Match})
	case k.Key == input.KeyA:
		u.Send(r, SetAlbumPlay{On: !r.state.AlbumPlay})
	default:
		return false
	}
	return true
}

// step picks the track by places before or after the one picked.
func (r *root) step(by int, u *gunim.UI) {
	for i, t := range r.state.Tracks {
		if t.ID == r.state.Current {
			if j := i + by; j >= 0 && j < len(r.state.Tracks) {
				u.Send(r, Pick{ID: r.state.Tracks[j].ID})
			}
			return
		}
	}
}

// header is the window's top: the album's name and its sum, and its
// settings: the silence before each track, the target, and how tracks
// are exported, where, and the buttons to add tracks and export.
type header struct {
	r      *root
	gap    *valueChip
	target *valueChip
	format *pill
	dir    *pill
	add    *pill
	export *pill
	// calc measures the tracks changed since they were measured.
	calc *pill
	sum  string
	// album is the album's loudness and range, measured together, and
	// albumOff how far its loudness is from the target.
	album     string
	albumOff  float32
	albumLoud bool
	// albumStale says a track changed since it was measured, so the
	// album's reading is old.
	albumStale bool
}

func newHeader(r *root) *header {
	h := &header{r: r}
	h.gap = newValueChip("SILENCE BEFORE", func(v float64) string { return fmt.Sprintf("%.2f s", v) },
		0.01, 0.1, 0, 10, 1, func(v float64, u *gunim.UI) { u.Send(r, SetGap{Gap: time.Duration(v * float64(time.Second))}) })
	h.target = newValueChip("TARGET", func(v float64) string { return fmt.Sprintf("%.1f LUFS", v) },
		0.05, 0.5, -30, -5, -14, func(v float64, u *gunim.UI) { u.Send(r, SetTarget{LUFS: float32(v)}) })
	h.format = newPill("16-bit · dither", func(u *gunim.UI) {
		s := r.state
		// 16 dithered, 16 plain, 24 dithered, 24 plain, 32 float.
		switch {
		case s.Bits == 16 && s.Dither:
			u.Send(r, SetExport{Bits: 16})
		case s.Bits == 16:
			u.Send(r, SetExport{Bits: 24, Dither: true})
		case s.Bits == 24 && s.Dither:
			u.Send(r, SetExport{Bits: 24})
		case s.Bits == 24:
			u.Send(r, SetExport{Bits: 32})
		default:
			u.Send(r, SetExport{Bits: 16, Dither: true})
		}
	})
	h.dir = newPill("Export to…", func(u *gunim.UI) { u.Send(r, ChooseExportDir{}) })
	h.add = newPill("Add tracks", func(u *gunim.UI) { u.Send(r, ChooseFiles{}) })
	h.export = newPill("Export album", func(u *gunim.UI) { u.Send(r, Export{}) })
	h.export.primary = true
	h.calc = newPill("Calc LUFS", func(u *gunim.UI) { u.Send(r, CalcLoudness{}) })
	return h
}

func (h *header) show(s Album) {
	h.gap.value = s.Gap.Seconds()
	h.target.value = float64(s.Target)
	words := fmt.Sprintf("%d-bit", s.Bits)
	switch {
	case s.Bits == 32:
		words = "32-bit float"
	case s.Dither:
		words += " · dither"
	}
	h.format.words = words
	if s.ExportDir != "" {
		h.dir.words = "→ " + lastDirs(s.ExportDir)
	} else {
		h.dir.words = "Export to…"
	}
	h.export.words = "Export album"
	if s.Exporting {
		h.export.words = "Exporting…"
	}
	var total time.Duration
	for _, t := range s.Tracks {
		total += t.Measure.Length
	}
	h.sum = fmt.Sprintf("%d tracks · %s", len(s.Tracks), short(total))
	h.album = "measuring…"
	if l := s.Loudness; l.Loud {
		h.album = fmt.Sprintf("%.1f LUFS", l.LUFS)
		if l.Ranged {
			h.album += fmt.Sprintf(" · LRA %.1f", l.LRA)
		}
	}
	h.albumOff = s.Loudness.LUFS - s.Target
	h.albumLoud = s.Loudness.Loud
	stale, measuring := 0, 0
	for _, t := range s.Tracks {
		if t.Stale {
			stale++
		}
		if t.Measuring {
			measuring++
		}
	}
	h.albumStale = stale > 0
	switch {
	case measuring > 0:
		h.calc.words = fmt.Sprintf("Measuring %d…", measuring)
	case stale > 0:
		h.calc.words = fmt.Sprintf("Calc LUFS · %d", stale)
	default:
		h.calc.words = "LUFS up to date"
	}
	h.calc.setLit(stale > 0 && measuring == 0)
}

// lastDirs is the last two parts of a folder's path.
func lastDirs(path string) string {
	n, cut := 0, 0
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			n++
			if n == 2 {
				cut = i + 1
				break
			}
		}
	}
	return path[cut:]
}

// Children implements [gunim.Composite].
func (h *header) Children() []gunim.Node {
	return []gunim.Node{h.gap, h.target, h.format, h.dir, h.add, h.export, h.calc}
}

// Layout implements [gunim.Node]: the settings right of the name, the
// buttons at the right.
func (h *header) Layout(c gunim.Constraints, _ gunim.Frame, kids gunim.Children) geom.Size {
	size := c.Max
	x := float32(260)
	y := (size.H - 44) / 2
	for i, w := range []float32{130, 130} {
		kids.At(i).Layout(gunim.Tight(geom.Sz(w, 44)))
		kids.At(i).Place(geom.Pt(x, y))
		x += w + 10
	}
	// The button that measures, after the album's reading.
	cw := pillWidth(h.calc.words)
	kids.At(6).Layout(gunim.Tight(geom.Sz(cw, 32)))
	kids.At(6).Place(geom.Pt(540+max(shaped(h.album, 14, true).Advance, 110)+16, (size.H-32)/2))
	right := size.W - 16
	for _, i := range []int{5, 4, 3, 2} {
		var words string
		switch i {
		case 5:
			words = h.export.words
		case 4:
			words = h.add.words
		case 3:
			words = h.dir.words
		case 2:
			words = h.format.words
		}
		w := max(pillWidth(words), 96)
		right -= w
		kids.At(i).Layout(gunim.Tight(geom.Sz(w, 36)))
		kids.At(i).Place(geom.Pt(right, (size.H-36)/2))
		right -= 10
	}
	return size
}

// Paint implements [gunim.Node].
func (h *header) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	widget.PaintIcon(p, f.Theme, icon.Disc3, geom.Rc(18, (box.H-26)/2, 26, 26), teal)
	shaped("Mastering", 18, true).Paint(p, geom.Pt(54, 12), ink)
	shaped(h.sum, 12, false).Paint(p, geom.Pt(54, 36), faded(ink, 0.5))
	if len(h.r.state.Tracks) > 0 {
		// The album's loudness, by the target's chip, as a chip reads.
		x, y := float32(540), (box.H-44)/2
		shaped("ALBUM", 9, true).Paint(p, geom.Pt(x, y+6), faded(teal, 0.85))
		c := faded(ink, 0.5)
		if h.albumLoud {
			c = loudnessColor(h.albumOff)
		}
		if h.albumStale {
			c = faded(c, 0.45)
		}
		shaped(h.album, 14, true).Paint(p, geom.Pt(x, y+19), c)
	}
	for k := range kids.All {
		k.Paint(p)
	}
}
