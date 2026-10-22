package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/widget"
)

// Patches: a Go plugin's settings kept by name, for any slot of the same
// plugin, on any track of any album. A plugin comes with patches of its
// own, as starting points; those the user saves are kept beside the
// settings, apart from the presets, which are whole chains. Loading a
// patch is a change to undo.

// PatchName names a patch the user kept, of the plugin Class.
type PatchName struct{ Class, Name string }

type (
	// LoadPatch gives a slot's plugin the settings of its patch Name:
	// one it comes with, where Own says so, or else one the user kept.
	LoadPatch struct {
		Track, Slot int
		Name        string
		Own         bool
	}
	// NamePatch asks for the name to keep a slot's settings as.
	NamePatch struct{ Track, Slot int }
	// PatchClosed says the dialog that names a patch closed.
	PatchClosed struct{}
	// SavePatch keeps a slot's settings as the patch Name of its plugin.
	SavePatch struct {
		Track, Slot int
		Name        string
	}
	// DeletePatch lets the patch Name of the plugin Class go.
	DeletePatch struct{ Class, Name string }
	// PatchNaming is what the dialog that names a patch shows.
	PatchNaming struct {
		Track, Slot int
		Plugin      string
	}
)

// keptPatch is a patch the user kept, of the plugin Class.
type keptPatch struct {
	Class string
	plug.Patch
}

// keptPatches are the patches as kept.
type keptPatches struct {
	Patches []keptPatch
}

// loadPatches takes up the patches kept.
func (a *app) loadPatches() {
	if a.patchesFile == "" {
		return
	}
	b, err := os.ReadFile(a.patchesFile)
	if err != nil {
		return
	}
	var k keptPatches
	if json.Unmarshal(b, &k) == nil {
		a.patches = slices.DeleteFunc(k.Patches, func(p keptPatch) bool { return goneGo(goPath, p.Class) })
		a.patchNames()
	}
}

// savePatches keeps the patches.
func (a *app) savePatches() {
	a.patchNames()
	if a.patchesFile == "" {
		return
	}
	b, err := json.MarshalIndent(keptPatches{Patches: a.patches}, "", "\t")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(a.patchesFile), 0o755)
	}
	if err == nil {
		tmp := a.patchesFile + ".tmp"
		if err = os.WriteFile(tmp, b, 0o644); err == nil {
			err = os.Rename(tmp, a.patchesFile)
		}
	}
	if err != nil {
		log.Printf("mastering: keeping the patches: %v", err)
	}
}

// patchNames tells the window the patches' names, by plugin, in order.
func (a *app) patchNames() {
	slices.SortFunc(a.patches, func(x, y keptPatch) int {
		if c := strings.Compare(x.Class, y.Class); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})
	a.Patches = a.Patches[:0]
	for _, p := range a.patches {
		a.Patches = append(a.Patches, PatchName{Class: p.Class, Name: p.Name})
	}
}

// patch returns the place of the patch name of the plugin class, or -1.
func (a *app) patch(class, name string) int {
	return slices.IndexFunc(a.patches, func(p keptPatch) bool { return p.Class == class && p.Name == name })
}

// handlePatches handles the patches' intents.
func (a *app) handlePatches(in gunim.Intent) bool {
	switch in := in.(type) {
	case NamePatch:
		t, i := a.slot(in.Track, in.Slot)
		if i < 0 || !t.Chain[i].goPlugin() || a.c == nil {
			return true
		}
		a.namingPatch = true
		if err := a.c.Mount(gunim.Root, "patch", "patch",
			PatchNaming{Track: in.Track, Slot: in.Slot, Plugin: t.Chain[i].title()}); err != nil {
			log.Print(err)
		}
	case PatchClosed:
		a.closePatchNaming()
	case SavePatch:
		a.closePatchNaming()
		t, i := a.slot(in.Track, in.Slot)
		name := strings.TrimSpace(in.Name)
		if i < 0 || !t.Chain[i].goPlugin() || name == "" {
			return true
		}
		s, err := readGoSettings(a.states[in.Slot])
		if err != nil {
			a.Note = err.Error()
			return true
		}
		p := keptPatch{Class: t.Chain[i].Class, Patch: plug.Patch{Name: name, Params: maps.Clone(s.Params),
			State: bytes.Clone(s.State)}}
		if k := a.patch(p.Class, name); k >= 0 {
			a.patches[k] = p
		} else {
			a.patches = append(a.patches, p)
		}
		a.savePatches()
	case LoadPatch:
		t, i := a.slot(in.Track, in.Slot)
		if i < 0 || !t.Chain[i].goPlugin() {
			return true
		}
		class := t.Chain[i].Class
		d, ok := plug.Lookup(class)
		if !ok {
			return true
		}
		var p *plug.Patch
		if in.Own {
			if k := slices.IndexFunc(d.Patches, func(p plug.Patch) bool { return p.Name == in.Name }); k >= 0 {
				p = &d.Patches[k]
			}
		} else if k := a.patch(class, in.Name); k >= 0 {
			p = &a.patches[k].Patch
		}
		if p != nil {
			a.applyPatch(t, in.Slot, &d, *p)
		}
	case DeletePatch:
		if k := a.patch(in.Class, in.Name); k >= 0 {
			a.patches = slices.Delete(a.patches, k, k+1)
			a.savePatches()
		}
	default:
		return false
	}
	return true
}

// applyPatch gives slot sid's plugin, of d, on track t, the settings
// of p: every parameter it leaves out at its default.
func (a *app) applyPatch(t *Track, sid int, d *plug.Descriptor, p plug.Patch) {
	s := goSettings{Params: map[uint32]float64{}, State: bytes.Clone(p.State)}
	for _, prm := range d.Params {
		if prm.Flags&plug.ReadOnly != 0 {
			continue
		}
		v, ok := p.Params[prm.ID]
		if !ok {
			v = prm.Default
		}
		s.Params[prm.ID] = prm.Clamp(v)
	}
	if e := a.goEngineOf(t, sid); e != nil {
		if err := e.p.SetState(s.State); err != nil {
			a.Note = fmt.Sprintf("%s: %v", p.Name, err)
			return
		}
		for id, v := range s.Params {
			e.setParam(id, v)
		}
	}
	a.states[sid] = s.pack()
	a.dirty = true
	a.remeasureChain(t.ID)
}

// closePatchNaming takes the dialog that names a patch down.
func (a *app) closePatchNaming() {
	if !a.namingPatch {
		return
	}
	a.namingPatch = false
	if a.c != nil {
		_ = a.c.Unmount("patch")
	}
}

// newPatchDialog makes the dialog that names a patch.
func newPatchDialog(n PatchNaming) *widget.Dialog {
	d := widget.NewDialog("Save " + n.Plugin + " patch")
	name := widget.NewTextField()
	name.Placeholder = "Vocal, airy"
	form := widget.NewForm()
	form.Add("Name", name)
	d.Body = form
	d.SetButtons("Save", "Cancel")
	d.OnDismiss = widget.Sends(PatchClosed{})
	d.Check = func() string {
		if strings.TrimSpace(name.Text()) == "" {
			return "Name the patch."
		}
		return ""
	}
	d.OnAccept = func(*gunim.UI) gunim.Intent { return SavePatch{Track: n.Track, Slot: n.Slot, Name: name.Text()} }
	return d
}
