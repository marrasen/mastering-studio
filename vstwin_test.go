package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/vst3"
)

// fakeVST is a VST3 plugin as the chain runs it, with an editor a
// window of the studio's can hold: its state is a string.
type fakeVST struct {
	state  []byte
	edits  uint64
	opened int
	closed bool
	// embedErr is why its editor will not show in a window of the
	// studio's; embedded and placed say where it shows there.
	embedErr error
	embedded bool
	placed   image.Rectangle
}

func (f *fakeVST) Process([]float32)                        {}
func (f *fakeVST) Latency() int                             { return 0 }
func (f *fakeVST) Reset()                                   {}
func (f *fakeVST) SetPosition(int64)                        {}
func (f *fakeVST) Set(uint32, float64)                      {}
func (f *fakeVST) State() ([]byte, error)                   { return f.state, nil }
func (f *fakeVST) SetState(b []byte) error                  { f.state = b; return nil }
func (f *fakeVST) Flush()                                   {}
func (f *fakeVST) Edits() uint64                            { return f.edits }
func (f *fakeVST) EditorOpen() bool                         { return f.opened > 0 }
func (f *fakeVST) OpenEditor(string) error                  { f.opened++; return nil }
func (f *fakeVST) Close()                                   { f.closed = true }
func (f *fakeVST) PlaceEditor(r image.Rectangle) (w, h int) { f.placed = r; return 400, 300 }
func (f *fakeVST) CloseEditor()                             { f.embedded = false }
func (f *fakeVST) EditorSize() (w, h int, ok bool)          { return 400, 300, true }
func (f *fakeVST) EmbedEditor(uintptr, func(w, h int)) (w, h int, err error) {
	if f.embedErr != nil {
		return 0, 0, f.embedErr
	}
	f.embedded = true
	return 400, 300, nil
}

// vstState is a VST3 plugin's state, as [vst3.Plugin.State] gives it,
// of a component's state of s and no controller's.
func vstState(s string) []byte {
	b := binary.LittleEndian.AppendUint32([]byte("gVS3"), uint32(len(s)))
	return binary.LittleEndian.AppendUint32(append(b, s...), 0)
}

// fakeClass is fakeVST's class.
var fakeClass = vst3.Class{ID: [16]byte{1, 2, 3, 4}, Name: "Fake", Vendor: "Tests"}

// vstApp returns savedApp's album with a fakeVST on its first track's
// chain, loaded, and the windows the studio opens, offscreen.
func vstApp(t *testing.T) (*app, *Track, Slot, *fakeVST, *[]gunim.WindowOptions) {
	t.Helper()
	// The user's presets go to a folder of the test's own.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	a := savedApp(t)
	t.Cleanup(a.closeRacks)
	t.Cleanup(a.closePluginWindows)
	tr := &a.Tracks[0]
	a.slots++
	s := Slot{ID: a.slots, Path: "/plugins/fake.vst3", Class: fakeClass.IDString(), Name: "Fake", Vendor: "Tests"}
	tr.Chain = append(tr.Chain, s)
	a.states[s.ID] = vstState("zero")
	f := &fakeVST{state: vstState("zero")}
	if r := a.racks[tr.ID]; r != nil {
		r.close()
	}
	class := fakeClass
	a.racks[tr.ID] = newRackOf([]*livePlugin{{slot: s.ID, p: f, gain: 1, class: &class}})
	var opened []gunim.WindowOptions
	a.newWindow = func(o gunim.WindowOptions) (*gunim.Window, error) {
		opened = append(opened, o)
		// At once: an offscreen window never shows its first frame, and
		// a window still coming in places no editor.
		o.Instant = true
		return gunim.NewOffscreenWith(o), nil
	}
	return a, tr, s, f, &opened
}

// pluginIntent waits for the next intent of a plugin's window, stepping
// w's frames meanwhile.
func pluginIntent(t *testing.T, a *app, w *gunim.Window) gunim.Intent {
	t.Helper()
	// A slot tries to open its editor for two seconds before it gives up.
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); {
		select {
		case in := <-a.plugIn:
			return in
		default:
		}
		w.Frame(time.Second / 60)
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the plugin's window sent nothing")
	return nil
}

func TestAVST3EditorOpensInAWindowOfTheStudios(t *testing.T) {
	a, tr, s, f, opened := vstApp(t)
	a.handle(ShowEditor{Track: tr.ID, Slot: s.ID})
	if len(*opened) != 1 || f.opened != 0 {
		t.Fatalf("shown, the editor opened %d windows of the studio's and %d of its own", len(*opened), f.opened)
	}
	o := (*opened)[0]
	if !o.Owned || o.Title != "Fake · "+tr.Title || o.AskToClose != (ClosePluginWindow{Slot: s.ID}) {
		t.Fatalf("the window opens as %+v", o)
	}
	// The editor, the bar, and the edges the window is sized by.
	if o.Size.W != 400+2*gunim.ResizeEdge || o.Size.H != 300+pluginBarH+gunim.ResizeEdge {
		t.Fatalf("the window opens at %v, for an editor of 400×300", o.Size)
	}
	// The editor shows under the bar.
	w := a.plugWins[s.ID].w
	for range 10 {
		w.Frame(time.Second / 60)
	}
	e := int(gunim.ResizeEdge)
	if want := image.Rect(e, pluginBarH, 400+e, 300+pluginBarH); !f.embedded || f.placed != want {
		t.Fatalf("the editor shows %v at %v, want at %v", f.embedded, f.placed, want)
	}
	// Shown again, it comes to the front.
	a.handle(ShowEditor{Track: tr.ID, Slot: s.ID})
	if len(*opened) != 1 || len(a.plugWins) != 1 {
		t.Fatalf("shown again, %d windows opened", len(*opened))
	}
	a.handlePluginWindow(ClosePluginWindow{Slot: s.ID})
	if len(a.plugWins) != 0 {
		t.Fatal("asked to close, the window stays")
	}
}

func TestAnEditorNoWindowOfTheStudiosCanHoldOpensInItsOwn(t *testing.T) {
	a, tr, s, f, opened := vstApp(t)
	f.embedErr = errors.New("no room")
	a.handle(ShowEditor{Track: tr.ID, Slot: s.ID})
	w := a.plugWins[s.ID].w
	in := pluginIntent(t, a, w)
	if e, ok := in.(EmbedFailed); !ok || e.Slot != s.ID || e.None {
		t.Fatalf("the window sent %#v", in)
	}
	a.handlePluginWindow(in)
	if len(a.plugWins) != 0 || f.opened != 1 {
		t.Fatalf("failing to hold the editor, %d windows stay, and the editor opened %d times on its own",
			len(a.plugWins), f.opened)
	}
	// From then on it opens in its own at once.
	a.handle(ShowEditor{Track: tr.ID, Slot: s.ID})
	if len(*opened) != 1 || f.opened != 2 {
		t.Fatalf("shown again, %d windows opened, and the editor %d times on its own", len(*opened), f.opened)
	}
}

func TestAnEditorWithNoneToShowSaysSo(t *testing.T) {
	a, tr, s, f, _ := vstApp(t)
	f.embedErr = fmt.Errorf("fake: %w", plug.ErrNoEditor)
	a.handle(ShowEditor{Track: tr.ID, Slot: s.ID})
	in := pluginIntent(t, a, a.plugWins[s.ID].w)
	if e, ok := in.(EmbedFailed); !ok || !e.None {
		t.Fatalf("the window sent %#v", in)
	}
	a.handlePluginWindow(in)
	if len(a.plugWins) != 0 || f.opened != 0 || a.Note != "Fake has no editor of its own" {
		t.Fatalf("with no editor, %d windows stay, the editor opened %d times, and the note says %q",
			len(a.plugWins), f.opened, a.Note)
	}
}

func TestAPluginsWindowFollowsItsSlot(t *testing.T) {
	a, tr, s, _, opened := vstApp(t)
	a.handle(ShowEditor{Track: tr.ID, Slot: s.ID})
	first := a.plugWins[s.ID]
	// Loaded anew, as at another rate, the window opens again on the new
	// plugin.
	again := &livePlugin{slot: s.ID, p: &fakeVST{state: vstState("zero")}, gain: 1, class: first.lp.class}
	a.racks[tr.ID].close()
	a.racks[tr.ID] = newRackOf([]*livePlugin{again})
	a.syncPluginWindows()
	if w := a.plugWins[s.ID]; w == nil || w == first || w.lp != again || len(*opened) != 2 {
		t.Fatalf("on a plugin loaded anew, the window is %+v, of %d opened", w, len(*opened))
	}
	// Renamed, the window is titled anew.
	a.handle(RenamePlugin{Track: tr.ID, Slot: s.ID, Label: "Wide"})
	a.syncPluginWindows()
	if got := a.plugWins[s.ID].shown.Title; got != "Wide · "+tr.Title {
		t.Fatalf("renamed, the window is titled %q", got)
	}
	// Removed, it closes.
	a.handle(RemovePlugin{Track: tr.ID, Slot: s.ID})
	a.syncPluginWindows()
	if len(a.plugWins) != 0 {
		t.Fatal("the slot removed, its window stays")
	}
}

func TestAPresetPickedIsAStepToUndo(t *testing.T) {
	a, tr, s, f, _ := vstApp(t)
	a.handle(ShowEditor{Track: tr.ID, Slot: s.ID})
	steps := len(a.undo)
	warm := PresetRef{Bank: vst3.FactoryBank, Group: "Fake", Name: "Warm"}
	a.handlePluginWindow(PickPreset{Track: tr.ID, Slot: s.ID, Preset: warm, State: vstState("warm")})
	if !bytes.Equal(f.state, vstState("warm")) || !bytes.Equal(a.states[s.ID], vstState("warm")) {
		t.Fatalf("picked, the plugin plays %q, and the album keeps %q", f.state, a.states[s.ID])
	}
	if len(a.undo) != steps+1 || a.UndoLabel != "the preset Warm on "+tr.Title {
		t.Fatalf("picking made %d steps, the last %q", len(a.undo)-steps, a.UndoLabel)
	}
	a.syncPluginWindows()
	if w := a.plugWins[s.ID].shown; !w.Playing || w.Current != warm {
		t.Fatalf("picked, the window shows %+v", w)
	}
	a.handle(Undo{})
	a.syncPluginWindows()
	if !bytes.Equal(f.state, vstState("zero")) || a.plugWins[s.ID].shown.Playing {
		t.Fatalf("undone, the plugin plays %q, and the window shows %+v", f.state, a.plugWins[s.ID].shown)
	}
}

func TestAPresetKeptIsAFileOfVST3s(t *testing.T) {
	a, tr, s, f, _ := vstApp(t)
	a.handle(ShowEditor{Track: tr.ID, Slot: s.ID})
	f.state = vstState("bright")
	a.handlePluginWindow(KeepPreset{Track: tr.ID, Slot: s.ID, Name: " Bright "})
	path := vst3.PresetPath(fakeClass, "Bright")
	class, st, err := vst3.ReadPreset(path)
	if err != nil || class != fakeClass.FUID() || !bytes.Equal(st, vstState("bright")) {
		t.Fatalf("kept, %s holds class %s and state %q (%v)", path, class, st, err)
	}
	if a.presetsKept != 1 {
		t.Fatalf("kept, the count of presets kept is %d", a.presetsKept)
	}
	// The window lists it.
	h := newPluginHost(f, a.racks[tr.ID].find(s.ID).class, a.pluginWin(tr, s))
	ps := h.src.Presets()
	if len(ps) != 1 || ps[0].Bank != vst3.UserBank || ps[0].Name != "Bright" {
		t.Fatalf("the window lists %+v", ps)
	}
	if b, err := ps[0].State(); err != nil || !bytes.Equal(b, vstState("bright")) {
		t.Fatalf("the preset listed plays %q (%v)", b, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
}

func TestPickingInTheBrowserSendsThePresetsState(t *testing.T) {
	a, tr, s, f, _ := vstApp(t)
	if err := vst3.WritePreset(vst3.PresetPath(fakeClass, "Soft"), fakeClass, vstState("soft")); err != nil {
		t.Fatal(err)
	}
	a.handle(ShowEditor{Track: tr.ID, Slot: s.ID})
	w := a.plugWins[s.ID].w
	var ps []plug.Preset
	gunim.RegisterPatch(w, "plugin", func(h *pluginHost, _ struct{}, u *gunim.UI) {
		ps = h.src.Presets()
		h.src.Pick(ps[0], u)
	})
	if err := w.Client().Patch("plugin", struct{}{}); err != nil {
		t.Fatal(err)
	}
	for in := pluginIntent(t, a, w); ; in = pluginIntent(t, a, w) {
		if _, failed := in.(EmbedFailed); failed {
			continue
		}
		p, ok := in.(PickPreset)
		want := PresetRef{Bank: vst3.UserBank, Group: "Fake", Name: "Soft"}
		if !ok || p.Track != tr.ID || p.Slot != s.ID || p.Preset != want || !bytes.Equal(p.State, vstState("soft")) {
			t.Fatalf("picked, the window sent %#v", in)
		}
		break
	}
	_ = f
}

func TestTheBrowserShowingIsKeptForTheNextWindows(t *testing.T) {
	a, tr, s, _, opened := vstApp(t)
	a.settingsFile = filepath.Join(t.TempDir(), "settings.json")
	a.handlePluginWindow(BrowsePresets{On: true})
	if !readSettings(a.settingsFile).Browse {
		t.Fatal("the browser showing is not kept")
	}
	a.handle(ShowEditor{Track: tr.ID, Slot: s.ID})
	if o := (*opened)[0]; o.Size.W != 400+browseW+gunim.ResizeEdge || !a.plugWins[s.ID].shown.Browse {
		t.Fatalf("browsing, the window opens %v wide, showing %+v", o.Size, a.plugWins[s.ID].shown)
	}
}
