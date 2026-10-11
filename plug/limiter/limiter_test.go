package limiter

import (
	"math"
	"math/rand/v2"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/mastering-studio/plug"
)

// host is a host for tests, which notes the gestures it is sent.
type host struct {
	values map[uint32]float64
	log    []string
	// out is the sound the views hear come out, or nil.
	out plug.Sound
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
func (h *host) Sound() (in, out plug.Sound)        { return nil, h.out }

func newLimiter(t *testing.T) (*plugin, *host) {
	t.Helper()
	d, ok := plug.Lookup("marrasen.limiter")
	if !ok {
		t.Fatal("the limiter did not register itself")
	}
	h := &host{values: map[uint32]float64{DriveID: 0, CeilingID: -1, ReleaseID: 120}}
	p, ok := d.New(h).(*plugin)
	if !ok {
		t.Fatal("the limiter made something else")
	}
	return p, h
}

// settings are the changes that set drive and ceiling, at frame 0.
func settings(drive, ceiling float64) []plug.Change {
	return []plug.Change{{ID: DriveID, Value: drive}, {ID: CeilingID, Value: ceiling}, {ID: ReleaseID, Value: 50}}
}

// loud returns n frames of music-like sound: a sine under bursts of
// noise, with lone spikes, the sharpest a limiter meets.
func loud(n int, seed uint64) []float32 {
	r := rand.New(rand.NewPCG(seed, 1))
	s := make([]float32, n)
	for i := range s {
		v := 0.3 * math.Sin(2*math.Pi*110*float64(i)/48000)
		if (i/2000)%3 == 0 {
			v += 0.6 * (2*r.Float64() - 1)
		}
		if i%3001 == 1500 {
			v = 1
		}
		s[i] = float32(v)
	}
	return s
}

func TestTheGainIsDownBeforeEachPeakComesOut(t *testing.T) {
	p, _ := newLimiter(t)
	pr := &p.proc
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 1, Channels: 2})
	const drive, ceiling = 12.0, -1.0
	l, r := loud(48000, 1), loud(48000, 2)
	top := math.Pow(10, ceiling/20)
	ahead := pr.Latency()
	// The drive changes halfway, and glides there.
	changes := settings(drive, ceiling)
	// A frame at a time, so the gain each frame goes out at is seen
	// before the clamp after it could hide a gain too high.
	outL, outR := make([]float32, 1), make([]float32, 1)
	peaks := make([]float64, len(l))
	worst := 0.0
	for i := range l {
		if i == len(l)/2 {
			changes = []plug.Change{{ID: DriveID, Value: drive + 6}}
		}
		pr.Process(&plug.Block{In: [][]float32{l[i : i+1], r[i : i+1]}, Out: [][]float32{outL, outR}, Frames: 1, Changes: changes})
		changes = nil
		peaks[i] = max(math.Abs(float64(l[i])), math.Abs(float64(r[i]))) * pr.drive
		if i >= ahead {
			worst = max(worst, peaks[i-ahead]*pr.gain/top)
		}
	}
	if worst > 1+1e-9 {
		t.Errorf("a peak went out %.2g dB over the ceiling, before the clamp", 20*math.Log10(worst))
	}
	if worst < 0.99 {
		t.Errorf("the loudest peak went out %.2f dB under the ceiling, want at it", 20*math.Log10(worst))
	}
}

func TestQuietSoundPassesUnchangedButLate(t *testing.T) {
	p, _ := newLimiter(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	if pr.Latency() != 240 {
		t.Fatalf("the limiter is %d frames late at 48 kHz, want its 5 ms, 240", pr.Latency())
	}
	in := make([]float32, 4096)
	for i := range in {
		in[i] = float32(0.25 * math.Sin(2*math.Pi*1000*float64(i)/48000))
	}
	out := make([]float32, len(in))
	changes := settings(0, -1)
	for at := 0; at < len(in); at += 512 {
		pr.Process(&plug.Block{In: [][]float32{in[at : at+512]}, Out: [][]float32{out[at : at+512]}, Frames: 512, Changes: changes})
		changes = nil
	}
	for i := range out {
		want := float32(0)
		if i >= 240 {
			want = in[i-240]
		}
		if math.Abs(float64(out[i]-want)) > 1e-6 {
			t.Fatalf("frame %d came out %v, want %v", i, out[i], want)
		}
	}
	if p.proc.Report(ReductionID) != 0 {
		t.Error("the limiter reports turning quiet sound down")
	}
}

func TestTheReductionIsReportedAndResetLetsGo(t *testing.T) {
	p, _ := newLimiter(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 4800, Channels: 2})
	in := make([]float32, 4800)
	for i := range in {
		in[i] = float32(math.Sin(2 * math.Pi * 1000 * float64(i) / 48000))
	}
	ch := [][]float32{slices.Clone(in), slices.Clone(in)}
	// Full scale, driven 6 dB into a ceiling at -1: turned down 7 dB.
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 4800, Changes: settings(6, -1)})
	got := p.proc.Report(ReductionID)
	if math.Abs(got+7) > 0.05 {
		t.Errorf("the limiter reports %.2f dB, want -7", got)
	}
	pr.Reset()
	if got := p.proc.Report(ReductionID); got != 0 {
		t.Errorf("after a reset the limiter reports %.2f dB, want 0", got)
	}
	// Nothing of before comes out after a reset.
	quiet := [][]float32{make([]float32, 240), make([]float32, 240)}
	pr.Process(&plug.Block{In: quiet, Out: quiet, Frames: 240})
	for _, v := range quiet[0] {
		if v != 0 {
			t.Fatal("sound from before the reset came out after it")
		}
	}
}

func TestProcessingAllocatesNothing(t *testing.T) {
	p, _ := newLimiter(t)
	pr := p.Processor()
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 512, Channels: 2})
	ch := [][]float32{loud(512, 1), loud(512, 2)}
	if raceOn {
		t.Skip("the race detector allocates of its own")
	}
	// On one thread, as testing.AllocsPerRun counts, so other
	// goroutines allocate as little as may be meanwhile; every
	// allocation in Process counted, not a mean rounded down.
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	var made uint64
	for i := range 64 {
		b := &plug.Block{In: ch, Out: ch, Frames: 512, Changes: settings(float64(i%24), -float64(i%12))}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		pr.Process(b)
		_ = p.proc.Report(ReductionID)
		runtime.ReadMemStats(&after)
		made += after.Mallocs - before.Mallocs
	}
	if made != 0 {
		t.Errorf("processing made %d things over 64 blocks", made)
	}
}

func TestTheFirstDriveIsTakenAtOnceAndLaterOnesGlide(t *testing.T) {
	p, _ := newLimiter(t)
	pr := &p.proc
	pr.Prepare(plug.Setup{Rate: 48000, MaxBlock: 64, Channels: 1})
	ch := [][]float32{make([]float32, 64)}
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 64, Changes: settings(12, -1)})
	if want := math.Pow(10, 12.0/20); math.Abs(pr.drive-want) > 1e-9 {
		t.Fatalf("the first block drives by %v, want +12 dB's %v at once", pr.drive, want)
	}
	pr.Process(&plug.Block{In: ch, Out: ch, Frames: 64, Changes: []plug.Change{{ID: DriveID, Value: 0}}})
	if pr.drive < 2 {
		t.Errorf("a later drive jumped to %v in a block's 64 frames", pr.drive)
	}
}

func TestTheLimiterKeepsNoState(t *testing.T) {
	p, _ := newLimiter(t)
	if p.State() != nil || p.SetState(nil) != nil {
		t.Error("a limiter has state")
	}
	if p.SetState([]byte("x")) == nil {
		t.Error("a limiter took state it cannot have")
	}
}

func TestTheNeedleSwingsAsAVUMetersDoes(t *testing.T) {
	m := newVU("TEST", func(time.Duration) (float32, bool) { return 0, true })
	from, to := m.needle.Value(), vuAt(0)
	step := time.Millisecond
	var reached time.Duration
	most := float32(0)
	for at := step; at <= time.Second; at += step {
		m.Step(step)
		x := (m.needle.Value() - from) / (to - from)
		if reached == 0 && x >= 0.99 {
			reached = at
		}
		most = max(most, x)
	}
	if reached < 270*time.Millisecond || reached > 330*time.Millisecond {
		t.Errorf("the needle swung 99%% of the way in %v, want 300 ms", reached)
	}
	if over := (most - 1) * 100; over < 0.5 || over > 2 {
		t.Errorf("the needle overshot by %.2f%%, want about 1.3", over)
	}
}

// sine is a sine at amp, heard to frame at.
type sine struct {
	amp float64
	at  int64
}

func (s *sine) Heard(dst []float32) (frames int, at int64, rate int) {
	n := len(dst) / 2
	for i := range n {
		v := float32(s.amp * math.Sin(2*math.Pi*1000*float64(s.at-int64(n)+int64(i))/48000))
		dst[2*i], dst[2*i+1] = v, v
	}
	return n, s.at, 48000
}

func TestTheMetersReadTheReductionAndTheSound(t *testing.T) {
	p, h := newLimiter(t)
	// A sine at -18 dBFS RMS reads 0 VU.
	s := &sine{amp: math.Sqrt2 * math.Pow(10, -18.0/20)}
	h.out = s
	h.values[ReductionID] = -4.5
	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	s.at += 48000
	if db, live := pn.out.read(0); !live || math.Abs(float64(db)) > 0.05 {
		t.Errorf("a sine at -18 dBFS reads %.2f VU, want 0", db)
	}
	if db, live := pn.gr.read(0); !live || db != -4.5 {
		t.Errorf("the reduction reads %v, want the host's -4.5", db)
	}
	// Between the chunks the sound comes in, both hold.
	frame := plug.StillAfter / 10
	if db, live := pn.out.read(frame); !live || math.Abs(float64(db)) > 0.05 {
		t.Errorf("between chunks the sound reads %.2f VU, want 0 held", db)
	}
	if db, live := pn.gr.read(frame); !live || db != -4.5 {
		t.Errorf("between chunks the reduction reads %v, want -4.5 held", db)
	}
	// The sound stops: both meters fall to rest.
	for range 10 {
		pn.out.read(frame)
		pn.gr.read(frame)
	}
	if db, live := pn.out.read(frame); live || db > vuLow {
		t.Errorf("a still sound reads %.2f VU, want under the scale", db)
	}
	if db, live := pn.gr.read(frame); live || db != 0 {
		t.Errorf("a still sound's reduction reads %v, want 0", db)
	}
}

func TestTheViewsWorkThroughTheHost(t *testing.T) {
	p, h := newLimiter(t)
	h.values[CeilingID] = -0.3
	if p.View(plug.Full, h) != nil {
		t.Error("the limiter has a full view of its own")
	}
	c, ok := p.View(plug.Chip, h).(*chip)
	if !ok || c.label.Text != "-0.3 dB" {
		t.Fatalf("the chip shows %q, want -0.3 dB", c.label.Text)
	}
	c.Follow(CeilingID, -2, nil)
	if c.label.Text != "-2.0 dB" {
		t.Errorf("the chip follows to %q, want -2.0 dB", c.label.Text)
	}

	pn, ok := p.View(plug.Panel, h).(*panel)
	if !ok {
		t.Fatal("the panel is of another type")
	}
	drive := pn.rows[0].Slider
	if pn.rows[1].Slider.Value() != -0.3 {
		t.Fatal("the ceiling's slider does not start at the host's ceiling")
	}
	// A drag of two steps and a let-go is one gesture; a key's step
	// commits alone, as a gesture of its own.
	var u *gunim.UI
	drive.OnChange(2, u)
	drive.OnChange(3, u)
	drive.OnCommit(3, u)
	drive.OnCommit(3.5, u)
	want := []string{"begin", "set", "set", "set", "end", "begin", "set", "end"}
	if !slices.Equal(h.log, want) {
		t.Errorf("the host heard %v, want %v", h.log, want)
	}
	if h.values[DriveID] != 3.5 {
		t.Errorf("the host's drive is %v, want 3.5", h.values[DriveID])
	}
	pn.Follow(ReleaseID, 400, nil)
	if pn.rows[2].Slider.Value() != 400 {
		t.Errorf("the release's slider follows to %v, want 400", pn.rows[2].Slider.Value())
	}
}
