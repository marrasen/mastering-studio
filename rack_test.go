package main

import (
	"math"
	"sync/atomic"
	"testing"

	"github.com/marrasen/gunim/audio/plug"
	"github.com/marrasen/gunim/audio/plug/maximizer"
	"github.com/marrasen/gunim/audio/plug/trim"
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

// sineSource is a quiet sine, endless, read from where it is sought.
type sineSource struct{ at int64 }

const sineHz, sineA = 440, 0.05

func sineAt(i int64) float32 { return float32(sineA * math.Sin(2*math.Pi*sineHz*float64(i)/48000)) }

func (s *sineSource) Len() int64 { return -1 }
func (s *sineSource) SeekFrame(f int64) error {
	s.at = f
	return nil
}

func (s *sineSource) Read(dst []float32) (int, error) {
	n := len(dst) / 2
	for i := range n {
		dst[2*i], dst[2*i+1] = sineAt(s.at+int64(i)), sineAt(s.at+int64(i))
	}
	s.at += int64(n)
	return n, nil
}

func TestBypassFadesAPluginWithoutItsOwnAndKeepsItsLatency(t *testing.T) {
	up, err := newPlugin(trimSlot(1), goSettings{Params: map[uint32]float64{trim.GainID: 6}}.pack(), 48000, false)
	if err != nil {
		t.Fatal(err)
	}
	late, err := newPlugin(Slot{ID: 2, Path: goPath, Class: maximizer.Descriptor.ID, Name: "Maximizer"}, nil, 48000, false)
	if err != nil {
		t.Fatal(err)
	}
	r := newRackOf([]*livePlugin{up, late})
	defer r.close()
	lat := r.latency()
	if lat == 0 {
		t.Fatal("the maximizer is not late, so the test shows nothing")
	}
	// The quiet sine, which the maximizer leaves as it is, through a
	// stage, as the track plays; and how far it moves at most from a
	// frame to the next, made louder by the trim.
	st := newStage(&sineSource{}, r, 0)
	most := float32(2 * sineA * 2 * math.Pi * sineHz / 48000)
	out := make([]float32, 0, 60*512)
	aligned := 0
	dst := make([]float32, 2*512)
	for b := range 60 {
		switch b {
		case 20:
			r.setBypass(1, true)
			r.setBypass(2, true)
		case 40:
			r.setBypass(1, false)
			r.setBypass(2, false)
		}
		if r.latency() != lat {
			t.Fatalf("bypassed, the chain's latency is %d, was %d", r.latency(), lat)
		}
		n, err := st.Read(dst)
		if err != nil {
			t.Fatal(err)
		}
		for i := range n {
			out = append(out, dst[2*i])
		}
		// The stage stays aligned: running the plugins over their
		// latency again would feed them what they already heard.
		if b == 0 {
			aligned = st.gen
		} else if st.gen != aligned {
			t.Fatalf("after block %d the stage aligned again", b)
		}
	}
	if len(out) < 40*512 {
		t.Fatalf("the stage played %d frames", len(out))
	}
	// Written so a NaN fails too.
	for i := 1; i < len(out); i++ {
		if d := out[i] - out[i-1]; !(d <= 1.2*most && d >= -1.2*most) {
			t.Fatalf("at frame %d the sound jumps %.4f, where a sine moves %.4f at most: a click", i, d, most)
		}
	}
	// Faded out, the sound as fed, in time with the chain: the stage
	// plays its sound as late as it is, so in time with the sine.
	for i := 35 * 512; i < 40*512; i++ {
		if d := out[i] - sineAt(int64(i)); !(d <= 1e-3 && d >= -1e-3) {
			t.Fatalf("bypassed, frame %d comes out %.4f, want %.4f", i, out[i], sineAt(int64(i)))
		}
	}
}

func TestASwapFadesFromOneChainToTheOtherAndLetsTheOldGo(t *testing.T) {
	trimAt := func(id int, db float64) *livePlugin {
		t.Helper()
		lp, err := newPlugin(trimSlot(id), goSettings{Params: map[uint32]float64{trim.GainID: db}}.pack(), 48000, false)
		if err != nil {
			t.Fatal(err)
		}
		return lp
	}
	up := trimAt(101, 6)
	r := newRackOf([]*livePlugin{up})
	defer r.close()
	st := newStage(&sineSource{}, r, 0)
	most := float32(2 * sineA * 2 * math.Pi * sineHz / 48000)
	out := make([]float32, 0, 40*512)
	dst := make([]float32, 2*512)
	for b := range 40 {
		if b == 10 {
			r.swap([]*livePlugin{trimAt(102, -6)}, 48000)
		}
		if b == 30 {
			r.retire()
		}
		n, err := st.Read(dst)
		if err != nil {
			t.Fatal(err)
		}
		for i := range n {
			out = append(out, dst[2*i])
		}
	}
	// Written so a NaN fails too.
	for i := 1; i < len(out); i++ {
		if d := out[i] - out[i-1]; !(d <= 1.2*most && d >= -1.2*most) {
			t.Fatalf("at frame %d the sound jumps %.4f, where a sine moves %.4f at most: a stutter", i, d, most)
		}
	}
	// After the fade, the chain swapped in: the sine 6 dB down.
	for i := 25 * 512; i < 30*512; i++ {
		want := sineAt(int64(i)) * float32(math.Pow(10, -6.0/20))
		if d := out[i] - want; !(d <= 1e-3 && d >= -1e-3) {
			t.Fatalf("after the swap, frame %d comes out %.4f, want %.4f", i, out[i], want)
		}
	}
	if _, ok := livePlugins.Load(101); ok || r.fade != nil || len(r.fadeFrom) != 0 {
		t.Errorf("faded from, the old plugin is still held: live %v, fading %v", ok, len(r.fadeFrom))
	}
}

func TestASwapWhereTheSoundDoesNotPlayLetsTheOldGoAtOnce(t *testing.T) {
	lp, err := newPlugin(trimSlot(111), nil, 48000, false)
	if err != nil {
		t.Fatal(err)
	}
	r := newRackOf([]*livePlugin{lp})
	defer r.close()
	r.setActive(false)
	in, err := newPlugin(trimSlot(112), nil, 48000, false)
	if err != nil {
		t.Fatal(err)
	}
	r.swap([]*livePlugin{in}, 48000)
	if _, ok := livePlugins.Load(111); ok || r.fade != nil || len(r.list()) != 1 || r.list()[0] != in {
		t.Errorf("swapped while still, the old plugin is held %v, fading %v", ok, r.fade != nil)
	}
}
