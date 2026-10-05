package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/anim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/icon"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/gunim/paint"
	"github.com/marrasen/gunim/theme"
	"github.com/marrasen/gunim/widget"
)

// registerViews is the window half.
func registerViews(w *gunim.Window, d *deck) {
	gunim.RegisterView(w, "album",
		func(Album) *root { return newRoot(d) },
		func(r *root, s Album, u *gunim.UI) { r.show(s, u) })
	// The window's own widgets, as its dialogs', in the studio's teal.
	w.RegisterTheme(theme.Make(themeName, theme.Set(widget.Accent, teal),
		theme.Set(widget.ButtonPrimaryFill, rgb(0x1f, 0x8f, 0x80)),
		theme.Set(widget.ButtonPrimaryHover, rgb(0x2a, 0xa3, 0x92))))
	gunim.RegisterView(w, "release", newReleaseDialog, nil)
	gunim.RegisterView(w, "help", newHelp, nil)
	gunim.RegisterView(w, "unsaved", newUnsavedDialog, nil)
	gunim.RegisterView(w, "preset", newPresetDialog, nil)
	gunim.RegisterView(w, "export", newExportDialog,
		func(e *exportDialog, d ExportDraft, u *gunim.UI) { e.show(d, u) })
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
	// headerMenu is the album's menu, around the header.
	headerMenu *widget.ContextMenu
	list       *trackList
	scroll     *widget.Scroll
	drop       *widget.DropTarget
	// ab is the sessions of listening, over the tracks; refs the
	// references, under them, under refHead.
	ab      *abBar
	refHead *refHead
	refs    *trackList
	refDrop *widget.DropTarget
	// toasts tells what an undo or redo did.
	toasts *widget.Toasts
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
	r.headerMenu = widget.NewContextMenu(r.header)
	r.header.menu = r.headerMenu
	r.list = newTrackList(r, false)
	r.list.menu = widget.NewContextMenu(r.list)
	r.scroll = widget.NewScroll(r.list.menu)
	r.drop = widget.NewDropTarget(r.scroll)
	r.drop.Accept = func(_ any, paths []string) bool { return len(paths) > 0 }
	r.drop.OnDrop = func(d input.Drop) gunim.Intent { return AddFiles{Paths: d.Paths} }
	r.ab = newABBar(r)
	r.refHead = newRefHead(r)
	r.refs = newTrackList(r, true)
	r.refs.menu = widget.NewContextMenu(r.refs)
	r.refDrop = widget.NewDropTarget(widget.NewScroll(r.refs.menu))
	r.refDrop.Accept = func(_ any, paths []string) bool { return len(paths) > 0 }
	r.refDrop.Hint = func(input.DragOver) any {
		return widget.DropHint{Text: "Add as reference tracks", Effect: widget.DropCopy}
	}
	r.refDrop.OnDrop = func(d input.Drop) gunim.Intent { return AddReferences{Paths: d.Paths} }
	r.toasts = &widget.Toasts{Life: 3 * time.Second}
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

// track returns the track picked, of the album or the references, and
// false for none.
func (r *root) track() (Track, bool) {
	if t := r.state.find(r.state.Current); t != nil {
		return *t, true
	}
	return Track{}, false
}

func (r *root) show(s Album, u *gunim.UI) {
	was := r.state
	r.state = s
	r.header.show(s)
	r.list.show(s, u)
	r.refs.show(s, u)
	r.ab.show(s)
	t, _ := r.track()
	r.editor.show(was, t)
	r.tools.show(t)
	r.chain.show(t, s)
	r.head.show(t, s, u)
	r.trans.show(s)
	r.strip.show(s)
	r.meters.show(was, s)
	if s.Undos != was.Undos && s.Undone != "" {
		// What was undone, and a way to take it back.
		to := widget.Toast{Title: s.Undone, Key: "undo", Icon: icon.Undo2, Action: "Redo", On: Redo{}}
		if strings.HasPrefix(s.Undone, "Redid") {
			to.Icon, to.Action, to.On = icon.Redo2, "Undo", Undo{}
		}
		r.toasts.Show(to, u)
	}
	// A preset deleted, and a way to bring it back.
	if s.Deletes != was.Deletes && s.DeletedPreset != "" {
		r.toasts.Show(widget.Toast{Title: "Deleted the preset " + s.DeletedPreset, Key: "preset", Icon: icon.Trash2,
			Action: "Undo", On: RestorePreset{}}, u)
	}
	// What went wrong, as saving.
	if s.Note != was.Note && s.Note != "" {
		r.toasts.Show(widget.Toast{Title: s.Note, Kind: widget.ToastError, Key: "note"}, u)
	}
	u.Invalidate()
}

// Children implements [gunim.Composite].
func (r *root) Children() []gunim.Node {
	return []gunim.Node{r.headerMenu, r.drop, r.edDrop, r.tools, r.trans, r.strip, r.meters, r.chainMenu, r.head,
		r.ab, r.refHead, r.refDrop, r.toasts}
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
	// Down the left: the sessions, the album's tracks, and the
	// references at the foot.
	refsTop := area.Max.Y - refsListH(len(r.state.References)) - refHeadH
	place(9, geom.Rc(area.Min.X, top, listW, abH))
	place(1, geom.Rect{Min: geom.Pt(area.Min.X, top+abH), Max: geom.Pt(area.Min.X+listW, refsTop)})
	place(10, geom.Rc(area.Min.X, refsTop, listW, refHeadH))
	place(11, geom.Rect{Min: geom.Pt(area.Min.X, refsTop+refHeadH), Max: geom.Pt(area.Min.X+listW, area.Max.Y)})
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
	// The toasts, in the editor's lower corner, over the transport.
	ts := kids.At(12).Layout(gunim.Constraints{Max: geom.Sz(360, size.H)})
	kids.At(12).Place(geom.Pt(x1-ts.W-gutter, bottom-stripH-gutter-transH-ts.H-gutter))
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
	// Ctrl, or Command, with S saves, and with Z undoes.
	if k.Mods.Has(input.ModControl) || k.Mods.Has(input.ModSuper) {
		shift := k.Mods.Has(input.ModShift)
		switch {
		case k.Key == input.KeyS && shift:
			u.Send(r, SaveAlbumAs{})
		case k.Key == input.KeyS:
			u.Send(r, SaveAlbum{})
		case k.Key == input.KeyZ && shift, k.Key == input.KeyY:
			u.Send(r, Redo{})
		case k.Key == input.KeyZ:
			u.Send(r, Undo{})
		default:
			return false
		}
		return true
	}
	at, _, _ := r.d.position()
	// Alt with Left or Right goes to the loop's in, or its out, looping
	// or not; from the out the sound plays on past it.
	if l := r.editor.track.Loop; l != nil && k.Mods.Has(input.ModAlt) &&
		(k.Key == input.KeyLeft || k.Key == input.KeyRight) {
		to := l.In
		if k.Key == input.KeyRight {
			to = l.Out
		}
		u.Send(r, SeekTo{At: r.editor.renderTime(to.Seconds())})
		return true
	}
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
	case k.Key == input.KeyI:
		r.editor.setLoopEnd(false, u)
	case k.Key == input.KeyO:
		r.editor.setLoopEnd(true, u)
	case k.Key == input.KeyL:
		r.editor.toggleLoop(u)
	case k.Key == input.KeyF1 || (k.Key == input.KeySlash && k.Mods.Has(input.ModShift)):
		u.Send(r, ShowHelp{})
	case k.Key == input.KeyB:
		u.Send(r, SetBypassAll{On: !r.state.Bypass})
	case k.Key == input.KeyS:
		u.Send(r, SwitchSide{})
	default:
		return false
	}
	return true
}

// step picks the track by places before or after the one picked, of
// the album or the references, as it is.
func (r *root) step(by int, u *gunim.UI) {
	list := r.state.listOf(r.state.Current)
	if i := indexOf(list, r.state.Current); i >= 0 {
		if j := i + by; j >= 0 && j < len(list) {
			u.Send(r, Pick{ID: list[j].ID})
		}
	}
}

// header is the window's top: the album's name and its sum, and its
// settings: the silence before each track, the target, and how tracks
// are exported, where, and the buttons to add tracks and export.
type header struct {
	anim.Group
	r      *root
	menu   *widget.ContextMenu
	hover  *anim.Float
	name   string
	gap    *valueChip
	target *valueChip
	add    *pill
	export *pill
	// calc measures the tracks changed since they were measured, help
	// shows the keys, and save saves the album.
	calc *pill
	help *iconButton
	save *pill
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
	h := &header{r: r, hover: anim.NewFloat(0)}
	h.Add(h.hover)
	h.gap = newValueChip("SILENCE BEFORE", func(v float64) string { return fmt.Sprintf("%.2f s", v) },
		0.01, 0.1, 0, 10, 1, func(v float64, u *gunim.UI) { u.Send(r, SetGap{Gap: time.Duration(v * float64(time.Second))}) })
	h.target = newValueChip("TARGET", func(v float64) string { return fmt.Sprintf("%.1f LUFS", v) },
		0.05, 0.5, -30, -5, -14, func(v float64, u *gunim.UI) { u.Send(r, SetTarget{LUFS: float32(v)}) })
	h.add = newPill("Add tracks", func(u *gunim.UI) { u.Send(r, ChooseFiles{}) })
	h.export = newPill("Export…", func(u *gunim.UI) {
		if r.state.Exporting {
			u.Send(r, CancelExport{})
			return
		}
		u.Send(r, OpenExport{})
	})
	h.export.primary = true
	h.calc = newPill("Measure loudness", func(u *gunim.UI) { u.Send(r, MeasureLoudness{}) })
	h.help = newIconButton(icon.CircleHelp, func(u *gunim.UI) { u.Send(r, ShowHelp{}) })
	h.save = newPill("Save", func(u *gunim.UI) { u.Send(r, SaveAlbum{}) })
	return h
}

func (h *header) show(s Album) {
	h.name = s.Title
	if h.name == "" {
		h.name = s.AlbumName
	}
	if h.name == "" {
		h.name = "Untitled project"
	}
	// Changes not saved: an asterisk, and the button to save them lit.
	if s.Unsaved {
		h.name += "*"
	}
	h.save.words = "Save"
	if s.Untitled {
		h.save.words = "Save as…"
	}
	h.save.setLit(s.Unsaved)
	h.gap.value = s.Gap.Seconds()
	h.target.value = float64(s.Target)
	h.export.words = "Export…"
	h.export.primary = !s.Exporting
	if s.Exporting {
		h.export.words = "Cancel export"
	}
	var total time.Duration
	for _, t := range s.Tracks {
		total += t.Measure.Length
	}
	h.sum = fmt.Sprintf("%d tracks · %s", len(s.Tracks), short(total))
	if s.Artist != "" {
		h.sum = s.Artist + " · " + h.sum
	}
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
	for _, t := range slices.Concat(s.Tracks, s.References) {
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
		h.calc.words = fmt.Sprintf("Measure loudness · %d", stale)
	default:
		h.calc.words = "Loudness up to date"
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
	return []gunim.Node{h.gap, h.target, h.add, h.export, h.calc, h.help, h.save}
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
	kids.At(4).Layout(gunim.Tight(geom.Sz(cw, 32)))
	kids.At(4).Place(geom.Pt(540+max(shaped(h.album, 14, true).Advance, 110)+16, (size.H-32)/2))
	right := size.W - 16
	for _, i := range []int{3, 2} {
		words := h.add.words
		if i == 3 {
			words = h.export.words
		}
		w := max(pillWidth(words), 96)
		right -= w
		kids.At(i).Layout(gunim.Tight(geom.Sz(w, 36)))
		kids.At(i).Place(geom.Pt(right, (size.H-36)/2))
		right -= 10
	}
	sw := max(pillWidth(h.save.words), 72)
	right -= sw
	kids.At(6).Layout(gunim.Tight(geom.Sz(sw, 36)))
	kids.At(6).Place(geom.Pt(right, (size.H-36)/2))
	right -= 10
	kids.At(5).Layout(gunim.Tight(geom.Sz(36, 36)))
	kids.At(5).Place(geom.Pt(right-36, (size.H-36)/2))
	return size
}

// Paint implements [gunim.Node].
func (h *header) Paint(p *paint.Painter, f gunim.Frame, box geom.Size, kids gunim.Children) {
	// The release's name, the menu of projects under it.
	if hv := h.hover.Value(); hv > 0.01 {
		p.RRect(h.titleRect(), 10, paint.Solid(faded(ink, 0.06*hv)))
	}
	p.Image(logo(), geom.Rc(15, (box.H-32)/2, 32, 32), paint.ImageOpts{Opacity: 1})
	paintFit(p, h.name, 18, true, geom.Pt(54, 12), titleW-36, ink)
	name := min(shaped(h.name, 18, true).Advance, titleW-36)
	widget.PaintIcon(p, f.Theme, icon.ChevronDown, geom.Rc(54+name+4, 16, 14, 14), faded(ink, 0.4+0.4*h.hover.Value()))
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

// titleW is how wide the album's name and its menu may be.
const titleW = 196

// titleRect is where the album's name is, which opens its menu.
func (h *header) titleRect() geom.Rect { return geom.Rc(8, 6, titleW+40, headerH-12) }

// Handle implements [gunim.Handler]: a press on the album's name opens
// the menu of projects.
func (h *header) Handle(e input.Event, u *gunim.UI) bool {
	switch e := e.(type) {
	case input.PointerMove:
		on := h.titleRect().Contains(e.Pos)
		h.hover.Animate(map[bool]float32{false: 0, true: 1}[on], anim.Snappy)
	case input.PointerLeave:
		h.hover.Animate(0, anim.Gentle)
	case input.PointerDown:
		if e.Button != input.ButtonPrimary || !h.titleRect().Contains(e.Pos) {
			return false
		}
		h.openMenu(u)
	default:
		return false
	}
	u.Invalidate()
	return true
}

// Focusable implements [gunim.Focusable].
func (h *header) Focusable() bool { return false }

// openMenu opens the menu of projects: the release's details, a new
// one, one opened, this one saved elsewhere, and those opened last.
func (h *header) openMenu(u *gunim.UI) {
	m := h.menu
	recent := []string{}
	for _, p := range h.r.state.RecentAlbums {
		if p != h.r.state.AlbumFile {
			recent = append(recent, p)
		}
	}
	s := h.r.state
	undo, redo := "Undo", "Redo"
	if s.UndoLabel != "" {
		undo += " " + s.UndoLabel
	}
	if s.RedoLabel != "" {
		redo += " " + s.RedoLabel
	}
	// Each item, and what it sends.
	type item struct {
		words, hint string
		ic          *icon.Icon
		off         bool
		send        gunim.Intent
	}
	items := []item{
		{undo, "Ctrl+Z", icon.Undo2, s.UndoLabel == "", Undo{}},
		{redo, "Ctrl+Shift+Z", icon.Redo2, s.RedoLabel == "", Redo{}},
		{"Save project", "Ctrl+S", icon.Save, false, SaveAlbum{}},
		{"Save project as…", "Ctrl+Shift+S", icon.Save, false, SaveAlbumAs{}},
		{"New project…", "", icon.FilePlus, false, NewAlbum{}},
		{"Open project…", "", icon.FolderOpen, false, OpenAlbum{}},
		{"Edit release details…", "", icon.Disc3, false, EditRelease{}},
		{"Keyboard shortcuts", "", icon.Keyboard, false, ShowHelp{}},
	}
	m.Breaks, m.Captions, m.Checked = []int{2, 4, 6}, nil, nil
	if len(recent) > 0 {
		m.Breaks = append(m.Breaks, len(items))
		m.Captions = []int{len(items)}
		items = append(items, item{words: "Recent"})
		for _, p := range recent {
			items = append(items, item{albumName(p), lastDirs(filepath.Dir(p)), icon.Disc3, false, OpenAlbumPath{Path: p}})
		}
	}
	m.Items, m.Icons, m.Hints, m.Disabled = nil, nil, nil, nil
	for _, it := range items {
		m.Items = append(m.Items, it.words)
		m.Icons = append(m.Icons, it.ic)
		m.Hints = append(m.Hints, it.hint)
		m.Disabled = append(m.Disabled, it.off)
	}
	m.Picked = func(i int, u *gunim.UI) {
		if i >= 0 && i < len(items) && items[i].send != nil {
			u.Send(h, items[i].send)
		}
	}
	m.Open(geom.Pt(16, headerH-6), u)
}

// themeName is the window's theme: dark, with the studio's teal.
const themeName = "mastering"
