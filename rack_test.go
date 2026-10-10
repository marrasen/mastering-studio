package main

import (
	"sync/atomic"
	"testing"

	"github.com/marrasen/mastering-studio/plug"
	"github.com/marrasen/mastering-studio/plug/trim"
)

// A plugin of the tests' own that tells of a block processed after it
// was closed.
func init() {
	plug.Register(plug.Descriptor{ID: "test.watcher", Name: "Watcher", Vendor: "Tests",
		New: func(plug.Host) plug.Plugin { return &watcher{} }})
}

// lateBlocks counts the blocks a watcher processed once closed.
var lateBlocks atomic.Int64

type watcher struct {
	closed atomic.Bool
	proc   watcherProc
}

type watcherProc struct{ w *watcher }

func (w *watcher) Processor() plug.Processor { w.proc.w = w; return &w.proc }
func (w *watcher) State() []byte             { return nil }
func (w *watcher) SetState([]byte) error     { return nil }
func (w *watcher) Close()                    { w.closed.Store(true) }

func (watcherProc) Prepare(plug.Setup) {}
func (p watcherProc) Process(*plug.Block) {
	if p.w.closed.Load() {
		lateBlocks.Add(1)
	}
}
func (watcherProc) Latency() int { return 0 }
func (watcherProc) Tail() int    { return 0 }
func (watcherProc) Reset()       {}

// endless is a sound that never ends: a steady level.
type endless struct{ at int64 }

func (e *endless) Len() int64 { return -1 }
func (e *endless) SeekFrame(f int64) error {
	e.at = f
	return nil
}

func (e *endless) Read(dst []float32) (int, error) {
	for i := range dst {
		dst[i] = 0.25
	}
	e.at += int64(len(dst) / 2)
	return len(dst) / 2, nil
}

func TestTheChainChangesAsItPlaysAndLetsNoPluginGoAtWork(t *testing.T) {
	lateBlocks.Store(0)
	r := newRackOf(nil)
	defer r.close()
	st := newStage(&endless{}, r, 0)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		dst := make([]float32, 2*700)
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := st.Read(dst); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}()
	// The application, meanwhile, changing the chain every way.
	slot := 0
	for i := range 300 {
		slot++
		class := "test.watcher"
		if i%3 == 0 {
			class = trim.Descriptor.ID
		}
		lp, err := newPlugin(Slot{ID: slot, Path: goPath, Class: class, Name: class}, nil, 48000, false)
		if err != nil {
			t.Fatal(err)
		}
		r.add(lp)
		r.setBypass(slot, i%4 == 1)
		r.setMatch(i%5 == 0)
		r.setGains(map[int]float32{slot: -3})
		r.setActive(i%7 != 3)
		if i%11 == 0 {
			r.flush()
		}
		if ps := r.list(); len(ps) > 3 {
			r.order([]Slot{{ID: ps[2].slot}, {ID: ps[0].slot}, {ID: ps[1].slot}})
			r.remove(ps[1].slot)
			if e, ok := ps[0].p.(*goEngine); ok && e.d.ID == trim.Descriptor.ID {
				_ = e.SetState(goSettings{Params: map[uint32]float64{trim.GainID: -6}}.pack())
			}
		}
	}
	close(stop)
	<-done
	if n := lateBlocks.Load(); n != 0 {
		t.Fatalf("plugins processed %d blocks after they were let go", n)
	}
}

func TestTheChainMakesNothingAsItPlays(t *testing.T) {
	r := newRackOf(nil)
	defer r.close()
	for i := range 4 {
		lp, err := newPlugin(trimSlot(i+1), nil, 48000, false)
		if err != nil {
			t.Fatal(err)
		}
		r.add(lp)
	}
	st := newStage(&endless{}, r, -1)
	st.tap, st.bypass = newIOTap(tapKeep), &atomic.Bool{}
	// The plugins' taps keep their sound too, as for their views.
	for _, lp := range r.list() {
		lp.listen()
	}
	dst := make([]float32, 2*stageChunk)
	// The first read aligns, as every change after.
	if _, err := st.Read(dst); err != nil {
		t.Fatal(err)
	}
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	// Only what reading makes counts: what the application makes
	// between reads, and other goroutines meanwhile, does not.
	made := allocsUnder(t, "(*chainStage).Read", func() {
		for i := range 200 {
			switch i % 4 {
			case 0:
				r.setBypass(1+i%3, i%8 == 0)
			case 1:
				st.bypass.Store(i%3 == 0)
			case 2:
				r.setMatch(i%6 == 2)
			}
			if _, err := st.Read(dst); err != nil {
				t.Error(err)
				return
			}
		}
	})
	if made != 0 {
		t.Fatalf("the chain made %d things over 200 reads", made)
	}
}
