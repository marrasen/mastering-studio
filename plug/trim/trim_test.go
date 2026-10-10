package trim

import (
	"math"
	"slices"
	"testing"

	"github.com/marrasen/gunim"
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
func (h *host) End(uint32, *gunim.UI)              { h.log = append(h.log, "end") }
func (h *host) BeginEdit(name string, _ *gunim.UI) { h.log = append(h.log, "edit "+name) }
func (h *host) Changed(*gunim.UI)                  { h.log = append(h.log, "changed") }
func (h *host) EndEdit(*gunim.UI)                  { h.log = append(h.log, "end edit") }

func newPlugin(t *testing.T) (plug.Plugin, *host) {
	t.Helper()
	d, ok := plug.Lookup("marrasen.trim")
	if !ok {
		t.Fatal("the trim did not register itself")
	}
	h := &host{values: map[uint32]float64{GainID: 0}}
	return d.New(h), h
}

// ones returns n frames of a channel at full scale.
func ones(n int) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = 1
	}
	return s
}

func TestTheFirstGainJumpsAndLaterOnesGlide(t *testing.T) {
	p, _ := newPlugin(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	ch := [][]float32{ones(512), ones(512)}
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 512, Changes: []plug.Change{{ID: GainID, Value: -6}}})
	want := float32(math.Pow(10, -6.0/20))
	if math.Abs(float64(ch[0][0]-want)) > 1e-6 || math.Abs(float64(ch[1][511]-want)) > 1e-6 {
		t.Fatalf("at -6 dB from the start the block runs %v to %v, want %v throughout", ch[0][0], ch[1][511], want)
	}

	// A move up at frame 100 glides: no step at once, there by the end.
	ch = [][]float32{ones(4800), ones(4800)}
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 4800, Changes: []plug.Change{{Frame: 100, ID: GainID, Value: 0}}})
	if ch[0][99] != want {
		t.Errorf("before the change the gain is %v, want %v", ch[0][99], want)
	}
	if ch[0][100] >= 0.6 {
		t.Errorf("the gain stepped to %v at the change", ch[0][100])
	}
	if math.Abs(float64(ch[0][4799]-1)) > 1e-4 {
		t.Errorf("100 ms later the gain is %v, want 1", ch[0][4799])
	}
}

func TestAGainOutOfRangeIsClamped(t *testing.T) {
	p, _ := newPlugin(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 44100, MaxBlock: 64, Channels: 1})
	in, out := [][]float32{ones(64)}, [][]float32{make([]float32, 64)}
	pr.Process(&plug.Block{In: in, Out: out, Frames: 64, Changes: []plug.Change{{ID: GainID, Value: 100}}})
	if want := float32(math.Pow(10, 24.0/20)); math.Abs(float64(out[0][63]-want)) > 1e-4 {
		t.Errorf("at +100 dB the gain is %v, want +24 dB's %v", out[0][63], want)
	}
	if in[0][0] != 1 {
		t.Error("processing apart from In wrote to In")
	}
}

func TestTheTrimKeepsNoState(t *testing.T) {
	p, _ := newPlugin(t)
	if p.State() != nil || p.SetState(nil) != nil {
		t.Error("a trim has state")
	}
	if p.SetState([]byte("x")) == nil {
		t.Error("a trim took state it cannot have")
	}
}

func TestTheViewsWorkThroughTheHost(t *testing.T) {
	p, h := newPlugin(t)
	h.values[GainID] = -3
	v, ok := p.(plug.Viewer)
	if !ok {
		t.Fatal("the trim has no views")
	}
	if v.View(plug.Full, h) != nil {
		t.Error("the trim has a full view of its own")
	}
	c, ok := v.View(plug.Chip, h).(*chip)
	if !ok || c.Text != "-3.0 dB" {
		t.Fatalf("the chip shows %q, want -3.0 dB", c.Text)
	}
	c.Follow(GainID, 1.5, nil)
	if c.Text != "+1.5 dB" {
		t.Errorf("the chip follows to %q, want +1.5 dB", c.Text)
	}

	pn, ok := v.View(plug.Panel, h).(*panel)
	if !ok || pn.Slider.Value() != -3 {
		t.Fatal("the panel's slider does not start at the host's gain")
	}
	// A drag of two steps and a let-go is one gesture.
	var u *gunim.UI
	pn.Slider.OnChange(-2, u)
	pn.Slider.OnChange(-1, u)
	pn.Slider.OnCommit(-1, u)
	// A key's step commits alone, as a gesture of its own.
	pn.Slider.OnCommit(-0.9, u)
	want := []string{"begin", "set", "set", "set", "end", "begin", "set", "end"}
	if !slices.Equal(h.log, want) {
		t.Errorf("the host heard %v, want %v", h.log, want)
	}
	if math.Abs(h.values[GainID]+0.9) > 1e-6 {
		t.Errorf("the host's gain is %v, want -0.9", h.values[GainID])
	}
	pn.Follow(GainID, 6, nil)
	if pn.Slider.Value() != 6 {
		t.Errorf("the slider follows to %v, want 6", pn.Slider.Value())
	}
}
