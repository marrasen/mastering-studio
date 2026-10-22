package main

import (
	"bytes"
	"fmt"
	"log"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/plugui"
	"github.com/marrasen/gunim/audio/vst3"
	"github.com/marrasen/gunim/geom"
)

// The VST3 plugins' windows, the application's half. A VST3 plugin's
// editor opens in a window of the studio's own, owned by the main one:
// a bar the studio draws over the editor, and the browser of presets
// beside it (vstview.go). Where the system cannot hold the editor in a
// window of the studio's, it opens in one of its own, as before. A
// window follows its slot's plugin: it closes where the slot goes, and
// opens again on a plugin loaded anew in its place.

type (
	// PickPreset gives the plugin of slot Slot of track Track the state
	// of Preset, State, as one step to undo.
	PickPreset struct {
		Track, Slot int
		Preset      PresetRef
		State       []byte
	}
	// KeepPreset keeps the state of slot Slot's plugin as the user's
	// preset Name, in a preset file of VST3's, which other hosts read.
	KeepPreset struct {
		Track, Slot int
		Name        string
	}
	// ClosePluginWindow closes slot Slot's window.
	ClosePluginWindow struct{ Slot int }
	// EmbedFailed says slot Slot's window could not hold its editor,
	// for Why: it opens in a window of its own, where it has one.
	EmbedFailed struct {
		Slot int
		Why  string
		None bool
	}
	// BrowsePresets says whether the browser of presets shows beside a
	// plugin's views, for the next ones to open so.
	BrowsePresets struct{ On bool }
)

// PresetRef names a preset: where it is filed, and its name.
type PresetRef struct{ Bank, Group, Name string }

// refOf is p's name.
func refOf(p plug.Preset) PresetRef { return PresetRef{Bank: p.Bank, Group: p.Group, Name: p.Name} }

// PluginWin is what a plugin's window shows.
type PluginWin struct {
	Track, Slot int
	Title       string
	// Current is the preset the plugin plays, where Playing says it
	// plays one: the one picked last, while its state is as picked.
	Current PresetRef
	Playing bool
	// Kept counts the presets kept, so the browser reads them afresh.
	Kept int
	// Browse says the browser shows, as the window opens.
	Browse bool
}

// embedder is a plugin whose editor can show in a window of the
// studio's, as a VST3 plugin's can: [plug.Embedder]'s methods that open,
// place, size and close it.
type embedder interface {
	plugui.Embeddable
	EditorSize() (w, h int, ok bool)
}

// pluginTopic is what a plugin's window watches.
const pluginTopic = "plugin"

// plugWin is a plugin's window open: on lp, the plugin of slot of
// track, as shown last.
type plugWin struct {
	w     *gunim.Window
	c     gunim.Client
	track int
	lp    *livePlugin
	shown PluginWin
}

// picked is the preset a slot's plugin took last, and its state as the
// plugin then held it.
type picked struct {
	ref   PresetRef
	state []byte
}

// pluginWinSize is the size a plugin's window opens at where its
// editor cannot say its own, and browseW how much wider the browser
// makes it. The window then fits the editor as it opens.
var pluginWinSize = geom.Sz(640, 420)

const browseW = 340

// openPluginWindow opens the window of slot i of track t, lp, or brings
// it to the front where it is open, and reports whether it did: not
// where the plugin's editor cannot show in a window of the studio's.
func (a *app) openPluginWindow(t *Track, i int, lp *livePlugin) bool {
	s := t.Chain[i]
	if w := a.plugWins[s.ID]; w != nil && w.lp == lp {
		w.c.ToFront()
		return true
	}
	ed, ok := lp.p.(embedder)
	if !ok || a.newWindow == nil || a.noEmbed[s.ID] == lp {
		return false
	}
	size := pluginWinSize
	if w, h, ok := ed.EditorSize(); ok {
		size = geom.Sz(float32(w), float32(h)+pluginBarH)
	}
	if a.Browse {
		size.W += browseW
	}
	state := a.pluginWin(t, s)
	w, err := a.newWindow(gunim.WindowOptions{Title: state.Title, Size: size, Owned: true, Icons: icons(),
		AskToClose: ClosePluginWindow{Slot: s.ID}, Theme: a.themed(themeNamed(a.theme))})
	if err != nil {
		log.Printf("mastering: a window for %s: %v", s.title(), err)
		return false
	}
	registerPluginView(w, ed, lp.class)
	for _, st := range studioThemes {
		w.RegisterTheme(a.themed(st.Name))
	}
	c := w.Client()
	_ = c.SetTheme(themeNamed(a.theme))
	if err := c.Mount(gunim.Root, "plugin", "plugin", state, pluginTopic); err != nil {
		c.Close()
		log.Printf("mastering: a window for %s: %v", s.title(), err)
		return false
	}
	_ = c.Focus("plugin")
	if a.plugWins == nil {
		a.plugWins = map[int]*plugWin{}
	}
	a.plugWins[s.ID] = &plugWin{w: w, c: c, track: t.ID, lp: lp, shown: state}
	go func() {
		for env := range c.Intents() {
			select {
			case a.plugIn <- env.Intent:
			case <-a.ctx.Done():
				return
			}
		}
	}()
	return true
}

// pluginWin is what the window of slot s of track t shows.
func (a *app) pluginWin(t *Track, s Slot) PluginWin {
	w := PluginWin{Track: t.ID, Slot: s.ID, Title: fmt.Sprintf("%s · %s", s.title(), t.Title), Kept: a.presetsKept,
		Browse: a.Browse}
	if p, ok := a.picked[s.ID]; ok && bytes.Equal(p.state, a.states[s.ID]) {
		w.Current, w.Playing = p.ref, true
	}
	return w
}

// closePluginWindow closes slot id's window, where one is open.
func (a *app) closePluginWindow(id int) {
	if w := a.plugWins[id]; w != nil {
		delete(a.plugWins, id)
		w.c.Leave()
	}
}

// closePluginWindows closes every plugin's window, as the studio ends.
func (a *app) closePluginWindows() {
	for id := range a.plugWins {
		a.closePluginWindow(id)
	}
}

// syncPluginWindows has each plugin's window follow its slot: it closes
// where the slot is gone or its plugin unloaded, opens again on a plugin
// loaded anew in its place, and shows the slot as it is now.
func (a *app) syncPluginWindows() {
	for id, w := range a.plugWins {
		t, i := a.slot(w.track, id)
		var lp *livePlugin
		if i >= 0 {
			if r := a.racks[t.ID]; r != nil {
				lp = r.find(id)
			}
		}
		if lp == nil {
			a.closePluginWindow(id)
			continue
		}
		if lp != w.lp {
			a.closePluginWindow(id)
			a.openPluginWindow(t, i, lp)
			continue
		}
		if s := a.pluginWin(t, t.Chain[i]); s != w.shown {
			w.shown = s
			_ = w.c.Publish(pluginTopic, s)
		}
	}
}

// handlePluginWindow takes an intent of a plugin's window.
func (a *app) handlePluginWindow(in gunim.Intent) {
	switch in := in.(type) {
	case ClosePluginWindow:
		a.closePluginWindow(in.Slot)
	case EmbedFailed:
		w := a.plugWins[in.Slot]
		if w == nil {
			return
		}
		a.closePluginWindow(in.Slot)
		t, i := a.slot(w.track, in.Slot)
		if i < 0 {
			return
		}
		if in.None {
			a.Note = t.Chain[i].title() + " has no editor of its own"
			return
		}
		log.Printf("mastering: %s's editor in a window of the studio's: %s", t.Chain[i].title(), in.Why)
		// The plugin's editor in a window of its own, from now on.
		if a.noEmbed == nil {
			a.noEmbed = map[int]*livePlugin{}
		}
		a.noEmbed[in.Slot] = w.lp
		a.handle(ShowEditor{Track: w.track, Slot: in.Slot})
	case PickPreset:
		a.pickPreset(in)
	case KeepPreset:
		a.keepPreset(in)
	default:
		a.handle(in)
	}
}

// pickPreset gives the slot's plugin the preset's state, as one step to
// undo.
func (a *app) pickPreset(in PickPreset) {
	t, i := a.slot(in.Track, in.Slot)
	if i < 0 {
		return
	}
	r := a.racks[t.ID]
	if r == nil {
		return
	}
	lp := r.find(in.Slot)
	if lp == nil {
		return
	}
	s := t.Chain[i]
	was := a.snapshot()
	if err := lp.p.SetState(in.State); err != nil {
		a.Note = fmt.Sprintf("%s: %v", s.title(), err)
		return
	}
	// The edits the state made are this step's, not the editor's.
	lp.edits = lp.p.Edits()
	a.readRack(t.ID)
	a.picks(s.ID, in.Preset)
	a.dirty = true
	if !a.isRef(t.ID) {
		a.recordSnap(was, fmt.Sprintf("the preset %s on %s", in.Preset.Name, t.Title), t.ID, "")
	}
	a.remeasureChain(t.ID)
}

// keepPreset keeps the state of the slot's plugin as the user's preset,
// over one of the same name.
func (a *app) keepPreset(in KeepPreset) {
	name := strings.TrimSpace(in.Name)
	t, i := a.slot(in.Track, in.Slot)
	if i < 0 || name == "" {
		return
	}
	r := a.racks[t.ID]
	if r == nil {
		return
	}
	lp := r.find(in.Slot)
	if lp == nil || lp.class == nil {
		return
	}
	r.flush()
	st, err := lp.p.State()
	if err == nil {
		err = vst3.WritePreset(vst3.PresetPath(*lp.class, name), *lp.class, st)
	}
	if err != nil {
		a.Note = fmt.Sprintf("keeping %s: %v", name, err)
		return
	}
	a.presetsKept++
	// It plays the preset kept, as its state is read in.
	if a.picked == nil {
		a.picked = map[int]picked{}
	}
	a.picked[in.Slot] = picked{ref: PresetRef{Bank: vst3.UserBank, Group: lp.class.Name, Name: name}, state: st}
}
