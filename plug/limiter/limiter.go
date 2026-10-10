// Package limiter is a master limiter: it turns the sound up by Drive
// and keeps its peaks under Ceiling, looking ahead so it turns them
// down smoothly before they come. Its views are two classic VU meters,
// of how far it turns the sound down and of the sound that comes out,
// over its controls.
package limiter

import (
	"errors"
	"fmt"
	"math"

	"github.com/marrasen/gunim"
	"github.com/marrasen/mastering-studio/plug"
)

// The parameters' IDs.
const (
	DriveID uint32 = iota + 1
	CeilingID
	ReleaseID
	// ReductionID is how far the limiter turns the sound down now, in
	// decibels, as it reports it.
	ReductionID
)

// Descriptor is the plugin, as it is registered.
var Descriptor = plug.Descriptor{
	ID:      "marrasen.limiter",
	Name:    "Limiter",
	Vendor:  "Marras",
	Version: "0.1.0",
	Kind:    "Fx|Dynamics",
	Params: []plug.Param{
		{ID: DriveID, Name: "Drive", Unit: "dB", Min: 0, Max: 24, Default: 0, Flags: plug.Automatable,
			Format: func(v float64) string { return fmt.Sprintf("%+.1f dB", v) }},
		{ID: CeilingID, Name: "Ceiling", Unit: "dB", Min: -12, Max: 0, Default: -1, Flags: plug.Automatable,
			Format: func(v float64) string { return fmt.Sprintf("%.1f dB", v) }},
		{ID: ReleaseID, Name: "Release", Unit: "ms", Min: 10, Max: 1000, Default: 120, Flags: plug.Automatable,
			Format: func(v float64) string { return fmt.Sprintf("%.0f ms", v) }},
		{ID: ReductionID, Name: "Reduction", Unit: "dB", Min: -24, Max: 0, Default: 0, Flags: plug.ReadOnly,
			Format: func(v float64) string { return fmt.Sprintf("%.1f dB", v) }},
	},
	New: func(h plug.Host) plug.Plugin { return &plugin{} },
}

func init() { plug.Register(Descriptor) }

// lookahead is how far ahead the limiter looks, in seconds: as late as
// its sound comes.
const lookahead = 0.005

// plugin is one limiter.
type plugin struct{ proc processor }

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: a limiter keeps nothing but its
// parameters.
func (p *plugin) State() []byte { return nil }

// SetState implements [plug.Plugin]: it takes only an empty state.
func (p *plugin) SetState(b []byte) error {
	if len(b) > 0 {
		return errors.New("limiter: a limiter keeps no state")
	}
	return nil
}

// Close implements [plug.Plugin].
func (p *plugin) Close() {}

// View implements [plug.Viewer]: a bar of the reduction in a chip, and
// the meters over the controls in a panel, which serves in full too.
func (p *plugin) View(s plug.Size, h plug.Host) gunim.Node {
	switch s {
	case plug.Chip:
		return newChip(h)
	case plug.Panel:
		return newPanel(h)
	default:
		return nil
	}
}

// processor limits the sound. For each frame it works out the gain that
// keeps the frame under the ceiling; the gain applied, lookahead later,
// is the least of those over the frames to come, averaged over as many,
// so it falls smoothly to what the loudest needs as that arrives, and
// rises again as slow as Release says.
type processor struct {
	rate float64
	// ahead is the lookahead in frames, delay the frames held back over
	// it, and at where in delay the next goes.
	ahead int
	delay []float32
	at    int
	// The least gain over the last ahead+1 frames, as a queue of the
	// frames that may yet be the least: their counts and gains, from
	// head, n of them, in rings of ahead+1.
	minAt   []int64
	minGain []float64
	head, n int
	count   int64
	// The average of the least gains over the last ahead frames: their
	// ring, where the next goes, and their sum.
	avg    []float64
	avgAt  int
	avgSum float64
	// gain is the gain applied; drive the gain in now, gliding to
	// driveTo; ceiling the most a sample may be; release how far gain
	// rises toward what the sound allows each frame.
	gain, drive, driveTo, ceiling, release float64
	glide                                  float64
	// fresh is set from a reset to the first block, whose drive is
	// taken at once rather than glided to.
	fresh bool
	// reduction is the least gain of the last block, in decibels, as
	// the plugin reports it.
	reduction plug.Meter
}

// Prepare implements [plug.Processor]: everything the limiter keeps is
// made here, so that processing makes nothing.
func (p *processor) Prepare(s plug.Setup) {
	p.rate = s.Rate
	p.ahead = max(1, int(math.Round(s.Rate*lookahead)))
	p.delay = make([]float32, 2*p.ahead)
	p.minAt, p.minGain = make([]int64, p.ahead+1), make([]float64, p.ahead+1)
	p.avg = make([]float64, p.ahead)
	p.glide = 1 - math.Exp(-1/(s.Rate*0.005))
	p.setCeiling(-1)
	p.setRelease(120)
	p.drive, p.driveTo = 1, 1
	p.Reset()
}

func (p *processor) setCeiling(db float64) {
	p.ceiling = math.Pow(10, Descriptor.Params[1].Clamp(db)/20)
}

func (p *processor) setRelease(ms float64) {
	p.release = 1 - math.Exp(-1/(Descriptor.Params[2].Clamp(ms)/1000*p.rate))
}

// take takes a change to a parameter.
func (p *processor) take(c plug.Change) {
	switch c.ID {
	case DriveID:
		p.driveTo = math.Pow(10, Descriptor.Params[0].Clamp(c.Value)/20)
		if p.fresh {
			p.drive = p.driveTo
		}
	case CeilingID:
		p.setCeiling(c.Value)
	case ReleaseID:
		p.setRelease(c.Value)
	}
}

// Process implements [plug.Processor].
func (p *processor) Process(b *plug.Block) {
	if len(b.In) == 0 || len(b.Out) == 0 {
		return
	}
	inL, outL := b.In[0], b.Out[0]
	inR, outR := inL, outL
	if len(b.In) > 1 && len(b.Out) > 1 {
		inR, outR = b.In[1], b.Out[1]
	}
	next := 0
	least := 1.0
	for i := range b.Frames {
		for next < len(b.Changes) && b.Changes[next].Frame <= i {
			p.take(b.Changes[next])
			next++
		}
		p.drive += (p.driveTo - p.drive) * p.glide
		l, r := float64(inL[i])*p.drive, float64(inR[i])*p.drive
		need := 1.0
		if peak := max(math.Abs(l), math.Abs(r)); peak > p.ceiling {
			need = p.ceiling / peak
		}
		g := p.smooth(p.leastOf(need))
		// The frame ahead frames back, which g was worked out for.
		dl, dr := float64(p.delay[2*p.at]), float64(p.delay[2*p.at+1])
		p.delay[2*p.at], p.delay[2*p.at+1] = float32(l), float32(r)
		p.at = (p.at + 1) % p.ahead
		outL[i] = float32(clamp(dl*g, p.ceiling))
		if len(b.Out) > 1 {
			outR[i] = float32(clamp(dr*g, p.ceiling))
		}
		least = min(least, g)
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
	p.fresh = false
	p.reduction.Set(20 * math.Log10(max(least, 1e-6)))
}

// leastOf adds need, the gain a frame needs, and returns the least of
// the last ahead+1.
func (p *processor) leastOf(need float64) float64 {
	size := len(p.minGain)
	// Those no less than need can no longer be the least.
	for p.n > 0 && p.minGain[(p.head+p.n-1)%size] >= need {
		p.n--
	}
	tail := (p.head + p.n) % size
	p.minAt[tail], p.minGain[tail] = p.count, need
	p.n++
	// Those older than the window go.
	if p.minAt[p.head] <= p.count-int64(size) {
		p.head = (p.head + 1) % size
		p.n--
	}
	p.count++
	return p.minGain[p.head]
}

// smooth averages least over the last ahead frames, and lets the gain
// rise from there no faster than the release.
func (p *processor) smooth(least float64) float64 {
	p.avgSum += least - p.avg[p.avgAt]
	p.avg[p.avgAt] = least
	p.avgAt++
	if p.avgAt == len(p.avg) {
		p.avgAt = 0
		// Summed anew once around, so rounding never builds up.
		p.avgSum = 0
		for _, v := range p.avg {
			p.avgSum += v
		}
	}
	a := p.avgSum / float64(len(p.avg))
	if a < p.gain {
		p.gain = a
	} else {
		p.gain += (a - p.gain) * p.release
	}
	return p.gain
}

// clamp keeps v within the ceiling, either way, where rounding would
// take it a hair past.
func clamp(v, ceiling float64) float64 { return max(-ceiling, min(v, ceiling)) }

// Latency implements [plug.Processor]: the sound comes the lookahead
// late.
func (p *processor) Latency() int { return p.ahead }

// Tail implements [plug.Processor]: the limiter never rings.
func (p *processor) Tail() int { return 0 }

// Reset implements [plug.Processor]: what it held, let go.
func (p *processor) Reset() {
	clear(p.delay)
	for i := range p.avg {
		p.avg[i] = 1
	}
	p.avgSum, p.avgAt = float64(len(p.avg)), 0
	p.head, p.n, p.count, p.at = 0, 0, 0, 0
	p.gain = 1
	p.drive = p.driveTo
	p.fresh = true
	p.reduction.Set(0)
}

var _ plug.Reporter = (*processor)(nil)

// Report implements [plug.Reporter]: the reduction.
func (p *processor) Report(id uint32) float64 {
	if id == ReductionID {
		return p.reduction.Get()
	}
	return 0
}
