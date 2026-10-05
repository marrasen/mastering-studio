package main

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audio/vst3"
)

// Slot is a plugin in a track's chain, which the track's sound runs
// through in order, after its cut and fades.
type Slot struct {
	ID int
	// Path is the plugin's bundle, and Class its effect, as
	// [vst3.Class.IDString] writes it.
	Path, Class  string
	Name, Vendor string
	// Label is what the slot is called, where it is named apart from
	// its plugin, as one of two of the same plugin.
	Label string
	// Bypass passes the sound by the plugin.
	Bypass bool
	// Gain is how much louder the plugin makes the sound, in LU, as last
	// measured while it ran, where Gained says it was: while levels are
	// matched, a plugin bypassed is played as this gain in its place.
	Gain   float32
	Gained bool
	// LRA is the loudness range of the sound out of the plugin, in LU,
	// as last measured, where Ranged says it has one.
	LRA    float32
	Ranged bool
	// Latency is how late the plugin's sound comes, in frames, once it
	// is loaded; Open says its editor is open, and Failed why it would
	// not load.
	Latency int
	Open    bool
	Failed  string
}

// PluginChoice is an effect this computer has, to add to a chain.
type PluginChoice struct {
	Path, Class  string
	Name, Vendor string
	// Kind is what it does, as its maker files it: "Fx|EQ".
	Kind string
}

// modules are the plugins' modules loaded, by path: loaded once, kept
// while the program runs, as a plugin's library is seldom safe to
// unload.
var (
	modulesMu sync.Mutex
	modules   = map[string]*vst3.Module{}
)

func loadModule(path string) (*vst3.Module, error) {
	modulesMu.Lock()
	defer modulesMu.Unlock()
	if m := modules[path]; m != nil {
		return m, nil
	}
	m, err := vst3.Open(path)
	if err != nil {
		return nil, err
	}
	modules[path] = m
	return m, nil
}

// closeModules unloads every module loaded. Nothing made from them may
// be at work.
func closeModules() {
	modulesMu.Lock()
	defer modulesMu.Unlock()
	for path, m := range modules {
		_ = m.Close()
		delete(modules, path)
	}
}

// scanPlugins lists the effects in the system's folders and dirs: from
// what their bundles list, or, where a bundle lists none, loading it.
func scanPlugins(dirs []string) []PluginChoice {
	var out []PluginChoice
	for _, b := range vst3.Scan(append(vst3.Dirs(), dirs...)...) {
		effects := b.Effects
		if effects == nil {
			m, err := loadModule(b.Path)
			if err != nil {
				continue
			}
			effects = m.Effects()
		}
		for _, c := range effects {
			out = append(out, PluginChoice{Path: b.Path, Class: c.IDString(), Name: c.Name, Vendor: c.Vendor,
				Kind: c.SubCategories})
		}
	}
	slices.SortFunc(out, func(a, b PluginChoice) int {
		if c := strings.Compare(strings.ToLower(a.Vendor), strings.ToLower(b.Vendor)); c != 0 {
			return c
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

// livePlugin is a slot's plugin, loaded.
type livePlugin struct {
	slot int
	p    *vst3.Plugin
	// bypassID is the plugin's own bypass, where hasBypass says it has
	// one; without, a bypassed plugin is passed by, its latency with it.
	bypassID  uint32
	hasBypass bool
	bypass    bool
	// gain is the plugin's gain, as a ratio, played in its place while
	// it is bypassed and the rack matches.
	gain float32
	// edits is the count of the editor's edits last seen.
	edits uint64
}

// rack holds a chain's plugins, loaded: a track's, which play, or a
// copy's, offline, for a track measured or exported.
type rack struct {
	mu      sync.Mutex
	plugins []*livePlugin
	active  bool
	// gen counts the changes to the plugins, so a stage realigns to
	// their latency.
	gen int
	// ran is when the plugins last processed sound.
	ran time.Time
	// match plays each plugin bypassed as its gain, so the sound is as
	// loud as with it on.
	match bool
	// meters, where set, measure the sound fed into the chain, then out
	// of each plugin in turn, for how much louder each makes it.
	meters []*audio.LoudnessMeter
}

// newPlugin loads slot s's plugin at rate, of state, offline or not.
func newPlugin(s Slot, state []byte, rate int, offline bool) (*livePlugin, error) {
	m, err := loadModule(s.Path)
	if err != nil {
		return nil, err
	}
	c, ok := m.Find(s.Class)
	if !ok {
		return nil, fmt.Errorf("%s: no %s in %s", s.Name, s.Class, s.Path)
	}
	p, err := m.New(c, vst3.Config{Rate: rate, Block: 512, Offline: offline})
	if err != nil {
		return nil, err
	}
	if len(state) > 0 {
		if err := p.SetState(state); err != nil {
			p.Close()
			return nil, err
		}
	}
	// Its latency, as some plugins tell it only once they process.
	p.Prime(100 * time.Millisecond)
	lp := &livePlugin{slot: s.ID, p: p, edits: p.Edits(), gain: 1}
	if s.Gained {
		lp.gain = float32(math.Pow(10, float64(s.Gain)/20))
	}
	lp.bypassID, lp.hasBypass = p.Bypass()
	lp.setBypass(s.Bypass)
	return lp, nil
}

func (lp *livePlugin) setBypass(on bool) {
	lp.bypass = on
	if lp.hasBypass {
		v := 0.0
		if on {
			v = 1
		}
		lp.p.Set(lp.bypassID, v)
	}
}

// passed says the plugin is passed by, not run.
func (lp *livePlugin) passed() bool { return lp.bypass && !lp.hasBypass }

// newRack loads the chain's plugins, with their states, at rate. The
// plugins that would not load are passed by, and their errors returned
// by slot.
func newRack(chain []Slot, states map[int][]byte, rate int, offline bool) (r *rack, failed map[int]error) {
	r = &rack{active: true}
	for _, s := range chain {
		lp, err := newPlugin(s, states[s.ID], rate, offline)
		if err != nil {
			if failed == nil {
				failed = map[int]error{}
			}
			failed[s.ID] = err
			continue
		}
		r.plugins = append(r.plugins, lp)
	}
	return r, failed
}

// offlineRack loads a copy of the chain to measure or export with, or
// fails where a plugin will not load.
func offlineRack(chain []Slot, states map[int][]byte, rate int) (*rack, error) {
	r, failed := newRack(chain, states, rate, true)
	for _, s := range chain {
		if err := failed[s.ID]; err != nil {
			r.close()
			return nil, fmt.Errorf("%s: %w", s.Name, err)
		}
	}
	return r, nil
}

// find returns slot id's plugin, or nil.
func (r *rack) find(id int) *livePlugin {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, lp := range r.plugins {
		if lp.slot == id {
			return lp
		}
	}
	return nil
}

// latencyLocked is how late the chain's sound comes. It runs with mu
// held.
func (r *rack) latencyLocked() int {
	n := 0
	for _, lp := range r.plugins {
		if !lp.passed() {
			n += lp.p.Latency()
		}
	}
	return n
}

// processLocked runs frames through the chain. It runs with mu held.
func (r *rack) processLocked(frames []float32) {
	if !r.active {
		return
	}
	r.ran = time.Now()
	if r.meters != nil {
		r.meters[0].Write(frames)
	}
	for i, lp := range r.plugins {
		if !lp.passed() {
			lp.p.Process(frames)
		}
		if lp.bypass && r.match && lp.gain != 1 {
			for k := range frames {
				frames[k] *= lp.gain
			}
		}
		if r.meters != nil {
			r.meters[i+1].Write(frames)
		}
	}
}

// setMatch plays the plugins bypassed as their gains, or not.
func (r *rack) setMatch(on bool) {
	r.mu.Lock()
	r.match = on
	r.mu.Unlock()
}

// setGains sets the plugins' gains, in LU, by slot.
func (r *rack) setGains(gains map[int]float32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, lp := range r.plugins {
		if g, ok := gains[lp.slot]; ok {
			lp.gain = float32(math.Pow(10, float64(g)/20))
		}
	}
}

// measureSteps has the rack measure how much louder each plugin makes
// the sound, at rate.
func (r *rack) measureSteps(rate int) {
	r.meters = make([]*audio.LoudnessMeter, len(r.plugins)+1)
	for i := range r.meters {
		r.meters[i] = audio.NewLoudnessMeter(rate)
	}
}

// ranges returns the loudness range of the sound fed into the chain,
// and of the sound out of each plugin, by slot, as the rack measured
// them; ok says the sound fed in has one.
func (r *rack) ranges() (in float32, ok bool, out map[int]float32) {
	if r.meters == nil {
		return 0, false, nil
	}
	lra := func(m *audio.LoudnessMeter) (float32, bool) {
		lo, hi, ranged := audio.LoudnessRange(m.ShortTerms())
		return float32(hi - lo), ranged
	}
	in, ok = lra(r.meters[0])
	out = map[int]float32{}
	for i, lp := range r.plugins {
		if v, ranged := lra(r.meters[i+1]); ranged {
			out[lp.slot] = v
		}
	}
	return in, ok, out
}

// steps returns how much louder each plugin that ran made the sound, in
// LU, by slot, as the rack measured it.
func (r *rack) steps() map[int]float32 {
	if r.meters == nil {
		return nil
	}
	out := map[int]float32{}
	for i, lp := range r.plugins {
		before, ok := r.meters[i].Integrated()
		after, ok2 := r.meters[i+1].Integrated()
		if !lp.bypass && ok && ok2 {
			out[lp.slot] = float32(after - before)
		}
	}
	return out
}

// setActive runs the chain's plugins or holds them: only the track
// heard has them running. A plugin held takes no time of the
// computer's; once it runs again, the stage runs it ahead over its
// latency, which clears out what it held from before.
func (r *rack) setActive(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != on {
		r.active = on
		r.gen++
	}
}

// flush passes the changes of the plugins' editors on to the plugins
// themselves, so their states hold them while no sound runs: while it
// does, the sound carries them.
func (r *rack) flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.ran) < 100*time.Millisecond {
		return
	}
	for _, lp := range r.plugins {
		lp.p.Flush()
	}
}

// changed tells the stages reading the rack its plugins changed.
func (r *rack) changed() {
	r.mu.Lock()
	r.gen++
	r.mu.Unlock()
}

// close lets the plugins go.
func (r *rack) close() {
	r.mu.Lock()
	ps := r.plugins
	r.plugins = nil
	r.gen++
	r.mu.Unlock()
	for _, lp := range ps {
		lp.p.Close()
	}
}

// chainStage runs a sound through a rack, as late as the rack's plugins make
// it, made up for: from a seek, the plugins run ahead over their
// latency, and once the sound ends they run on over it, so the chainStage's
// frame f is the sound's frame f, processed.
type chainStage struct {
	in audio.Seeker
	r  *rack
	// gen is the rack's changes the stage is aligned to, lat their
	// latency, skip the frames to run ahead, tail the silence to run on,
	// and at the frame next.
	gen, lat   int
	skip, tail int
	ended      bool
	at         int64
	buf        []float32
	// fed is the frame of the sound fed in next, which runs ahead of
	// at by the latency; tap, where set, takes what is fed, for the
	// input's meters.
	fed int64
	tap *ioTap
	// out is the gain after the chain, as a ratio, and outTo where it
	// goes, in bits, as the editor sets it while the sound plays: it
	// glides there over a block, so a change makes no click.
	out   float32
	outTo atomic.Uint32
	// level is the gain that matches the track's level to the target,
	// as a ratio, and levelTo where it goes, in bits, gliding there over
	// a block: each track carries its own, so a turn into the next takes
	// up its gain on the very sample.
	level   float32
	levelTo atomic.Uint32
	// bypass, where set, has the stage play the sound as fed, the
	// chain's latency later, its gain in taken back out and no gain
	// after: the mix as it came, to compare. inGain is the gain in, as a
	// ratio, in bits; delay holds the sound fed over the latency; dry is
	// how far the sound played is the one fed, gliding there.
	bypass *atomic.Bool
	inGain atomic.Uint32
	// dryMeter, where set, measures the sound as fed, its gain in taken
	// back out: the mix's loudness, for levels matched while bypassed.
	dryMeter *audio.LoudnessMeter
	dryCopy  []float32
	delay    []float32
	at0      int
	dry      float32
	dryBuf   []float32
}

// setIn sets the gain in, in decibels, which bypassing takes back out.
func (s *chainStage) setIn(db float32) {
	s.inGain.Store(math.Float32bits(float32(math.Pow(10, float64(db)/20))))
}

// delayed passes frames through the delay of the chain's latency: each
// comes out as the one fed that long before.
func (s *chainStage) delayed(frames []float32) {
	n := len(s.delay) / 2
	if n == 0 {
		return
	}
	for i := 0; i+1 < len(frames); i += 2 {
		j := 2 * s.at0
		frames[i], s.delay[j] = s.delay[j], frames[i]
		frames[i+1], s.delay[j+1] = s.delay[j+1], frames[i+1]
		s.at0 = (s.at0 + 1) % n
	}
}

// setOut sets the gain after the chain, in decibels.
func (s *chainStage) setOut(db float32) {
	s.outTo.Store(math.Float32bits(float32(math.Pow(10, float64(db)/20))))
}

// setLevel sets the gain that matches the track's level, as a ratio.
func (s *chainStage) setLevel(g float32) { s.levelTo.Store(math.Float32bits(g)) }

func newStage(in audio.Seeker, r *rack, outDB float32) *chainStage {
	s := &chainStage{in: in, r: r, buf: make([]float32, 2*512)}
	s.setOut(outDB)
	s.setIn(0)
	s.setLevel(1)
	s.out, s.level = math.Float32frombits(s.outTo.Load()), 1
	r.mu.Lock()
	_ = s.alignLocked(0)
	r.mu.Unlock()
	return s
}

// Len implements [audio.Seeker].
func (s *chainStage) Len() int64 { return s.in.Len() }

// SeekFrame implements [audio.Seeker].
func (s *chainStage) SeekFrame(f int64) error {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	return s.alignLocked(f)
}

func (s *chainStage) alignLocked(f int64) error {
	lat := s.r.latencyLocked()
	s.gen, s.lat, s.skip, s.tail, s.ended, s.at, s.fed = s.r.gen, lat, lat, lat, false, f, f
	s.delay, s.at0 = make([]float32, 2*lat), 0
	for _, lp := range s.r.plugins {
		lp.p.SetPosition(f)
	}
	return s.in.SeekFrame(f)
}

// Read implements [audio.Source].
func (s *chainStage) Read(dst []float32) (int, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	// The plugins changed, or a plugin's latency did: the stage runs
	// ahead again from where it is.
	if s.gen != s.r.gen || s.lat != s.r.latencyLocked() {
		_ = s.alignLocked(s.at)
	}
	for s.skip > 0 {
		k := s.fill(s.buf[:2*min(s.skip, len(s.buf)/2)])
		if k == 0 {
			s.skip = 0
			break
		}
		s.tapLocked(s.buf[:2*k])
		s.dryBuf = append(s.dryBuf[:0], s.buf[:2*k]...)
		s.delayed(s.dryBuf)
		s.r.processLocked(s.buf[:2*k])
		s.skip -= k
	}
	n := s.fill(dst)
	if n == 0 {
		return 0, io.EOF
	}
	s.tapLocked(dst[:2*n])
	// The sound as fed, the latency later, for bypassing.
	s.dryBuf = append(s.dryBuf[:0], dst[:2*n]...)
	dry := s.dryBuf
	s.delayed(dry)
	s.r.processLocked(dst[:2*n])
	// The gain after the chain, gliding to where it is set.
	to := math.Float32frombits(s.outTo.Load())
	if from := s.out; from != to || to != 1 {
		for i := range n {
			g := from + (to-from)*float32(i+1)/float32(n)
			dst[2*i] *= g
			dst[2*i+1] *= g
		}
		s.out = to
	}
	// Bypassed, the sound as fed, its gain in taken back out, gliding
	// from the one to the other over the block.
	wantDry := float32(0)
	if s.bypass != nil && s.bypass.Load() {
		wantDry = 1
	}
	if from := s.dry; from != 0 || wantDry != 0 {
		back := 1 / max(math.Float32frombits(s.inGain.Load()), 1e-6)
		for i := range n {
			m := from + (wantDry-from)*float32(i+1)/float32(n)
			dst[2*i] = dst[2*i]*(1-m) + dry[2*i]*back*m
			dst[2*i+1] = dst[2*i+1]*(1-m) + dry[2*i+1]*back*m
		}
		s.dry = wantDry
	}
	// The gain that matches levels, gliding to where it is set.
	if from, to := s.level, math.Float32frombits(s.levelTo.Load()); from != to || to != 1 {
		for i := range n {
			g := from + (to-from)*float32(i+1)/float32(n)
			dst[2*i] *= g
			dst[2*i+1] *= g
		}
		s.level = to
	}
	s.at += int64(n)
	return n, nil
}

// tapLocked gives the tap the frames fed in, and counts them.
func (s *chainStage) tapLocked(frames []float32) {
	if s.tap != nil {
		s.tap.write(s.fed, frames)
	}
	if s.dryMeter != nil {
		back := 1 / max(math.Float32frombits(s.inGain.Load()), 1e-6)
		s.dryCopy = append(s.dryCopy[:0], frames...)
		for i := range s.dryCopy {
			s.dryCopy[i] *= back
		}
		s.dryMeter.Write(s.dryCopy)
	}
	s.fed += int64(len(frames) / 2)
}

// ioTap holds the last seconds of the sound fed into a chain, each
// stretch at the frame it was fed from, for the input's meters to read
// as the sound comes out to be heard.
type ioTap struct {
	mu     sync.Mutex
	chunks []tapChunk
	kept   int
}

type tapChunk struct {
	at     int64
	frames []float32
}

// tapKeep is how many frames a tap keeps.
const tapKeep = 3 * 48000

func (t *ioTap) write(at int64, frames []float32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.chunks = append(t.chunks, tapChunk{at, slices.Clone(frames)})
	t.kept += len(frames) / 2
	for t.kept > tapKeep && len(t.chunks) > 1 {
		t.kept -= len(t.chunks[0].frames) / 2
		t.chunks = t.chunks[1:]
	}
}

// read appends to dst the frames fed from frame from up to to.
func (t *ioTap) read(dst []float32, from, to int64) []float32 {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range t.chunks {
		n := int64(len(c.frames) / 2)
		lo, hi := max(from, c.at), min(to, c.at+n)
		if hi > lo {
			dst = append(dst, c.frames[2*(lo-c.at):2*(hi-c.at)]...)
		}
	}
	return dst
}

// fill reads the sound into dst, then the silence after it.
func (s *chainStage) fill(dst []float32) int {
	n := 0
	if !s.ended {
		k, err := s.in.Read(dst)
		n = k
		if err != nil || k == 0 {
			s.ended = true
		}
	}
	if s.ended && n < len(dst)/2 && s.tail > 0 {
		k := min(len(dst)/2-n, s.tail)
		clear(dst[2*n : 2*(n+k)])
		s.tail -= k
		n += k
	}
	return n
}
