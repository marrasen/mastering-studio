// Package eq is a parametric EQ in the [plug] format: up to eight
// bands, each a bell, a shelf, a cut or a notch, kept as the plugin's
// state, and a graph to drag them about on.
package eq

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/mastering-studio/plug"
)

// BypassID is the ID of the bypass parameter: over 0.5 passes the
// sound by the EQ.
const BypassID uint32 = 1

// Descriptor is the plugin, as it is registered.
var Descriptor = plug.Descriptor{
	ID:      "marrasen.eq",
	Name:    "EQ",
	Vendor:  "Marras",
	Version: "0.1.0",
	Kind:    "Fx|EQ",
	Params: []plug.Param{{
		ID: BypassID, Name: "Bypass",
		Min: 0, Max: 1, Default: 0, Steps: 1,
		Flags: plug.Bypass | plug.Hidden,
	}},
	New: func(h plug.Host) plug.Plugin { return newPlugin(h) },
}

func init() { plug.Register(Descriptor) }

// The bands' bounds: frequencies from eqLow to eqHigh, gains of
// eqRange decibels each way, and Q from minQ to maxQ.
const (
	eqLow, eqHigh = 20.0, 20000.0
	eqRange       = 18
	maxBands      = 8
	minQ, maxQ    = 0.1, 18
)

// version is the version of the state's format that State writes.
const version = 1

// plugin is one EQ.
type plugin struct {
	host plug.Host
	eq   *audio.EQ
	proc processor
	// mu guards bands, which State and SetState reach from the app's
	// goroutine and the views from the window's.
	mu    sync.Mutex
	bands []audio.Band
}

func newPlugin(h plug.Host) *plugin {
	p := &plugin{host: h, eq: audio.NewEQ()}
	p.proc.eq = p.eq
	return p
}

// Processor implements [plug.Plugin].
func (p *plugin) Processor() plug.Processor { return &p.proc }

// State implements [plug.Plugin]: the bands, as JSON.
func (p *plugin) State() []byte {
	b, err := json.Marshal(stateOf(p.Bands()))
	if err != nil {
		// Bands of numbers and names always marshal.
		panic("eq: " + err.Error())
	}
	return b
}

// SetState implements [plug.Plugin]: it takes the bands State wrote,
// and none for an empty state. A state it cannot read changes nothing.
func (p *plugin) SetState(b []byte) error {
	var bands []audio.Band
	if len(b) > 0 {
		var err error
		if bands, err = parseState(b); err != nil {
			return err
		}
	}
	p.setBands(bands)
	return nil
}

// Close implements [plug.Plugin].
func (p *plugin) Close() {}

// Bands returns a copy of the EQ's bands.
func (p *plugin) Bands() []audio.Band {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.bands)
}

// setBands makes bands the EQ's bands, for the sound too.
func (p *plugin) setBands(bands []audio.Band) {
	p.mu.Lock()
	p.bands = slices.Clone(bands)
	p.mu.Unlock()
	p.eq.Set(bands)
}

// View implements [plug.Viewer]: the curve in a chip, the graph in a
// panel, and the graph over the band picked in full.
func (p *plugin) View(s plug.Size, h plug.Host) gunim.Node {
	switch s {
	case plug.Chip:
		return newChip(p, h)
	case plug.Panel:
		return newPanel(p, h)
	case plug.Full:
		return newFull(p, h)
	default:
		return nil
	}
}

// state is the plugin's state as it is saved: its format's version and
// the bands.
type state struct {
	Version int         `json:"version"`
	Bands   []savedBand `json:"bands"`
}

// savedBand is a band as it is saved, its kind by name, so the format
// is the plugin's own and not gunim's.
type savedBand struct {
	ID    int     `json:"id"`
	Kind  string  `json:"kind"`
	Freq  float32 `json:"freq"`
	Gain  float32 `json:"gain"`
	Q     float32 `json:"q"`
	Slope int     `json:"slope,omitempty"`
	On    bool    `json:"on"`
}

// kindKeys name the kinds of band in the saved state, in their order.
var kindKeys = []string{"bell", "lowShelf", "highShelf", "lowCut", "highCut", "notch"}

// stateOf returns bands as they are saved.
func stateOf(bands []audio.Band) state {
	s := state{Version: version, Bands: make([]savedBand, len(bands))}
	for i, b := range bands {
		s.Bands[i] = savedBand{ID: b.ID, Kind: kindKeys[b.Kind], Freq: b.Freq, Gain: b.Gain, Q: b.Q, Slope: b.Slope, On: b.On}
	}
	return s
}

// parseState returns the bands in b, or what is wrong with it.
func parseState(b []byte) ([]audio.Band, error) {
	var s state
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("eq: reading the state: %w", err)
	}
	if s.Version != version {
		return nil, fmt.Errorf("eq: the state is of version %d, and this EQ reads version %d", s.Version, version)
	}
	if len(s.Bands) > maxBands {
		return nil, fmt.Errorf("eq: the state has %d bands, and an EQ has at most %d", len(s.Bands), maxBands)
	}
	bands := make([]audio.Band, len(s.Bands))
	seen := map[int]bool{}
	for i, sb := range s.Bands {
		kind := slices.Index(kindKeys, sb.Kind)
		if kind < 0 {
			return nil, fmt.Errorf("eq: band %d is of an unknown kind, %q", i+1, sb.Kind)
		}
		b := audio.Band{ID: sb.ID, Kind: audio.FilterKind(kind), Freq: sb.Freq, Gain: sb.Gain, Q: sb.Q, Slope: sb.Slope, On: sb.On}
		if err := checkBand(b); err != nil {
			return nil, fmt.Errorf("eq: band %d %w", i+1, err)
		}
		if seen[b.ID] {
			return nil, fmt.Errorf("eq: two bands have the ID %d", b.ID)
		}
		seen[b.ID] = true
		bands[i] = b
	}
	return bands, nil
}

// checkBand returns what is wrong with b, if anything, as the end of a
// sentence about it.
func checkBand(b audio.Band) error {
	inside := func(v, lo, hi float32) bool { return v >= lo && v <= hi }
	switch {
	case b.ID <= 0:
		return errors.New("has no ID")
	case !inside(b.Freq, eqLow, eqHigh):
		return fmt.Errorf("is at %g Hz, outside %g Hz to %g Hz", b.Freq, eqLow, eqHigh)
	case !inside(b.Gain, -eqRange, eqRange):
		return fmt.Errorf("has a gain of %g dB, outside ±%d dB", b.Gain, eqRange)
	case !inside(b.Q, minQ, maxQ):
		return fmt.Errorf("has a Q of %g, outside %g to %g", b.Q, minQ, float32(maxQ))
	case b.Slope != 0 && (b.Slope%12 != 0 || !inside(float32(b.Slope), 12, 48)):
		return fmt.Errorf("has a slope of %d dB an octave, not 12, 24, 36 or 48", b.Slope)
	}
	return nil
}

// piece is how many frames the insert takes at a time: an EQ glides
// toward its settings a step each call, and the steps are made for
// the 128 frames a mixer gives it.
const piece = 128

// processor runs the EQ on a block's first two channels.
type processor struct {
	eq     *audio.EQ
	insert audio.Insert
	rate   float64
	// scratch holds a chunk of the block as interleaved stereo frames.
	scratch []float32
	// bypass is the bypass the EQ was last told.
	bypass bool
}

// Prepare implements [plug.Processor].
func (p *processor) Prepare(s plug.Setup) {
	p.rate = s.Rate
	p.insert = p.eq.InsertAt(int(s.Rate))
	p.scratch = make([]float32, 2*max(s.MaxBlock, 0))
}

// Process implements [plug.Processor]: it works through the block in
// chunks that fit the scratch buffer, and each change takes effect from
// the start of the chunk its frame falls in. Channels past the second
// pass through.
func (p *processor) Process(b *plug.Block) {
	for ch := 2; ch < len(b.Out) && ch < len(b.In); ch++ {
		copy(b.Out[ch][:b.Frames], b.In[ch][:b.Frames])
	}
	chunk := len(p.scratch) / 2
	if p.insert == nil || chunk == 0 || len(b.In) == 0 || len(b.Out) == 0 {
		p.passBy(b)
		return
	}
	next := 0
	for at := 0; at < b.Frames; at += chunk {
		n := min(chunk, b.Frames-at)
		for next < len(b.Changes) && b.Changes[next].Frame < at+n {
			p.take(b.Changes[next])
			next++
		}
		p.run(b, at, n)
	}
	for ; next < len(b.Changes); next++ {
		p.take(b.Changes[next])
	}
}

// run runs the EQ on n frames of b from frame at.
func (p *processor) run(b *plug.Block, at, n int) {
	left, right := b.In[0][at:at+n], b.In[0][at:at+n]
	if len(b.In) > 1 {
		right = b.In[1][at : at+n]
	}
	buf := p.scratch[:2*n]
	for i := range n {
		buf[2*i], buf[2*i+1] = left[i], right[i]
	}
	for i := 0; i < n; i += piece {
		p.insert.Process(buf[2*i : 2*min(i+piece, n)])
	}
	outL := b.Out[0][at : at+n]
	for i := range n {
		outL[i] = buf[2*i]
	}
	if len(b.Out) > 1 {
		outR := b.Out[1][at : at+n]
		for i := range n {
			outR[i] = buf[2*i+1]
		}
	}
}

// passBy copies the first two channels through unchanged, as a
// processor not yet prepared does.
func (p *processor) passBy(b *plug.Block) {
	for ch := range min(len(b.In), len(b.Out), 2) {
		copy(b.Out[ch][:b.Frames], b.In[ch][:b.Frames])
	}
	for _, c := range b.Changes {
		p.take(c)
	}
}

// take takes a change to the bypass.
func (p *processor) take(c plug.Change) {
	if c.ID != BypassID {
		return
	}
	if on := c.Value > 0.5; on != p.bypass {
		p.bypass = on
		p.eq.SetBypass(on)
	}
}

// Latency implements [plug.Processor]: the EQ is never late.
func (p *processor) Latency() int { return 0 }

// Tail implements [plug.Processor]: the EQ's ringing is too short to
// count.
func (p *processor) Tail() int { return 0 }

// Reset implements [plug.Processor]: a fresh insert, with nothing held
// from before.
func (p *processor) Reset() {
	if p.rate > 0 {
		p.insert = p.eq.InsertAt(int(p.rate))
	}
}

// isCut says whether a band of kind k cuts, and so has a slope and
// no gain.
func isCut(k audio.FilterKind) bool { return k == audio.LowCut || k == audio.HighCut }

// hasGain says whether a band of kind k lifts and lowers.
func hasGain(k audio.FilterKind) bool {
	return k == audio.Bell || k == audio.LowShelf || k == audio.HighShelf
}

// clampQ returns q within the bounds a band's Q keeps to.
func clampQ(q float64) float32 { return float32(max(minQ, min(q, maxQ))) }

// clampHz returns hz within the graph's frequencies.
func clampHz(hz float64) float32 { return float32(max(eqLow, min(hz, eqHigh))) }

// clampDB returns db within the graph's gains.
func clampDB(db float32) float32 { return max(-eqRange, min(db, eqRange)) }
