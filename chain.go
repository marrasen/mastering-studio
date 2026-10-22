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
	// Values and GoState are a Go plugin's settings, for the window to
	// show: its parameters' values, by ID, and its own state. Preset is
	// the preset it plays, where Playing says it plays one: the one
	// picked last, while its settings are as picked.
	Values  map[uint32]float64
	GoState []byte
	Preset  PresetRef
	Playing bool
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
	// The Go plugins, built in, first.
	return append(goChoices(), out...)
}

// engine is a plugin loaded, as the chain runs it: a VST3 plugin, or a
// Go plugin, through a [goEngine].
type engine interface {
	// Process changes interleaved stereo frames in place, on the
	// audio's goroutine.
	Process(frames []float32)
	Latency() int
	// Reset drops the sound held from before, and SetPosition says
	// where in the track the next frames are.
	Reset()
	SetPosition(frame int64)
	// Set sets a parameter, from 0 to 1, as VST3 carries it.
	Set(id uint32, v float64)
	State() ([]byte, error)
	SetState(b []byte) error
	// Flush, Edits, EditorOpen and OpenEditor are of an editor of the
	// plugin's own, in a window of its own.
	Flush()
	Edits() uint64
	EditorOpen() bool
	OpenEditor(title string) error
	Close()
}

// livePlugin is a slot's plugin, loaded.
type livePlugin struct {
	slot int
	p    engine
	// bypassID is the plugin's own bypass, where hasBypass says it has
	// one; without, the rack fades the plugin out for its own sound, as
	// fade keeps it, the plugin running on, so its latency stays.
	bypassID  uint32
	hasBypass bool
	bypass    bool
	fade      *bypassFade
	rate      int
	// gain is the plugin's gain, as a ratio, played in its place while
	// it is bypassed and the rack matches.
	gain float32
	// edits is the count of the editor's edits last seen.
	edits uint64
	// class is a VST3 plugin's class, for its presets.
	class *vst3.Class
	// taps, made as a view first asks, keep the sound fed into the
	// plugin and the sound that comes out, for its views.
	taps atomic.Pointer[plugTaps]
}

// plugTaps are the taps of a plugin's sound.
type plugTaps struct{ in, out *ioTap }

// listen returns the plugin's taps, made the first time it is asked.
func (lp *livePlugin) listen() *plugTaps {
	if t := lp.taps.Load(); t != nil {
		return t
	}
	lp.taps.CompareAndSwap(nil, &plugTaps{in: newIOTap(plugTapKeep), out: newIOTap(plugTapKeep)})
	return lp.taps.Load()
}

// livePlugins are the plugins loaded to play, by slot, as a plugin's
// views find the sound it plays; a copy loaded to measure or export is
// not among them.
var livePlugins sync.Map

// forget takes lp out of livePlugins, as it closes.
func (lp *livePlugin) forget() { livePlugins.CompareAndDelete(lp.slot, lp) }

// newPlugin loads slot s's plugin at rate, of state, offline or not:
// one loaded to play is among livePlugins.
func newPlugin(s Slot, state []byte, rate int, offline bool) (*livePlugin, error) {
	lp, err := loadPlugin(s, state, rate, offline)
	if err == nil {
		lp.rate = rate
	}
	if err == nil && !offline {
		livePlugins.Store(s.ID, lp)
	}
	return lp, err
}

// loadPlugin loads slot s's plugin.
func loadPlugin(s Slot, state []byte, rate int, offline bool) (*livePlugin, error) {
	if s.goPlugin() {
		return newGoPlugin(s, state, rate, offline)
	}
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
	lp := &livePlugin{slot: s.ID, p: p, edits: p.Edits(), gain: 1, class: &c}
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

// bypassFade fades a plugin without a bypass of its own out, for the
// sound it was fed, and back in: the sound fed is kept in ring, so it
// comes out as late as the plugin's own, lat frames, and the two are
// mixed by wet, gliding a frame at a time. dryGain is the gain on the
// sound fed, the plugin's gain while the rack matches levels, gliding
// too. Only the sound touches it, but for ring and lat, which the
// application makes anew as the plugin's latency changes.
type bypassFade struct {
	ring         []float32
	lat, w       int
	wet, dryGain float32
	step         float32
}

// bypassFadeMS is how long a plugin takes to fade out or in.
const bypassFadeMS = 20

// fadeFor returns the plugin's fade, made anew for its latency, lat,
// where that changed, faded out already where the plugin is bypassed.
func (lp *livePlugin) fadeFor(lat int) *bypassFade {
	if f := lp.fade; f != nil && f.lat == lat {
		return f
	}
	f := &bypassFade{ring: make([]float32, 2*(lat+stageChunk)), lat: lat, wet: 1, dryGain: 1,
		step: 1 / (bypassFadeMS / 1000.0 * float32(max(lp.rate, 1)))}
	if lp.bypass {
		f.wet = 0
	}
	lp.fade = f
	return f
}

// run runs frames through the plugin p, mixed with the sound fed, faded
// to wet as bypassed says, the sound fed at dryGain.
func (f *bypassFade) run(p engine, frames []float32, bypassed bool, dryGain float32) {
	size := len(f.ring) / 2
	n := len(frames) / 2
	for i := range n {
		f.ring[2*f.w], f.ring[2*f.w+1] = frames[2*i], frames[2*i+1]
		f.w = (f.w + 1) % size
	}
	p.Process(frames)
	wet := float32(1)
	if bypassed {
		wet = 0
	}
	if f.wet == wet && wet == 1 && f.dryGain == dryGain {
		return
	}
	for i := range n {
		if f.wet < wet {
			f.wet = min(f.wet+f.step, wet)
		} else if f.wet > wet {
			f.wet = max(f.wet-f.step, wet)
		}
		f.dryGain += (dryGain - f.dryGain) * f.step
		if math.Abs(float64(dryGain-f.dryGain)) < 1e-4 {
			f.dryGain = dryGain
		}
		j := ((f.w-n+i-f.lat)%size + size) % size
		for ch := range 2 {
			d := f.ring[2*j+ch] * f.dryGain
			frames[2*i+ch] = d + f.wet*(frames[2*i+ch]-d)
		}
	}
}

// rack holds a chain's plugins, loaded: a track's, which play, or a
// copy's, offline, for a track measured or exported.
//
// The application changes it, and the sound reads it, without waiting
// for each other. Each change makes a run, the plugins as the sound is
// to play them, and hands it over whole: the sound takes the run as it
// starts a read, and never a lock. A plugin taken out is let go only
// once every read that might hold it has finished. The reads of a rack
// come one at a time, as its stage plays in one mixer.
type rack struct {
	// mu guards the application's side: plugins, active and match,
	// and the plugins' bypasses and gains. The sound never takes it.
	mu      sync.Mutex
	plugins []*livePlugin
	active  bool
	// match plays each plugin bypassed as its gain, so the sound is as
	// loud as with it on.
	match bool
	// gen counts the changes to which plugins run, and in what order;
	// reshaped says a change made one. A bypass, a match or a gain
	// changes none.
	gen      int
	reshaped bool
	// run is what the sound plays; entered and left count the reads
	// started and finished, and holding has the reads play silence
	// while the application works the plugins as they must not
	// process.
	run           atomic.Pointer[rackRun]
	entered, left atomic.Int64
	holding       atomic.Bool
	// ran is when the plugins last processed sound, in nanoseconds
	// since ranFrom.
	ran atomic.Int64
	// fadeFrom are the plugins a swap took out, which the sound fades
	// from to the rack's own as fade runs, and which are let go once it
	// has.
	fadeFrom []*livePlugin
	fade     *chainFade
	// meters, where set, measure the sound fed into the chain, then out
	// of each plugin in turn, for how much louder each makes it. They
	// are set before the sound runs, offline.
	meters []*audio.LoudnessMeter
}

// rackRun is a rack's plugins as the sound plays them: made by a
// change, and never changed after.
type rackRun struct {
	// gen counts the changes to which plugins run, so a stage realigns
	// to them; a bypass changes none.
	gen     int
	plugins []runPlugin
	active  bool
	match   bool
	// latency is how late the run's sound comes, in frames.
	latency int
	// old are the plugins a swap took out, which the sound fades from
	// as fade runs.
	old  []runPlugin
	fade *chainFade
}

// chainFade fades the sound from one chain to another, a swap's: buf
// holds the sound fed, for the chain faded from, left is how many frames
// it has yet to run of total, and done says it is through. Only the
// sound touches it, but for done.
type chainFade struct {
	buf         []float32
	left, total int
	done        atomic.Bool
}

// swapFadeMS is how long a swap fades from one chain to the other.
const swapFadeMS = 30

func newChainFade(rate int) *chainFade {
	n := max(1, rate*swapFadeMS/1000)
	return &chainFade{buf: make([]float32, 2*stageChunk), left: n, total: n}
}

// runPlugin is a plugin as a run plays it.
type runPlugin struct {
	lp *livePlugin
	// bypassed says it is bypassed, and gain what it plays in its place
	// as the run matches; fade, for a plugin without a bypass of its
	// own, fades it.
	bypassed bool
	gain     float32
	fade     *bypassFade
}

// ranFrom is when the ran times count from.
var ranFrom = time.Now()

func newRackOf(plugins []*livePlugin) *rack {
	r := &rack{plugins: plugins, active: true, reshaped: true}
	r.publishLocked()
	return r
}

// publishLocked hands the sound a run of the rack as it is now. It
// runs with mu held, or before the rack is shared. The plugins'
// latency is asked here, on the application's side, as a VST3 plugin
// may answer it only on its own thread.
func (r *rack) publishLocked() {
	if r.reshaped {
		r.gen++
		r.reshaped = false
	}
	run := &rackRun{gen: r.gen, active: r.active, match: r.match, plugins: make([]runPlugin, len(r.plugins))}
	for i, lp := range r.plugins {
		lat := lp.p.Latency()
		run.plugins[i] = runPlugin{lp: lp, bypassed: lp.bypass, gain: lp.gain}
		if !lp.hasBypass {
			run.plugins[i].fade = lp.fadeFor(lat)
		}
		run.latency += lat
	}
	if r.fade != nil {
		run.fade = r.fade
		for _, lp := range r.fadeFrom {
			rp := runPlugin{lp: lp, bypassed: lp.bypass, gain: lp.gain}
			if !lp.hasBypass {
				rp.fade = lp.fadeFor(lp.p.Latency())
			}
			run.old = append(run.old, rp)
		}
	}
	r.run.Store(run)
}

// change runs fn with mu held, and hands the sound the rack as fn left
// it.
func (r *rack) change(fn func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn()
	r.publishLocked()
}

// settle returns once every read that started before it was called
// has finished: those after take the run handed over last.
func (r *rack) settle() {
	for e := r.entered.Load(); r.left.Load() < e; {
		time.Sleep(100 * time.Microsecond)
	}
}

// enter starts a read, and returns the run it plays; leave finishes it.
func (r *rack) enter() *rackRun {
	r.entered.Add(1)
	return r.run.Load()
}

func (r *rack) leave() { r.left.Add(1) }

// newRack loads the chain's plugins, with their states, at rate. The
// plugins that would not load are passed by, and their errors returned
// by slot.
func newRack(chain []Slot, states map[int][]byte, rate int, offline bool) (r *rack, failed map[int]error) {
	var plugins []*livePlugin
	for _, s := range chain {
		lp, err := newPlugin(s, states[s.ID], rate, offline)
		if err != nil {
			if failed == nil {
				failed = map[int]error{}
			}
			failed[s.ID] = err
			continue
		}
		plugins = append(plugins, lp)
	}
	return newRackOf(plugins), failed
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

// list returns the rack's plugins, in order.
func (r *rack) list() []*livePlugin {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.plugins)
}

// add puts lp at the end of the chain.
func (r *rack) add(lp *livePlugin) {
	r.change(func() { r.plugins, r.reshaped = append(r.plugins, lp), true })
}

// remove takes slot id's plugin out of the chain and lets it go, once
// the sound has let it go too.
func (r *rack) remove(id int) {
	var gone *livePlugin
	r.change(func() {
		r.reshaped = true
		r.plugins = slices.DeleteFunc(r.plugins, func(lp *livePlugin) bool {
			if lp.slot == id {
				gone = lp
				return true
			}
			return false
		})
	})
	if gone != nil {
		r.settle()
		gone.forget()
		gone.p.Close()
	}
}

// order puts the plugins in the order of the slots in chain.
func (r *rack) order(chain []Slot) {
	r.change(func() {
		r.reshaped = true
		at := map[int]int{}
		for k, s := range chain {
			at[s.ID] = k
		}
		slices.SortStableFunc(r.plugins, func(x, y *livePlugin) int { return at[x.slot] - at[y.slot] })
	})
}

// setBypass passes slot id's plugin by, or runs it again.
func (r *rack) setBypass(id int, on bool) {
	r.change(func() {
		for _, lp := range r.plugins {
			if lp.slot == id {
				lp.setBypass(on)
			}
		}
	})
}

// latency is how late the chain's sound comes, as last handed over.
func (r *rack) latency() int { return r.run.Load().latency }

// process runs frames through run's plugins: frames fed into the chain
// from frame at on, which a plugin's taps keep.
func (r *rack) process(run *rackRun, frames []float32, at int64) {
	if r.holding.Load() {
		clear(frames)
		return
	}
	if !run.active {
		return
	}
	r.ran.Store(int64(time.Since(ranFrom)))
	if r.meters != nil {
		r.meters[0].Write(frames)
	}
	// A swap's chain fading out hears the sound fed too.
	f := run.fade
	fading := f != nil && f.left > 0 && len(frames) <= len(f.buf)
	if fading {
		copy(f.buf, frames)
		for _, rp := range run.old {
			rp.process(f.buf[:len(frames)], run.match)
		}
	}
	for i, rp := range run.plugins {
		taps := rp.lp.taps.Load()
		if taps != nil {
			taps.in.write(at, frames)
		}
		rp.process(frames, run.match)
		if taps != nil {
			taps.out.write(at, frames)
		}
		if r.meters != nil {
			r.meters[i+1].Write(frames)
		}
	}
	if fading {
		// From the chain swapped out to the chain in, a frame at a time.
		n := len(frames) / 2
		for i := range n {
			t := float32(f.total-f.left+i) / float32(f.total)
			t = min(max(t, 0), 1)
			for ch := range 2 {
				k := 2*i + ch
				frames[k] = f.buf[k] + (frames[k]-f.buf[k])*t
			}
		}
		if f.left -= n; f.left <= 0 {
			f.left = 0
			f.done.Store(true)
		}
	}
}

// process runs frames through the plugin: faded for a bypass where it
// has none of its own, and played as its gain bypassed where the run
// matches levels.
func (rp runPlugin) process(frames []float32, match bool) {
	if rp.fade != nil {
		dryGain := float32(1)
		if match {
			dryGain = rp.gain
		}
		rp.fade.run(rp.lp.p, frames, rp.bypassed, dryGain)
		return
	}
	rp.lp.p.Process(frames)
	if rp.bypassed && match && rp.gain != 1 {
		for k := range frames {
			frames[k] *= rp.gain
		}
	}
}

// swap puts plugins in the place of the rack's own, at rate. As the
// sound plays, it fades from the ones out to the ones in, and retire
// lets the ones out go once it has; where it does not play, they go at
// once. A swap still fading lets its own go first.
func (r *rack) swap(plugins []*livePlugin, rate int) {
	r.letGoFaded(true)
	var old []*livePlugin
	r.change(func() {
		old, r.plugins, r.reshaped = r.plugins, plugins, true
		if r.active && len(old) > 0 {
			r.fadeFrom, r.fade = old, newChainFade(rate)
			old = nil
		}
	})
	r.settle()
	for _, lp := range old {
		lp.forget()
		lp.p.Close()
	}
}

// retire lets go the plugins a swap took out, once the sound has faded
// from them, or at once where it no longer plays.
func (r *rack) retire() { r.letGoFaded(false) }

// letGoFaded lets go the plugins a swap took out: once faded from, or,
// now, at once.
func (r *rack) letGoFaded(now bool) {
	r.mu.Lock()
	f, active := r.fade, r.active
	r.mu.Unlock()
	if f == nil || !now && active && !f.done.Load() {
		return
	}
	var old []*livePlugin
	r.change(func() { old, r.fadeFrom, r.fade = r.fadeFrom, nil, nil })
	r.settle()
	for _, lp := range old {
		lp.forget()
		lp.p.Close()
	}
}

// setMatch plays the plugins bypassed as their gains, or not.
func (r *rack) setMatch(on bool) { r.change(func() { r.match = on }) }

// setGains sets the plugins' gains, in LU, by slot.
func (r *rack) setGains(gains map[int]float32) {
	r.change(func() {
		for _, lp := range r.plugins {
			if g, ok := gains[lp.slot]; ok {
				lp.gain = float32(math.Pow(10, float64(g)/20))
			}
		}
	})
}

// measureSteps has the rack measure how much louder each plugin makes
// the sound, at rate. It is called before the sound runs.
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
	for i, lp := range r.list() {
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
	for i, lp := range r.list() {
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
		r.active, r.reshaped = on, true
		r.publishLocked()
	}
}

// isActive says the chain's plugins run.
func (r *rack) isActive() bool { return r.run.Load().active }

// flush passes the changes of the plugins' editors on to the plugins
// themselves, so their states hold them while no sound runs: while it
// does, the sound carries them. As a plugin flushes it must not
// process, so a read that comes meanwhile plays silence.
func (r *rack) flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(ranFrom)-time.Duration(r.ran.Load()) < 100*time.Millisecond {
		return
	}
	r.holding.Store(true)
	r.settle()
	for _, lp := range r.plugins {
		lp.p.Flush()
	}
	r.holding.Store(false)
}

// changed hands the sound the rack again, as a plugin's latency changed.
func (r *rack) changed() { r.change(func() {}) }

// close lets the plugins go, once the sound has.
func (r *rack) close() {
	r.letGoFaded(true)
	var ps []*livePlugin
	r.change(func() { ps, r.plugins = r.plugins, nil })
	r.settle()
	for _, lp := range ps {
		lp.forget()
		lp.p.Close()
	}
}

// chainStage runs a sound through a rack, as late as the rack's plugins make
// it, made up for: from a seek, the plugins run ahead over their
// latency, and once the sound ends they run on over it, so the chainStage's
// frame f is the sound's frame f, processed.
//
// A read works in chunks of stageChunk frames at most, in buffers made
// with the stage, so the sound makes nothing as it plays.
type chainStage struct {
	in audio.Seeker
	r  *rack
	// gen is the rack's run the stage is aligned to, or -1 to align
	// before the next read, lat its latency, skip the frames to run
	// ahead, tail the silence to run on, and at the frame next.
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
	// ratio, in bits; delay holds the sound fed over the latency, in
	// delayRoom; dry is how far the sound played is the one fed,
	// gliding there.
	bypass    *atomic.Bool
	inGain    atomic.Uint32
	delay     []float32
	delayRoom []float32
	at0       int
	dry       float32
	dryBuf    []float32
	// dryMeter, where set, measures the sound as fed, its gain in taken
	// back out: the mix's loudness, for levels matched while bypassed.
	dryMeter *audio.LoudnessMeter
	dryCopy  []float32
}

// stageChunk is the most frames a stage's read works at once: as many
// as a resampler reads.
const stageChunk = 1024

// maxBypassDelay is the most frames of latency bypassing makes up
// for: a chain later than that is compared a little out of time.
const maxBypassDelay = 1 << 16

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
	s := &chainStage{in: in, r: r, gen: -1, buf: make([]float32, 2*512), delayRoom: make([]float32, 2*maxBypassDelay),
		dryBuf: make([]float32, 2*stageChunk), dryCopy: make([]float32, 2*stageChunk)}
	s.setOut(outDB)
	s.setIn(0)
	s.setLevel(1)
	s.out, s.level = math.Float32frombits(s.outTo.Load()), 1
	return s
}

// Len implements [audio.Seeker].
func (s *chainStage) Len() int64 { return s.in.Len() }

// SeekFrame implements [audio.Seeker]: the plugins align to the frame
// at the next read.
func (s *chainStage) SeekFrame(f int64) error {
	s.at, s.gen = f, -1
	return s.in.SeekFrame(f)
}

// align runs the plugins of run ahead again from frame f.
func (s *chainStage) align(run *rackRun, f int64) {
	lat := run.latency
	s.gen, s.lat, s.skip, s.tail, s.ended, s.at, s.fed = run.gen, lat, lat, lat, false, f, f
	s.delay, s.at0 = s.delayRoom[:2*min(lat, maxBypassDelay)], 0
	clear(s.delay)
	for _, rp := range run.plugins {
		rp.lp.p.SetPosition(f)
	}
}

// Read implements [audio.Source]: stageChunk frames at most.
func (s *chainStage) Read(dst []float32) (int, error) {
	run := s.r.enter()
	defer s.r.leave()
	// The plugins changed, or their latency did, or the sound moved:
	// the stage runs ahead again from where it is.
	if s.gen != run.gen || s.lat != run.latency {
		if s.gen != -1 {
			// The sound fed in runs ahead of what comes out: back to
			// where it comes out.
			_ = s.in.SeekFrame(s.at)
		}
		s.align(run, s.at)
	}
	for s.skip > 0 {
		k := s.fill(s.buf[:2*min(s.skip, len(s.buf)/2)])
		if k == 0 {
			if s.ended {
				s.skip = 0
			}
			break
		}
		s.tapIn(s.buf[:2*k])
		dry := s.dryBuf[:2*k]
		copy(dry, s.buf[:2*k])
		s.delayed(dry)
		s.r.process(run, s.buf[:2*k], s.fed-int64(k))
		s.skip -= k
	}
	if s.skip > 0 {
		// Still running ahead, waiting for the sound fed in.
		return 0, nil
	}
	dst = dst[:2*min(len(dst)/2, stageChunk)]
	n := s.fill(dst)
	if n == 0 {
		if s.ended {
			return 0, io.EOF
		}
		return 0, nil
	}
	s.tapIn(dst[:2*n])
	// The sound as fed, the latency later, for bypassing.
	dry := s.dryBuf[:2*n]
	copy(dry, dst[:2*n])
	s.delayed(dry)
	s.r.process(run, dst[:2*n], s.fed-int64(n))
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

// tapIn gives the tap the frames fed in, and counts them.
func (s *chainStage) tapIn(frames []float32) {
	if s.tap != nil {
		s.tap.write(s.fed, frames)
	}
	if s.dryMeter != nil {
		back := 1 / max(math.Float32frombits(s.inGain.Load()), 1e-6)
		c := s.dryCopy[:len(frames)]
		for i, v := range frames {
			c[i] = v * back
		}
		s.dryMeter.Write(c)
	}
	s.fed += int64(len(frames) / 2)
}

// ioTap holds the last seconds of the sound fed into a chain, each
// frame with the frame of the sound it is, for the input's meters to
// read as the sound comes out to be heard. One goroutine writes and
// others read, none waiting: a frame read as it is written over is
// left out.
type ioTap struct {
	// samples holds frames, in bits, and frames which frame of the
	// sound each is, -1 while it is written.
	samples []atomic.Uint32
	frames  []atomic.Int64
}

// tapKeep is how many frames the input's tap keeps, and plugTapKeep a
// plugin's, for its views.
const (
	tapKeep     = 3 * 48000
	plugTapKeep = 1 << 16
)

// newIOTap returns a tap that keeps the last keep frames.
func newIOTap(keep int) *ioTap {
	t := &ioTap{samples: make([]atomic.Uint32, 2*keep), frames: make([]atomic.Int64, keep)}
	for i := range t.frames {
		t.frames[i].Store(-1)
	}
	return t
}

func (t *ioTap) write(at int64, frames []float32) {
	for i := 0; i+1 < len(frames); i += 2 {
		f := at + int64(i/2)
		k := int(f % int64(len(t.frames)))
		t.frames[k].Store(-1)
		t.samples[2*k].Store(math.Float32bits(frames[i]))
		t.samples[2*k+1].Store(math.Float32bits(frames[i+1]))
		t.frames[k].Store(f)
	}
}

// read appends to dst the frames fed from frame from up to to.
func (t *ioTap) read(dst []float32, from, to int64) []float32 {
	for f := max(from, 0); f < to; f++ {
		k := int(f % int64(len(t.frames)))
		if t.frames[k].Load() != f {
			continue
		}
		l, r := t.samples[2*k].Load(), t.samples[2*k+1].Load()
		if t.frames[k].Load() != f {
			continue
		}
		dst = append(dst, math.Float32frombits(l), math.Float32frombits(r))
	}
	return dst
}

// fill reads the sound into dst, then the silence after it.
func (s *chainStage) fill(dst []float32) int {
	n := 0
	if !s.ended {
		k, err := s.in.Read(dst)
		n = k
		if err != nil {
			s.ended = true
		}
		if k == 0 && err == nil {
			// Nothing yet, as a stream not decoded this far: no
			// silence after it either, yet.
			return 0
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
