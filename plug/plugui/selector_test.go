package plugui

import (
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/mastering-studio/plug"
)

func TestASelectorPicksByLampWheelAndKeys(t *testing.T) {
	s := NewSelector([]string{"A", "B", "C"}, []string{"one", "two", "three"})
	var picked []int
	s.OnPick = func(i int, _ *gunim.UI) { picked = append(picked, i) }
	s.Layout(gunim.Constraints{Max: geom.Sz(72, 100)}, gunim.Frame{}, gunim.Children{})
	s.Handle(input.PointerDown{Pos: geom.Pt(60, 8), Button: input.ButtonPrimary, Clicks: 1}, nil)
	// The wheel down steps back.
	s.Handle(input.Scroll{Delta: geom.Pt(0, 1)}, nil)
	if s.Selected() != 1 {
		t.Fatalf("the wheel down took the selector from 2 to %d, want 1", s.Selected())
	}
	s.Handle(input.KeyPress{Key: input.KeyLeft}, nil)
	// Past the first, nothing.
	s.Handle(input.KeyPress{Key: input.KeyLeft}, nil)
	if want := []int{2, 1, 0}; len(picked) != 3 || picked[0] != 2 || picked[1] != 1 || picked[2] != 0 {
		t.Fatalf("the selector picked %v, want %v", picked, want)
	}
	if a := s.Access(); a.Role != access.RoleTabList || a.Value != "A" || a.Parts[2].Description != "three" {
		t.Errorf("the selector reads as %+v", a)
	}
	s.AccessAct(access.Request{Part: 1, Action: access.ActionPress}, nil)
	if s.Selected() != 1 {
		t.Errorf("pressed by a screen reader, the selector picked %d, want 1", s.Selected())
	}
	s.Locked = true
	s.Handle(input.KeyPress{Key: input.KeyRight}, nil)
	if s.Selected() != 1 {
		t.Error("locked, the selector picked")
	}
}

func TestAKnobBoundToItsOwnValueTurnsItAsAGesture(t *testing.T) {
	var log []string
	v := 0.0
	k := NewKnobFunc(plug.Param{Name: "Mix", Max: 1, Default: 0.5}, 0,
		func(*gunim.UI) { log = append(log, "begin") },
		func(x float64, _ *gunim.UI) { v = x; log = append(log, "set") },
		func(*gunim.UI) { log = append(log, "end") })
	k.Handle(input.PointerDown{Pos: geom.Pt(20, 100), Button: input.ButtonPrimary, Clicks: 1}, nil)
	k.Handle(input.PointerMove{Pos: geom.Pt(20, 10)}, nil)
	k.Handle(input.PointerUp{Pos: geom.Pt(20, 10), Button: input.ButtonPrimary}, nil)
	if v != 0.5 || len(log) != 3 || log[0] != "begin" || log[2] != "end" {
		t.Fatalf("dragged half its throw, the value is %v, by %v", v, log)
	}
	// Locked, it turns not.
	k.Locked = true
	k.Handle(input.PointerDown{Pos: geom.Pt(20, 100), Button: input.ButtonPrimary, Clicks: 2}, nil)
	k.Handle(input.KeyPress{Key: input.KeyEnd}, nil)
	if v != 0.5 || len(log) != 3 {
		t.Errorf("locked, the knob turned to %v", v)
	}
	// Another value to turn, in its place.
	k.Locked = false
	k.SetParam(plug.Param{Name: "Slope", Min: 12, Max: 48, Steps: 3}, 24, nil)
	k.Handle(input.KeyPress{Key: input.KeyUp}, nil)
	if v != 36 || k.String() != "Slope 36" {
		t.Errorf("turning a slope a step up from 24 set %v (%s), want 36", v, k)
	}
}
