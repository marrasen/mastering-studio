package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/widget"
)

// Presets: plugin chains kept by name beside the settings, for any track
// of any album: each plugin as set, bypassed or not, and named. A track
// remembers the preset its chain was loaded from or saved as, so Save
// keeps its changes there. Loading a preset is a change to undo; a
// preset deleted, which no album's history holds, can be brought back
// from the toast that tells of it.

// Preset is a plugin chain kept by name.
type Preset struct {
	Name  string
	Chain []keptSlot
}

type (
	// SavePreset keeps a track's chain as the preset Name, or, Name
	// empty, as the preset it was loaded from or saved as.
	SavePreset struct {
		Track int
		Name  string
	}
	// NamePreset asks for the name to keep a track's chain as.
	NamePreset struct{ Track int }
	// PresetClosed says the dialog that names a preset closed.
	PresetClosed struct{}
	// LoadPreset gives a track the chain of the preset Name.
	LoadPreset struct {
		Track int
		Name  string
	}
	// DeletePreset lets the preset Name go.
	DeletePreset struct{ Name string }
	// RestorePreset brings back the preset deleted last.
	RestorePreset struct{}
	// PresetNaming is what the dialog that names a preset shows.
	PresetNaming struct {
		Track int
		Name  string
	}
)

// keptPresets are the presets as kept.
type keptPresets struct {
	Presets []Preset
}

// loadPresets takes up the presets kept.
func (a *app) loadPresets() {
	if a.presetsFile == "" {
		return
	}
	b, err := os.ReadFile(a.presetsFile)
	if err != nil {
		return
	}
	var k keptPresets
	if json.Unmarshal(b, &k) == nil {
		a.presets = k.Presets
		a.presetNames()
	}
}

// savePresets keeps the presets.
func (a *app) savePresets() {
	a.presetNames()
	if a.presetsFile == "" {
		return
	}
	b, err := json.MarshalIndent(keptPresets{Presets: a.presets}, "", "\t")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(a.presetsFile), 0o755)
	}
	if err == nil {
		tmp := a.presetsFile + ".tmp"
		if err = os.WriteFile(tmp, b, 0o644); err == nil {
			err = os.Rename(tmp, a.presetsFile)
		}
	}
	if err != nil {
		log.Printf("mastering: keeping the presets: %v", err)
	}
}

// presetNames tells the window the presets' names, in order.
func (a *app) presetNames() {
	slices.SortFunc(a.presets, func(x, y Preset) int {
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})
	a.Presets = a.Presets[:0]
	for _, p := range a.presets {
		a.Presets = append(a.Presets, p.Name)
	}
}

// preset returns the place of the preset name, or -1.
func (a *app) preset(name string) int {
	return slices.IndexFunc(a.presets, func(p Preset) bool { return p.Name == name })
}

// handlePresets handles the presets' intents.
func (a *app) handlePresets(in gunim.Intent) bool {
	switch in := in.(type) {
	case NamePreset:
		t := a.track(in.Track)
		if t == nil || a.c == nil {
			return true
		}
		a.naming = true
		if err := a.c.Mount(gunim.Root, "preset", "preset", PresetNaming{Track: t.ID, Name: t.Preset}); err != nil {
			log.Print(err)
		}
	case PresetClosed:
		a.closeNaming()
	case SavePreset:
		a.closeNaming()
		t := a.track(in.Track)
		if t == nil {
			return true
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = t.Preset
		}
		if name == "" {
			return true
		}
		chain, states := a.chainOf(t)
		p := Preset{Name: name}
		for _, s := range chain {
			p.Chain = append(p.Chain, keptSlot{Path: s.Path, Class: s.Class, Name: s.Name, Vendor: s.Vendor,
				Label: s.Label, Bypass: s.Bypass, State: bytes.Clone(states[s.ID])})
		}
		if i := a.preset(name); i >= 0 {
			a.presets[i] = p
		} else {
			a.presets = append(a.presets, p)
		}
		a.savePresets()
		t.Preset = name
		a.dirty = true
	case LoadPreset:
		i := a.preset(in.Name)
		if i < 0 || a.track(in.Track) == nil {
			return true
		}
		chain, states := []Slot{}, map[int][]byte{}
		for k, s := range a.presets[i].Chain {
			chain = append(chain, Slot{ID: k + 1, Path: s.Path, Class: s.Class, Name: s.Name, Vendor: s.Vendor,
				Label: s.Label, Bypass: s.Bypass})
			states[k+1] = s.State
		}
		a.giveChain(in.Track, chain, states)
		a.track(in.Track).Preset = in.Name
		a.dirty = true
	case DeletePreset:
		i := a.preset(in.Name)
		if i < 0 {
			return true
		}
		p := a.presets[i]
		a.lastDeleted = &p
		a.presets = slices.Delete(a.presets, i, i+1)
		a.savePresets()
		a.DeletedPreset, a.Deletes = p.Name, a.Deletes+1
	case RestorePreset:
		if p := a.lastDeleted; p != nil && a.preset(p.Name) < 0 {
			a.presets = append(a.presets, *p)
			a.lastDeleted = nil
			a.savePresets()
		}
	default:
		return false
	}
	return true
}

// closeNaming takes the dialog that names a preset down.
func (a *app) closeNaming() {
	if !a.naming {
		return
	}
	a.naming = false
	if a.c != nil {
		_ = a.c.Unmount("preset")
	}
}

// giveChain gives track id chain, its plugins' states in states, each
// slot a new one: its plugins load anew, and while it plays, it plays
// on through them.
func (a *app) giveChain(id int, chain []Slot, states map[int][]byte) {
	t := a.track(id)
	if t == nil {
		return
	}
	at, _, _ := a.d.position()
	a.dropRack(id)
	for _, s := range t.Chain {
		delete(a.states, s.ID)
	}
	t.Chain = nil
	for _, s := range chain {
		a.slots++
		c := s
		// Its gain and range were of other sound.
		c.ID, c.Open, c.Failed, c.Gain, c.Gained, c.LRA, c.Ranged = a.slots, false, "", 0, false, 0, false
		t.Chain = append(t.Chain, c)
		a.states[c.ID] = bytes.Clone(states[s.ID])
	}
	a.remeasureChain(id)
	if a.Current == id && a.Playing {
		a.play(at, 15*time.Millisecond)
	}
}

// newPresetDialog makes the dialog that names a preset.
func newPresetDialog(n PresetNaming) *widget.Dialog {
	d := widget.NewDialog("Save preset")
	name := widget.NewTextField()
	name.SetText(n.Name)
	name.Placeholder = "Mastering, gentle"
	form := widget.NewForm()
	form.Add("Name", name)
	d.Body = form
	d.SetButtons("Save", "Cancel")
	d.Dismiss = PresetClosed{}
	d.Check = func() string {
		if strings.TrimSpace(name.Text()) == "" {
			return "Name the preset."
		}
		return ""
	}
	d.OnAccept = func() gunim.Intent { return SavePreset{Track: n.Track, Name: name.Text()} }
	return d
}
