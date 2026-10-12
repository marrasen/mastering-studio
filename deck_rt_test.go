package main

import (
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/plug/trim"
)

// playingDeck plays a 20-second track at 44.1 kHz through a chain of
// trims, mixed at rate.
func playingDeck(t *testing.T, rate int) (*deck, *audio.Mixer, string) {
	t.Helper()
	path := writeTrack(t, 0, 20*time.Second, 0, 0.3)
	mix := audio.NewMixer()
	mix.SetRate(rate)
	d := newDeck(mix)
	r := newRackOf(nil)
	for i := range 3 {
		lp, err := newPlugin(trimSlot(i+1), nil, 44100, false)
		if err != nil {
			t.Fatal(err)
		}
		r.add(lp)
	}
	t.Cleanup(func() {
		d.stop()
		r.close()
	})
	if err := d.play(1, path, time.Second, Edit{}, r, 0, false, 0); err != nil {
		t.Fatal(err)
	}
	return d, mix, path
}

func TestPlayingMakesNothingAsTheTrackChanges(t *testing.T) {
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	for _, rate := range []int{44100, 48000} {
		d, mix, path := playingDeck(t, rate)
		buf := make([]float32, 2*1024)
		for range 20 {
			mix.Mix(buf)
		}
		// Only what mixing makes counts: the stream's decoder, the
		// mixer's teller and the deck's watch make what they make on
		// goroutines of their own, meanwhile.
		made := allocsUnder(t, "(*Mixer).Mix", func() {
			for i := range 600 {
				switch i % 100 {
				case 10:
					d.edit(1, path, time.Second, Edit{FadeIn: Fade{Length: time.Second}})
				case 30:
					d.setLoop(1, path, time.Second, Edit{}, 2*time.Second, 2500*time.Millisecond, true)
				case 70:
					d.setLoop(1, path, time.Second, Edit{}, 0, 0, false)
					d.seek(5 * time.Second)
				}
				if i%100 == 30 || i%100 == 70 {
					// Time for the stream to decode where it must.
					time.Sleep(20 * time.Millisecond)
				}
				mix.Mix(buf)
			}
		})
		if made != 0 {
			t.Fatalf("mixing at %d Hz made %d things over 600 blocks", rate, made)
		}
	}
}

// allocsUnder runs run, and returns how many things were made in it
// with fn among their callers: the allocations of one call, as other
// goroutines allocate as they like meanwhile.
func allocsUnder(t *testing.T, fn string, run func()) int64 {
	t.Helper()
	was := runtime.MemProfileRate
	runtime.MemProfileRate = 1
	defer func() { runtime.MemProfileRate = was }()
	count := func() int64 {
		// The profile is as of the last collection, finished.
		runtime.GC()
		runtime.GC()
		var recs []runtime.MemProfileRecord
		n, _ := runtime.MemProfile(nil, true)
		for {
			recs = make([]runtime.MemProfileRecord, n+50)
			var ok bool
			if n, ok = runtime.MemProfile(recs, true); ok {
				recs = recs[:n]
				break
			}
		}
		var total int64
		for _, r := range recs {
			frames := runtime.CallersFrames(r.Stack())
			for {
				f, more := frames.Next()
				if strings.Contains(f.Function, fn) {
					total += r.AllocObjects
					break
				}
				if !more {
					break
				}
			}
		}
		return total
	}
	before := count()
	run()
	return count() - before
}

func TestPlayingChangesAsTheApplicationWorksMeanwhile(t *testing.T) {
	d, mix, path := playingDeck(t, 48000)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]float32, 2*512)
		for {
			select {
			case <-stop:
				return
			default:
				mix.Mix(buf)
			}
		}
	}()
	for i := range 200 {
		switch i % 5 {
		case 0:
			d.edit(1, path, time.Second, Edit{Gain: float32(i%7) - 3})
		case 1:
			d.setLoop(1, path, time.Second, Edit{}, time.Duration(i%9)*time.Second, time.Duration(i%9)*time.Second+300*time.Millisecond, i%2 == 0)
		case 2:
			d.seek(time.Duration(i%15) * time.Second)
		case 3:
			_, _, _ = d.position()
		case 4:
			d.setPaused(i%4 == 0)
		}
		time.Sleep(time.Millisecond)
	}
	close(stop)
	<-done
}

func TestAPluginsViewsHearItsSoundInAndOut(t *testing.T) {
	d, mix, _ := playingDeck(t, 44100)
	// The second trim, at -6 dB, played through.
	v, ok := livePlugins.Load(2)
	lp, isLive := v.(*livePlugin)
	if !ok || !isLive {
		t.Fatal("the trim playing is not among the plugins live")
	}
	e, ok := lp.p.(*goEngine)
	if !ok {
		t.Fatal("the trim is no Go plugin")
	}
	e.setParam(trim.GainID, -6)
	in := heardSound{slot: 2, d: d}
	out := heardSound{slot: 2, d: d, out: true}
	dst := make([]float32, 2*2048)
	// Asked the first time, it starts keeping the sound.
	in.Heard(dst)
	buf := make([]float32, 2*512)
	for range 400 {
		mix.Mix(buf)
	}
	rms := func(s heardSound) float64 {
		t.Helper()
		n, _, rate := s.Heard(dst)
		if n != 2048 || rate != 44100 {
			t.Fatalf("heard %d frames at %d Hz, want 2048 at 44100", n, rate)
		}
		var sum float64
		for _, x := range dst[:2*n] {
			sum += float64(x) * float64(x)
		}
		return math.Sqrt(sum / float64(2*n))
	}
	ri, ro := rms(in), rms(out)
	if ri == 0 || math.Abs(ro/ri-math.Pow(10, -6.0/20)) > 0.01 {
		t.Fatalf("the trim at -6 dB is heard at %.4f in and %.4f out", ri, ro)
	}
	// Nothing heard of a slot that plays nowhere.
	if n, _, _ := (heardSound{slot: 99, d: d}).Heard(dst); n != 0 {
		t.Fatalf("a slot playing nowhere is heard, %d frames", n)
	}
}
