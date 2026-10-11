package plugui

import (
	"math"
	"slices"
	"testing"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/access"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/input"
	"github.com/marrasen/mastering-studio/plug"
)

// host is a host for tests, which notes the gestures it is sent.
type host struct {
	values map[uint32]float64
	log    []string
}

func (h *host) Param(id uint32) float64 { return h.values[id] }
func (h *host) Begin(uint32, *gunim.UI) { h.log = append(h.log, "begin") }
func (h *host) Set(id uint32, v float64, _ *gunim.UI) {
	h.values[id] = v
	h.log = append(h.log, "set")
}
func (h *host) End(uint32, *gunim.UI)                                            { h.log = append(h.log, "end") }
func (h *host) BeginEdit(string, *gunim.UI)                                      {}
func (h *host) Changed(*gunim.UI)                                                {}
func (h *host) EndEdit(*gunim.UI)                                                {}
func (h *host) Sound() (in, out plug.Sound)                                      { return nil, nil }
func (h *host) Traced(uint32, int64) (float32, bool)                             { return 0, false }
func (h *host) Spectrum([]float64, func([]float64, error, *gunim.UI), *gunim.UI) {}

var release = plug.Param{ID: 3, Name: "Release", Min: 10, Max: 1000, Default: 100, Log: true}

func TestADragTurnsTheKnobAsOneGesture(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	k := NewKnob(h, release)
	k.Handle(input.PointerDown{Pos: geom.Pt(20, 100), Button: input.ButtonPrimary, Clicks: 1}, nil)
	k.Handle(input.PointerMove{Pos: geom.Pt(20, 55)}, nil)
	// Up half its throw: by ratios, the geometric mean, 100.
	k.Handle(input.PointerMove{Pos: geom.Pt(20, 10)}, nil)
	k.Handle(input.PointerUp{Pos: geom.Pt(20, 10), Button: input.ButtonPrimary}, nil)
	if !slices.Equal(h.log, []string{"begin", "set", "set", "end"}) {
		t.Errorf("the drag sent %v, want one gesture", h.log)
	}
	if v := h.values[3]; math.Abs(v-100) > 1e-9 {
		t.Errorf("up half its throw the knob is at %v, want 100", v)
	}
	// Held, it ignores the value set elsewhere; let go, it follows.
	k.held = true
	k.Follow(500, nil)
	k.held = false
	if k.Value() != 100 {
		t.Errorf("held, the knob followed to %v", k.Value())
	}
	k.Follow(500, nil)
	if k.Value() != 500 {
		t.Errorf("the knob follows to %v, want 500", k.Value())
	}
}

func TestKeysWheelAndDoubleClickStepTheKnob(t *testing.T) {
	h := &host{values: map[uint32]float64{3: 10}}
	k := NewKnob(h, release)
	k.Handle(input.KeyPress{Key: input.KeyEnd}, nil)
	if h.values[3] != 1000 {
		t.Errorf("End took the knob to %v, want 1000", h.values[3])
	}
	k.Handle(input.Scroll{Delta: geom.Pt(0, 1)}, nil)
	if v := h.values[3]; v >= 1000 || v < 900 {
		t.Errorf("a step of the wheel down took the knob to %v", v)
	}
	k.Handle(input.PointerDown{Pos: geom.Pt(20, 20), Button: input.ButtonPrimary, Clicks: 2}, nil)
	if h.values[3] != 100 {
		t.Errorf("a double click took the knob to %v, want its default, 100", h.values[3])
	}
	if want := []string{"begin", "set", "end", "begin", "set", "end", "begin", "set", "end"}; !slices.Equal(h.log, want) {
		t.Errorf("the steps sent %v, want a gesture each", h.log)
	}
	if a := k.Access(); a.Role != access.RoleSlider || a.Name != "Release" || a.Range.Value != 100 {
		t.Errorf("the knob reads as %+v", a)
	}
	k.AccessAct(access.Request{SetValue: true, Value: 20}, nil)
	if h.values[3] != 20 {
		t.Errorf("a screen reader set the knob to %v, want 20", h.values[3])
	}
}
