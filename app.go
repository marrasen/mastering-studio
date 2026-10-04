package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/driver"
)

// The vocabulary the two halves share.
type (
	// Album is what the window shows.
	Album struct {
		// Release is what the album is released as, as its exports are
		// tagged.
		Release
		// Tracks are the album's tracks, in order.
		Tracks []Track
		// Gap is the silence before every track.
		Gap time.Duration
		// Target is the loudness the album aims at, in LUFS, which the
		// tracks' readings are held against, and levels are matched to.
		Target float32
		// Bits and Dither are how tracks are exported, and ExportDir
		// where.
		Bits      int
		Dither    bool
		ExportDir string
		// ExportWAV and ExportMP3 say which files an export writes, the
		// MP3 at MP3Rate kbps.
		ExportWAV, ExportMP3 bool
		MP3Rate              int
		// Current is the track picked, which plays; Playing says it
		// does, and Starts counts the starts, so the window can tell a
		// new play from one going on.
		Current int
		Playing bool
		Starts  int
		// Match plays every track at the target loudness, to compare
		// them on their sound alone; Volume is the listening level.
		Match  bool
		Volume float32
		// AlbumPlay plays on from each track into the next, without a
		// gap, as the album's files play one after another.
		AlbumPlay bool
		// Follow is how the editor follows the playhead, View how it shows
		// the track, and Curves the loudness curves it draws over it.
		Follow Follow
		View   View
		Curves uint8
		// Exporting says an export is running.
		Exporting bool
		// Note says what went wrong last, for the window to show.
		Note string
		// Loudness is the album's, every track measured together, as
		// bs1770gain measures an album, once every track is measured.
		Loudness Measure
		// Plugins are the effects this computer has, once found, which
		// Scanning says is under way; Recent are those added last, the
		// latest first.
		Recent  []PluginChoice
		Plugins []PluginChoice
		// AlbumFile is the file the album is kept in, AlbumName what it
		// is called, and RecentAlbums the albums opened last, the latest
		// first.
		AlbumFile    string
		AlbumName    string
		RecentAlbums []string
		Scanning     bool
	}
	// Track is one of the album's tracks.
	Track struct {
		ID    int
		Title string
		File  string
		Edit  Edit
		// Note is a note on the track, as what it still needs.
		Note string
		// Silence, where set, is the silence before the track in place of
		// the album's.
		Silence *time.Duration
		// Seq is the window's count of edits taken, so it can tell its
		// own edit coming back from an older one.
		Seq int
		// Chain is the plugins the track runs through, after its edit.
		Chain []Slot
		// Scanned says the file has been read through: its format,
		// length, waveform and where its sound starts and ends.
		Scanned    bool
		Format     audio.Format
		Frames     int64
		Wave       *Wave
		SoundStart time.Duration
		SoundEnd   time.Duration
		// Measure is the track measured as rendered, which Measuring
		// says is under way for the edit as it is now.
		Measure   Measure
		Measured  bool
		Measuring bool
		// Stale says the track changed since it was last measured, to be
		// measured again with CalcLoudness.
		Stale bool
		// Matching says its gain is being found to bring it to the
		// target.
		Matching bool
		// Progress is how far its export is, from 0 to 1, while it is
		// exported, and Exported where it went once it is, with Out its
		// reading as written.
		Progress float32
		Exported string
		Out      Measure
	}

	// AddFiles adds files to the album, at its end.
	AddFiles struct{ Paths []string }
	// ChooseFiles asks, with the system's dialog, for files to add.
	ChooseFiles struct{}
	// RemoveTrack takes a track off the album.
	RemoveTrack struct{ ID int }
	// MoveTrack moves the track at From to To.
	MoveTrack struct{ From, To int }
	// RenameTrack renames a track, as its file is named on export.
	RenameTrack struct {
		ID    int
		Title string
	}
	// Pick picks a track: while one plays, it plays on from the same
	// moment, so the ear compares the two.
	Pick struct{ ID int }
	// SetEdit sets a track's edit, often, as its handles are dragged.
	SetEdit struct {
		ID   int
		Edit Edit
		Seq  int
	}
	// TogglePlay plays or pauses the track picked.
	TogglePlay struct{}
	// PlayFromStart plays the track picked from its start, the silence
	// before it and all.
	PlayFromStart struct{}
	// SeekTo moves the track playing.
	SeekTo struct{ At time.Duration }
	// SetGap sets the silence before every track.
	SetGap struct{ Gap time.Duration }
	// SetTarget sets the album's target loudness.
	SetTarget struct{ LUFS float32 }
	// SetMatch turns level matching on or off.
	SetMatch struct{ On bool }
	// SetVolume sets the listening level.
	SetVolume struct{ Volume float32 }
	// SetExport sets how tracks are exported.
	SetExport struct {
		Bits   int
		Dither bool
	}
	// ChooseExportDir asks, with the system's dialog, where to export.
	ChooseExportDir struct{}
	// ReplaceFile gives a track another file, as a new mix of it, its
	// title, edit and chain kept.
	ReplaceFile struct {
		ID   int
		Path string
	}
	// ChooseReplacement asks, with the system's dialog, for a track's
	// new file.
	ChooseReplacement struct{ ID int }
	// ShowFile shows a track's file in the system's file manager.
	ShowFile struct{ ID int }
	// SetAlbumPlay turns playing on through the album on or off.
	SetAlbumPlay struct{ On bool }
	// SetFollow sets how the editor follows the playhead.
	SetFollow struct{ Follow Follow }
	// SetView sets how the editor shows the track.
	SetView struct{ View View }
	// SetCurves sets the loudness curves the editor draws.
	SetCurves struct{ Curves uint8 }
	// SetSilence sets the silence before a track, or, nil, gives it the
	// album's.
	SetSilence struct {
		ID      int
		Silence *time.Duration
	}
	// SetNote sets a track's note.
	SetNote struct {
		ID   int
		Note string
	}
	// Export exports tracks: those named, or every one.
	Export struct{ IDs []int }
	// CalcLoudness measures every track changed since it was last
	// measured.
	CalcLoudness struct{}
	// MatchTarget sets a track's gain so it measures at the target.
	MatchTarget struct{ ID int }
	// NewAlbum asks where to make a new album, and opens it, empty.
	NewAlbum struct{}
	// OpenAlbum asks for an album to open.
	OpenAlbum struct{}
	// OpenAlbumPath opens the album at Path.
	OpenAlbumPath struct{ Path string }
	// SaveAlbumAs asks where to keep the album from now on.
	SaveAlbumAs struct{}

	// AddPlugin adds an effect to the end of a track's chain.
	AddPlugin struct {
		Track  int
		Choice PluginChoice
	}
	// RemovePlugin takes a plugin out of a track's chain.
	RemovePlugin struct{ Track, Slot int }
	// MovePlugin moves the plugin at From in a track's chain to To.
	MovePlugin struct{ Track, From, To int }
	// SetBypass passes a plugin by, or runs it again.
	SetBypass struct {
		Track, Slot int
		On          bool
	}
	// ShowEditor opens a plugin's editor.
	ShowEditor struct{ Track, Slot int }
	// CopyChain gives the tracks To a copy of track From's chain, every
	// plugin as it is set; To empty means every other track.
	CopyChain struct {
		From int
		To   []int
	}
)

// albumTopic is what the window watches.
const albumTopic = "album"

// project is the album as kept between runs.
type project struct {
	Release
	Tracks    []keptTrack
	Gap       time.Duration
	Target    float32
	Bits      int
	Dither    bool
	ExportDir string
	// ExportAt is the export folder whole, as it was last saved, where
	// ExportDir is from the album's folder.
	ExportAt string `json:",omitempty"`
	// ExportWAV and ExportMP3 say which files an export writes; nil is
	// a WAV, as before either was kept.
	ExportWAV *bool `json:",omitempty"`
	ExportMP3 bool  `json:",omitempty"`
	MP3Rate   int   `json:",omitempty"`
	Volume    float32
	Match     bool
	Current   int
	AlbumPlay bool
	Follow    Follow
	View      View   `json:",omitempty"`
	Curves    *uint8 `json:",omitempty"`
}

type keptTrack struct {
	Title, File string
	Note        string         `json:",omitempty"`
	Silence     *time.Duration `json:",omitempty"`
	Edit        Edit
	Chain       []keptSlot `json:",omitempty"`
	// At is File whole, as it was last saved, where File is from the
	// album's folder: the album opens on the computer it was saved on
	// even where the folders do not move together.
	At string `json:",omitempty"`
	// Measure is the track as last measured, of its file as it was then,
	// and Stale says it changed since.
	Measure *keptMeasure `json:",omitempty"`
	Stale   bool         `json:",omitempty"`
}

// keptSlot is a plugin of a chain, kept with its state.
type keptSlot struct {
	Path, Class  string
	Name, Vendor string
	Bypass       bool
	State        []byte
}

// app is the application half.
type app struct {
	Album
	ctx context.Context
	// stop ends ctx, as the program ends, and work counts what runs in
	// the background with plugins, to wait for before they are let go.
	stop context.CancelFunc
	work sync.WaitGroup
	d    *deck
	file string
	ids  int
	// scans and measures carry what the background tells; measuring
	// holds how to stop each track's measuring, and settle when it is
	// due once its edits have paused.
	scans    chan scanned
	measures chan measured
	measurer map[int]context.CancelFunc
	settle   map[int]time.Time
	// choose shows the system's dialog; chosen carries what was chosen.
	choose func(driver.ChooseOptions) ([]string, error)
	chosen chan []string
	dirs   chan string
	// progress carries the export's progress.
	progress chan exported
	dirty    bool
	// replayed is when the track playing was last played again for an
	// edit, and replay when it is due again, for edits dragged faster
	// than it is worth reopening the file.
	replayed time.Time
	replay   <-chan time.Time
	// racks are the tracks' chains, loaded, by track: a track's is
	// loaded once it plays or its editor opens. states are the plugins'
	// states, by slot, as last read, and slots counts the slots made.
	racks  map[int]*rack
	states map[int][]byte
	slots  int
	// pluginDirs are where else to look for plugins, and found carries
	// what was found.
	pluginDirs []string
	found      chan []PluginChoice
	// replacing carries the files chosen to replace tracks', and
	// reveal shows a file in the file manager.
	replacing chan ReplaceFile
	reveal    func(path string) error
	// queued is what the deck has waiting to play next, in album play.
	queued queuedKey
	// version counts each track's changes, so a measuring tells whether
	// it measured the track as it is.
	version map[int]int
	// measuringVersion is the version of each track being measured.
	measuringVersion map[int]int
	// matches carries the gains found to bring tracks to the target.
	matches chan matched
	// settingsFile keeps what the program keeps across albums, and
	// saveDialog asks where to save; albums carries albums chosen.
	settingsFile string
	saveDialog   func(driver.SaveOptions) (string, error)
	albums       chan albumChoice
	// c is the window's client, and releasing says the release's
	// dialog is open.
	c         *gunim.Client
	releasing bool
	// stopExport cancels the export running.
	stopExport context.CancelFunc
	// exportOpen says the export's dialog is open, and lame is where
	// LAME was located, for MP3s.
	exportOpen bool
	lame       string
	lames      chan string
}

type scanned struct {
	id   int
	path string
	sc   scan
	err  error
}

type measured struct {
	id      int
	version int
	m       Measure
	err     error
}

type exported struct {
	id int
	// finished says the track's export is over, done or not.
	finished bool
	version  int
	progress float32
	path     string
	out      Measure
	err      error
	done     bool
}

func newApp(ctx context.Context, d *deck, file string) *app {
	ctx, stop := context.WithCancel(ctx)
	a := &app{ctx: ctx, stop: stop, d: d, file: file,
		scans: make(chan scanned, 16), measures: make(chan measured, 16),
		measurer: map[int]context.CancelFunc{}, settle: map[int]time.Time{},
		chosen: make(chan []string, 1), dirs: make(chan string, 1), progress: make(chan exported, 64),
		racks: map[int]*rack{}, version: map[int]int{}, measuringVersion: map[int]int{}, states: map[int][]byte{}, found: make(chan []PluginChoice, 1),
		replacing: make(chan ReplaceFile, 1), matches: make(chan matched, 4),
		albums: make(chan albumChoice, 1), lames: make(chan string, 1)}
	a.Gap, a.Target, a.Bits, a.Dither, a.Volume = time.Second, -14, 16, true, 0.8
	a.ExportWAV, a.MP3Rate = true, 320
	a.Curves = CurveS | CurveI
	return a
}

// load takes up the album kept in the file.
func (a *app) load() {
	b, err := os.ReadFile(a.file)
	if err != nil {
		return
	}
	var p project
	if json.Unmarshal(b, &p) != nil {
		return
	}
	a.Gap, a.Target, a.Bits, a.Dither = p.Gap, p.Target, p.Bits, p.Dither
	a.ExportDir = found(a.file, p.ExportDir, p.ExportAt)
	a.ExportWAV, a.ExportMP3 = p.ExportWAV == nil || *p.ExportWAV, p.ExportMP3
	if p.MP3Rate > 0 {
		a.MP3Rate = p.MP3Rate
	}
	a.Release = p.Release
	if p.Volume > 0 {
		a.Volume = p.Volume
	}
	a.Match, a.AlbumPlay, a.Follow, a.View = p.Match, p.AlbumPlay, p.Follow, p.View
	if p.Curves != nil {
		a.Curves = *p.Curves
	}
	for i, k := range p.Tracks {
		k.File = found(a.file, k.File, k.At)
		id := a.add(k.File, k.Title, k.Edit)
		if i == p.Current {
			a.Current = id
		}
		t := a.track(id)
		t.Note, t.Silence = k.Note, k.Silence
		for _, s := range k.Chain {
			a.slots++
			t.Chain = append(t.Chain, Slot{ID: a.slots, Path: s.Path, Class: s.Class, Name: s.Name,
				Vendor: s.Vendor, Bypass: s.Bypass})
			a.states[a.slots] = s.State
		}
		// Measured before, it waits for CalcLoudness, unless its file
		// changed since.
		if m, ok := k.Measure.measure(k.File); ok {
			t.Measure, t.Measured, t.Measuring, t.Stale = m, true, false, k.Stale
			delete(a.settle, id)
		}
	}
	a.measureAlbum()
}

// save keeps the album, if it changed.
func (a *app) save() {
	if !a.dirty || a.file == "" {
		return
	}
	a.dirty = false
	a.readStates()
	p := project{Release: a.Release, Gap: a.Gap, Target: a.Target, Bits: a.Bits, Dither: a.Dither,
		ExportDir: relative(a.file, a.ExportDir), ExportAt: a.ExportDir, ExportWAV: &a.ExportWAV, ExportMP3: a.ExportMP3,
		MP3Rate: a.MP3Rate,
		Volume:  a.Volume, Match: a.Match, Current: a.place(a.Current), AlbumPlay: a.AlbumPlay,
		Follow: a.Follow, View: a.View, Curves: &a.Curves}
	for _, t := range a.Tracks {
		k := keptTrack{Title: t.Title, File: relative(a.file, t.File), At: t.File, Note: t.Note, Silence: t.Silence,
			Edit: t.Edit, Stale: t.Stale}
		if t.Measured {
			k.Measure = keep(t.File, t.Measure)
		}
		for _, s := range t.Chain {
			k.Chain = append(k.Chain, keptSlot{Path: s.Path, Class: s.Class, Name: s.Name, Vendor: s.Vendor,
				Bypass: s.Bypass, State: a.states[s.ID]})
		}
		p.Tracks = append(p.Tracks, k)
	}
	b, err := json.MarshalIndent(p, "", "\t")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(a.file), 0o755)
	}
	if err == nil {
		tmp := a.file + ".tmp"
		if err = os.WriteFile(tmp, b, 0o644); err == nil {
			err = os.Rename(tmp, a.file)
		}
	}
	if err != nil {
		log.Printf("mastering: keeping the album: %v", err)
	}
}

// track returns the track with ID id, or nil.
func (a *app) track(id int) *Track {
	for i := range a.Tracks {
		if a.Tracks[i].ID == id {
			return &a.Tracks[i]
		}
	}
	return nil
}

// place returns where track id is on the album, or -1.
func (a *app) place(id int) int {
	return slices.IndexFunc(a.Tracks, func(t Track) bool { return t.ID == id })
}

// add puts the file at path on the album and starts reading it.
func (a *app) add(path, title string, e Edit) int {
	a.ids++
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	t := Track{ID: a.ids, Title: title, File: path, Edit: e}
	a.Tracks = append(a.Tracks, t)
	if a.Current == 0 {
		a.Current = t.ID
	}
	a.scan(t.ID, path)
	a.remeasure(t.ID)
	a.Loudness = Measure{}
	a.dirty = true
	return t.ID
}

// scan reads track id's file, path, in the background.
func (a *app) scan(id int, path string) {
	go func() {
		sc, err := scanTrack(a.ctx, path)
		select {
		case a.scans <- scanned{id, path, sc, err}:
		case <-a.ctx.Done():
		}
	}()
}

// slots holds the measurings running at once, so many tracks measured
// together share the computer.
var slots = make(chan struct{}, max(1, runtime.NumCPU()/2))

// remeasure marks track id changed since it was measured. A track
// never measured is measured once its changes pause a moment; the rest
// wait for CalcLoudness, as measuring through heavy plugins after every
// change would keep the computer busy.
func (a *app) remeasure(id int) {
	t := a.track(id)
	if t == nil {
		return
	}
	a.version[id]++
	t.Stale = true
	if !t.Measured {
		a.measureSoon(id, 300*time.Millisecond)
	}
}

// measureSoon measures track id after wait.
func (a *app) measureSoon(id int, wait time.Duration) {
	if t := a.track(id); t != nil {
		t.Measuring = true
		a.settle[id] = time.Now().Add(wait)
	}
}

// calcLoudness measures every track changed since it was measured, and
// not being measured as it is now.
func (a *app) calcLoudness() {
	for _, t := range a.Tracks {
		if t.Stale && (!t.Measuring || a.measuringVersion[t.ID] != a.version[t.ID]) {
			a.measureSoon(t.ID, 0)
		}
	}
}

// measured takes a measuring's result: the track as it was measured,
// fresh where nothing changed since.
func (a *app) measured(m measured) {
	t := a.track(m.id)
	if t == nil {
		return
	}
	t.Measuring = false
	if m.err != nil {
		a.Note = fmt.Sprintf("%s: %v", t.Title, m.err)
		return
	}
	t.Measure, t.Measured = m.m, true
	t.Stale = m.version != a.version[m.id]
	// The project keeps it, so the next run need not measure again.
	a.dirty = true
	a.applyLevel()
	a.measureAlbum()
}

// startMeasures starts the measurings whose edits have settled.
func (a *app) startMeasures() {
	now := time.Now()
	for id, at := range a.settle {
		if now.Before(at) {
			continue
		}
		delete(a.settle, id)
		t := a.track(id)
		if t == nil {
			continue
		}
		if cancel := a.measurer[id]; cancel != nil {
			cancel()
		}
		ctx, cancel := context.WithCancel(a.ctx)
		a.measurer[id] = cancel
		chain, states := a.chainOf(t)
		a.measuringVersion[id] = a.version[id]
		a.work.Add(1)
		go func(path string, gap time.Duration, e Edit, version int) {
			defer a.work.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			m, err := measure(ctx, path, gap, e, chain, states)
			<-slots
			if ctx.Err() != nil {
				return
			}
			select {
			case a.measures <- measured{id, version, m, err}:
			case <-a.ctx.Done():
			}
		}(t.File, a.gapOf(t), t.Edit, a.version[id])
	}
}

// nextSettle returns when the next measuring is due, or nil.
func (a *app) nextSettle() <-chan time.Time {
	var first time.Time
	for _, at := range a.settle {
		if first.IsZero() || at.Before(first) {
			first = at
		}
	}
	if first.IsZero() {
		return nil
	}
	return time.After(time.Until(first))
}

// serve keeps the album and hears what the window sends.
func serve(ctx context.Context, c gunim.Client, d *deck, o options) error {
	a := newApp(ctx, d, o.file)
	a.pluginDirs = o.plugins
	a.choose = func(o driver.ChooseOptions) ([]string, error) { return c.ChooseFiles(ctx, o) }
	a.reveal = c.Reveal
	a.c = &c
	a.saveDialog = func(o driver.SaveOptions) (string, error) { return c.SaveFile(ctx, o) }
	a.settingsFile = o.settings
	st := readSettings(a.settingsFile)
	a.RecentAlbums, a.Recent, a.lame = st.Albums, st.Plugins, st.LAME
	useLAME(findLAME(a.lame))
	a.load()
	a.opened(a.file)
	for _, p := range o.paths {
		a.add(p, "", Edit{})
	}
	a.applyLevel()
	a.Scanning = true
	go func() { a.found <- scanPlugins(a.pluginDirs) }()
	defer a.shutdown()
	if err := c.Mount(gunim.Root, "album", "album", a.Album, albumTopic); err != nil {
		return err
	}
	watch := time.NewTicker(time.Second)
	defer watch.Stop()
	_ = c.Focus("album")
	_ = c.SetTheme(themeName)
	if o.play {
		a.play(0, 10*time.Millisecond)
	}
	defer a.save()
	var keep <-chan time.Time
	for {
		if a.dirty && keep == nil {
			keep = time.After(time.Second)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-keep:
			keep = nil
			a.save()
			continue
		case s := <-a.scans:
			a.scanned(s)
		case m := <-a.measures:
			a.measured(m)
		case m := <-a.matches:
			a.matchedGain(m)
		case c := <-a.albums:
			a.chosenAlbum(c)
		case p := <-a.lames:
			if found := findLAME(p); found != "" {
				a.lame = found
				useLAME(found)
				a.writeSettings()
			} else {
				a.Note = p + " is no program to run"
			}
			if a.exportOpen {
				_ = c.Publish(exportTopic, a.exportDraft(nil))
			}
		case <-a.nextSettle():
			a.startMeasures()
		case <-a.replay:
			a.replay = nil
			if a.Playing {
				a.replayEdit()
			}
		case paths := <-a.chosen:
			a.addPaths(paths)
		case dir := <-a.dirs:
			a.ExportDir = dir
			a.dirty = true
			if a.exportOpen {
				_ = c.Publish(exportTopic, a.exportDraft(nil))
			}
		case p := <-a.progress:
			a.exportProgress(p)
		case ps := <-a.found:
			a.Plugins, a.Scanning = ps, false
		case r := <-a.replacing:
			a.replace(r.ID, r.Path)
		case id := <-a.d.turns:
			// Album play ran on into the next track.
			a.turned(id)
		case <-watch.C:
			if !a.watchPlugins() {
				continue
			}
		case <-a.d.done():
			// The track played to its end.
			a.Playing = false
			a.d.stop()
		case ev, ok := <-c.Intents():
			if !ok {
				return c.Err()
			}
			a.handle(ev.Intent)
		}
		a.queueNext()
		_ = c.Publish(albumTopic, a.Album)
	}
}

// scanned takes what reading a track's file told.
func (a *app) scanned(s scanned) {
	t := a.track(s.id)
	if t == nil || t.File != s.path {
		// Gone, or given another file since.
		return
	}
	if s.err != nil {
		a.Note = fmt.Sprintf("%s: %v", filepath.Base(t.File), s.err)
		return
	}
	t.Scanned, t.Format, t.Frames, t.Wave = true, s.sc.Format, s.sc.Frames, s.sc.Wave
	t.SoundStart, t.SoundEnd = s.sc.SoundStart, s.sc.SoundEnd
}

// addPaths adds files, the folders among them opened, in name order.
func (a *app) addPaths(paths []string) {
	var files []string
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !fi.IsDir() {
			files = append(files, p)
			continue
		}
		ents, err := os.ReadDir(p)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if ext := strings.ToLower(filepath.Ext(e.Name())); ext == ".wav" || ext == ".flac" || ext == ".mp3" || ext == ".ogg" {
				files = append(files, filepath.Join(p, e.Name()))
			}
		}
	}
	slices.Sort(files)
	for _, f := range files {
		a.add(f, "", Edit{})
	}
}

// replayEdit plays the track picked again as edited, from where it is:
// at once, or, within 60 ms of the last time, 60 ms after it.
func (a *app) replayEdit() {
	const every = 60 * time.Millisecond
	if wait := every - time.Since(a.replayed); wait > 0 {
		if a.replay == nil {
			a.replay = time.After(wait)
		}
		return
	}
	a.replayed = time.Now()
	t := a.track(a.Current)
	if t == nil || !a.d.edit(t.ID, t.File, a.gapOf(t), t.Edit) {
		at, _, _ := a.d.position()
		a.play(at, 15*time.Millisecond)
	}
}

// applyLevel sets the listening level, matched to the target where
// levels are matched.
func (a *app) applyLevel() {
	var match float32
	if t := a.track(a.Current); a.Match && t != nil && t.Measured && t.Measure.Loud {
		match = max(-24, min(a.Target-t.Measure.LUFS, 24))
	}
	a.d.setLevel(a.Volume, match)
}

// play plays the track picked from at, the one playing fading out over
// fade.
func (a *app) play(at, fade time.Duration) {
	t := a.track(a.Current)
	if t == nil {
		return
	}
	if err := a.d.play(t.ID, t.File, a.gapOf(t), t.Edit, a.rackOf(t), at, false, fade); err != nil {
		a.Note = err.Error()
		return
	}
	a.Playing = true
	a.Starts++
	a.applyLevel()
}

func (a *app) handle(in gunim.Intent) {
	switch in := in.(type) {
	case AddFiles:
		a.addPaths(in.Paths)
	case ChooseFiles:
		go func() {
			paths, err := a.choose(driver.ChooseOptions{Title: "Add tracks", Multiple: true,
				Filters: []driver.FileFilter{{Name: "Sound", Patterns: []string{"*.wav", "*.flac", "*.mp3", "*.ogg"}}}})
			if err == nil && len(paths) > 0 {
				a.chosen <- paths
			}
		}()
	case ChooseExportDir:
		go func() {
			paths, err := a.choose(driver.ChooseOptions{Title: "Export to", Folders: true})
			if err == nil && len(paths) > 0 {
				a.dirs <- paths[0]
			}
		}()
	case RemoveTrack:
		if i := a.place(in.ID); i >= 0 {
			if a.Current == in.ID {
				a.d.stop()
			}
			a.dropRack(in.ID)
			for _, s := range a.Tracks[i].Chain {
				delete(a.states, s.ID)
			}
			a.Tracks = slices.Delete(a.Tracks, i, i+1)
			a.measureAlbum()
			if a.Current == in.ID {
				a.d.stop()
				a.Playing = false
				a.Current = 0
				if len(a.Tracks) > 0 {
					a.Current = a.Tracks[min(i, len(a.Tracks)-1)].ID
				}
			}
			a.dirty = true
		}
	case MoveTrack:
		if in.From >= 0 && in.From < len(a.Tracks) && in.To >= 0 && in.To < len(a.Tracks) && in.From != in.To {
			t := a.Tracks[in.From]
			a.Tracks = slices.Insert(slices.Delete(a.Tracks, in.From, in.From+1), in.To, t)
			a.dirty = true
		}
	case RenameTrack:
		if t := a.track(in.ID); t != nil && strings.TrimSpace(in.Title) != "" {
			t.Title = strings.TrimSpace(in.Title)
			a.dirty = true
		}
	case Pick:
		if a.track(in.ID) == nil || in.ID == a.Current {
			return
		}
		at, _, _ := a.d.position()
		a.Current = in.ID
		a.dirty = true
		if a.Playing {
			// The same moment of the other track, with a crossfade too
			// short to hear as one.
			a.play(at, 25*time.Millisecond)
		} else {
			a.d.stop()
		}
		a.applyLevel()
	case SetEdit:
		t := a.track(in.ID)
		if t == nil {
			return
		}
		t.Edit, t.Seq = in.Edit, in.Seq
		a.dirty = true
		a.remeasure(t.ID)
		// While it plays, it plays as edited, from where it is.
		if a.Playing && a.Current == t.ID {
			a.replayEdit()
		}
	case TogglePlay:
		switch {
		case a.Playing:
			a.Playing = false
			a.d.setPaused(true)
		case a.d.done() != nil:
			a.Playing = true
			a.d.setPaused(false)
		default:
			a.play(0, 10*time.Millisecond)
		}
	case PlayFromStart:
		a.play(0, 10*time.Millisecond)
	case SeekTo:
		if a.d.done() == nil {
			a.play(in.At, 10*time.Millisecond)
			a.Playing = true
			return
		}
		a.d.seek(in.At)
	case SetGap:
		a.Gap = max(0, min(in.Gap, 10*time.Second))
		a.dirty = true
		for _, t := range a.Tracks {
			a.remeasure(t.ID)
		}
	case SetTarget:
		a.Target = max(-30, min(in.LUFS, -5))
		a.dirty = true
		a.applyLevel()
	case SetMatch:
		a.Match = in.On
		a.dirty = true
		a.applyLevel()
	case SetVolume:
		a.Volume = max(0, min(in.Volume, 1))
		a.dirty = true
		a.applyLevel()
	case SetExport:
		if in.Bits == 16 || in.Bits == 24 || in.Bits == 32 {
			a.Bits = in.Bits
		}
		a.Dither = in.Dither
		a.dirty = true
	case Export:
		a.export(in.IDs)
	case LocateLAME:
		go func() {
			paths, err := a.choose(driver.ChooseOptions{Title: "Locate LAME",
				Filters: []driver.FileFilter{{Name: "LAME", Patterns: []string{"lame.exe", "lame"}}}})
			if err == nil && len(paths) > 0 {
				a.lames <- paths[0]
			}
		}()
	case CancelExport:
		if a.stopExport != nil {
			a.stopExport()
		}
	case OpenExport:
		a.openExport(true, in.IDs)
	case ExportClosed:
		a.openExport(false, nil)
	case StartExport:
		if in.Bits == 16 || in.Bits == 24 || in.Bits == 32 {
			a.Bits = in.Bits
		}
		a.Dither, a.ExportWAV, a.ExportMP3 = in.Dither, in.WAV, in.MP3
		if in.MP3Rate > 0 {
			a.MP3Rate = in.MP3Rate
		}
		a.dirty = true
		a.openExport(false, nil)
		a.export(in.IDs)
	case CalcLoudness:
		a.calcLoudness()
	case MatchTarget:
		a.match(in.ID)
	case EditRelease:
		a.editRelease(true)
	case ReleaseClosed:
		a.editRelease(false)
	case SetRelease:
		a.Release = in.Release
		a.dirty = true
		a.editRelease(false)
	case NewAlbum, OpenAlbum, SaveAlbumAs:
		a.chooseAlbum(in)
	case OpenAlbumPath:
		if _, err := os.Stat(in.Path); err != nil {
			a.Note = err.Error()
			a.RecentAlbums = slices.DeleteFunc(a.RecentAlbums, func(p string) bool { return p == in.Path })
			a.writeSettings()
			return
		}
		a.switchTo(in.Path, false)
	case ReplaceFile:
		a.replace(in.ID, in.Path)
	case ChooseReplacement:
		t := a.track(in.ID)
		if t == nil {
			return
		}
		go func(id int, old string) {
			paths, err := a.choose(driver.ChooseOptions{Title: "Replace " + filepath.Base(old),
				Filters: []driver.FileFilter{{Name: "Sound", Patterns: []string{"*.wav", "*.flac", "*.mp3", "*.ogg"}}}})
			if err == nil && len(paths) > 0 {
				a.replacing <- ReplaceFile{ID: id, Path: paths[0]}
			}
		}(t.ID, t.File)
	case ShowFile:
		if t := a.track(in.ID); t != nil && a.reveal != nil {
			if err := a.reveal(t.File); err != nil {
				a.Note = err.Error()
			}
		}
	case SetAlbumPlay:
		a.AlbumPlay = in.On
		a.dirty = true
	case SetFollow:
		a.Follow = in.Follow % followModes
		a.dirty = true
	case SetView:
		a.View = in.View
		a.dirty = true
	case SetCurves:
		a.Curves = in.Curves
		a.dirty = true
	case SetSilence:
		t := a.track(in.ID)
		if t == nil {
			return
		}
		if in.Silence != nil {
			s := max(0, min(*in.Silence, 10*time.Second))
			in.Silence = &s
		}
		t.Silence = in.Silence
		a.dirty = true
		a.remeasure(t.ID)
		if a.Playing && a.Current == t.ID {
			a.replayEdit()
		}
	case SetNote:
		if t := a.track(in.ID); t != nil && t.Note != in.Note {
			t.Note = in.Note
			a.dirty = true
		}
	default:
		a.handleChain(in)
	}
}

// gapOf is the silence before track t: its own, or the album's.
func (s *Album) gapOf(t *Track) time.Duration {
	if t.Silence != nil {
		return *t.Silence
	}
	return s.Gap
}
