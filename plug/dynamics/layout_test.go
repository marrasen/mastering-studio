package dynamics

import (
	"math"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/gunimtest"
	"github.com/marrasen/mastering-studio/plug"
)

// boundsOf lays the panel out at size and returns where each of nodes
// is.
func boundsOf(t *testing.T, pn *panel, size geom.Size, nodes ...gunim.Node) []geom.Rect {
	t.Helper()
	w := gunimtest.New(t, size, nil)
	gunim.RegisterView(w, "dyn", func(struct{}) gunim.Node { return pn }, nil)
	if err := w.Client().Mount(gunim.Root, "dyn", "dyn", nil); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	out := make([]geom.Rect, len(nodes))
	gunim.RegisterPatch(w, "dyn", func(_ gunim.Node, _ struct{}, u *gunim.UI) {
		for i, n := range nodes {
			out[i], _ = u.Bounds(n)
		}
	})
	if err := w.Client().Patch("dyn", struct{}{}); err != nil {
		t.Fatal(err)
	}
	w.Frame(time.Second / 60)
	return out
}

func TestTheSoundFillsThePanelUnderItsControls(t *testing.T) {
	p, h := newDynamics(t)
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	size := geom.Sz(1600, 860)
	nodes := make([]gunim.Node, 0, 3+len(pn.sections))
	nodes = append(nodes, pn.hist, pn.gr, pn.out)
	for _, s := range pn.sections {
		nodes = append(nodes, s)
	}
	b := boundsOf(t, pn, size, nodes...)
	if b[0] != (geom.Rect{Max: size.Point()}) {
		t.Fatalf("the history is %v, want the whole panel", b[0])
	}
	// The sections in one row along the foot, centred.
	first, last := b[3], b[len(b)-1]
	if first.Min.Y != last.Min.Y || last.Max.Y != size.H-panelInset ||
		math.Abs(float64((first.Min.X+last.Max.X)/2-size.W/2)) > 1 {
		t.Errorf("the sections run from %v to %v: not along the foot, centred", first, last)
	}
	// The meters side by side, centred above them.
	gr, out := b[1], b[2]
	if gr.Min.Y != out.Min.Y || gr.Max.X >= out.Min.X || out.Max.Y > first.Min.Y-sectionGap ||
		math.Abs(float64((gr.Min.X+out.Max.X)/2-size.W/2)) > 1 {
		t.Errorf("the meters are at %v and %v, over sections from %v: not side by side, centred above", gr, out, first)
	}
}
