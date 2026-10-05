package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/audioui"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/install"
)

// The vocabulary the two halves share.
type (
	// Album is what the window shows.
	Album struct {
		// Release is what the album is released as, as its exports are
		// tagged.
		Release
		// Tracks are the album's tracks, in order, and References tracks
		// to compare it with, the same in every project.
		Tracks     []Track
		References []Track
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
		// ExportReport writes a report of each export beside its files.
		ExportReport bool
		// Current is the track picked, which plays; Playing says it
		// does, and Starts counts the starts, so the window can tell a
		// new play from one going on.
		Current int
		Playing bool
		Starts  int
		// Side is the session of listening heard, A or B, which Current,
		// Playing and Looping are of; Away is the other, as it was left,
		// and Background plays it on, unheard, while it is away.
		Side       Side
		Away       Session
		Background bool
		// Spectrum is how the meters show the spectrum, and Carry where a
		// track picked while one plays starts, both kept across runs.
		Spectrum SpectrumView
		Carry    Carry
		// Presets are the names of the plugin chains kept, in order;
		// DeletedPreset is the one deleted last, which Deletes counts.
		Presets       []string
		DeletedPreset string
		Deletes       int
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
		// Looping plays the loop of the track picked over and over.
		Looping bool
		// Listen is how the sound is listened to: in stereo, mono, or
		// its side alone; Bypass plays the tracks without their chains
		// and gains, to compare.
		Listen Listen
		Bypass bool
		// Exporting says an export is running.
		Exporting bool
		// Note says what went wrong last, for the window to show.
		Note string
		// Update is the newer release the window tells of.
		Update Update
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
		// Unsaved says the album holds changes the user has not saved,
		// kept in its draft; Untitled that it was never saved.
		Unsaved  bool
		Untitled bool
		// UndoLabel and RedoLabel name the changes undo and redo take
		// back or make again, or are empty; Undone says what the last
		// undo or redo did, which Undos counts.
		UndoLabel, RedoLabel string
		Undone               string
		Undos                int
		Scanning             bool
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
		// Marks are notes at times of it, in their order.
		Marks []Mark
		// Loop, where set, is the stretch of its file played over and
		// over while the album loops.
		Loop *Loop
		// Seq is the window's count of edits taken, so it can tell its
		// own edit coming back from an older one.
		Seq int
		// Chain is the plugins the track runs through, after its edit, and
		// Preset the preset it was loaded from or saved as.
		Chain  []Slot
		Preset string
		// Scanned says the file has been read through: its format,
		// length, waveform and where its sound starts and ends.
		Scanned    bool
		Format     audio.Format
		Frames     int64
		Wave       *audioui.Wave
		SoundStart time.Duration
		SoundEnd   time.Duration
		// Measure is the track measured as rendered, which Measuring
		// says is under way for the edit as it is now.
		Measure   Measure
		Measured  bool
		Measuring bool
		// Stale says the track changed since it was last measured, to be
		// measured again with MeasureLoudness.
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
	// MoveTrack moves the track at From to To, on the album, or, Refs,
	// among the references.
	MoveTrack struct {
		From, To int
		Refs     bool
	}
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
	// SetListen sets how the sound is listened to.
	SetListen struct{ Listen Listen }
	// Quit is the window asked to close: its placement is kept first.
	Quit struct{}
	// FetchUpdate fetches the newer release told of, and puts it in
	// place for the next start.
	FetchUpdate struct{}
	// RestartToUpdate closes the studio as Quit does, and starts the
	// release put in place.
	RestartToUpdate struct{}
	// SetBypassAll plays the tracks without their chains and gains, or
	// with.
	SetBypassAll struct{ On bool }
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
	// MeasureLoudness measures every track changed since it was last
	// measured.
	MeasureLoudness struct{}
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
	// RenamePlugin names a slot of a track's chain apart from its plugin,
	// or, Label empty, after it again.
	RenamePlugin struct {
		Track, Slot int
		Label       string
	}
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
	// Report writes a report of each export.
	Report    bool `json:",omitempty"`
	Volume    float32
	Match     bool
	Current   int
	AlbumPlay bool
	Follow    Follow
	Looping   bool   `json:",omitempty"`
	View      View   `json:",omitempty"`
	Curves    *uint8 `json:",omitempty"`
	// B is the track session B has, and whether it loops.
	B *keptSession `json:",omitempty"`
}

// keptSession is a session's track, as a project keeps it: its place
// on the album, or among the references, -1 for neither.
type keptSession struct {
	Track, Ref int
	Looping    bool `json:",omitempty"`
}

type keptTrack struct {
	Title, File string
	Note        string         `json:",omitempty"`
	Silence     *time.Duration `json:",omitempty"`
	Marks       []Mark         `json:",omitempty"`
	Loop        *Loop          `json:",omitempty"`
	Edit        Edit
	Chain       []keptSlot `json:",omitempty"`
	// Preset is the preset its chain was loaded from or saved as.
	Preset string `json:",omitempty"`
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
	Label        string `json:",omitempty"`
	Bypass       bool
	State        []byte
	// Gain is how much louder the plugin made the track, as last
	// measured, where Gained says it was.
	Gain   float32 `json:",omitempty"`
	Gained bool    `json:",omitempty"`
	// LRA is the loudness range out of the plugin, as last measured,
	// where Ranged says it had one.
	LRA    float32 `json:",omitempty"`
	Ranged bool    `json:",omitempty"`
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
	// offered is the newer release the window last told of.
	offered install.Release
	ids     int
	// scanCache keeps what reading each file told, for the next run.
	scanCache scanCache
	// refsFile is where the references are kept, and chosenRefs carries
	// the files chosen to add to them.
	refsFile   string
	chosenRefs chan []string
	// drafts is the folder of the albums' drafts, or "" for none: the
	// album is then saved as it changes. asking says the dialog of
	// changes not saved is up, and quitAfterSave that the window closes
	// once the album is saved where the user says.
	drafts        string
	asking        bool
	quitAfterSave bool
	// history is the album's changes, to undo and redo.
	history
	// presets are the plugin chains kept by name, in presetsFile;
	// naming says the dialog that names one is up, and lastDeleted is
	// the one deleted last, to bring back.
	presets     []Preset
	presetsFile string
	naming      bool
	lastDeleted *Preset
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
	// markIDs counts the notes at times made, and looped what the deck
	// loops, as told last.
	markIDs int
	looped  loopKey
	// exportOpen says the export's dialog is open, and lame is where
	// LAME was located, for MP3s.
	exportOpen bool
	lame       string
	lames      chan string
	// window is where the window was as it closed last, which the
	// settings keep; helping says the help is open.
	window  *driver.Placement
	helping bool
	// zoom is how large the window draws its content.
	zoom float32
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
	// reportErr says why the export's report was not written, as done.
	reportErr error
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
		chosen: make(chan []string, 1), chosenRefs: make(chan []string, 1), dirs: make(chan string, 1), progress: make(chan exported, 64),
		racks: map[int]*rack{}, version: map[int]int{}, measuringVersion: map[int]int{}, states: map[int][]byte{}, found: make(chan []PluginChoice, 1),
		replacing: make(chan ReplaceFile, 1), matches: make(chan matched, 4),
		albums: make(chan albumChoice, 1), lames: make(chan string, 1)}
	a.Gap, a.Target, a.Bits, a.Dither, a.Volume = time.Second, -14, 16, true, 0.8
	a.ExportWAV, a.MP3Rate = true, 320
	a.Curves = CurveS | CurveI
	return a
}

// load takes up the album kept in the file, or in its draft, where
// that holds changes newer than the file.
func (a *app) load() {
	a.history = history{}
	a.Unsaved, a.Untitled = false, a.untitled()
	from := a.file
	if d := a.draftPath(); d != "" && d != a.file && newer(d, a.file) {
		from = d
	}
	p, ok := readProject(from)
	if !ok {
		return
	}
	// A draft, or the untitled album with anything on it, holds changes
	// not saved.
	if from != a.file || (a.untitled() && len(p.Tracks) > 0) {
		a.savedID, a.Unsaved = -1, true
	}
	a.Gap, a.Target, a.Bits, a.Dither = p.Gap, p.Target, p.Bits, p.Dither
	a.ExportDir = found(a.file, p.ExportDir, p.ExportAt)
	a.ExportWAV, a.ExportMP3, a.ExportReport = p.ExportWAV == nil || *p.ExportWAV, p.ExportMP3, p.Report
	if p.MP3Rate > 0 {
		a.MP3Rate = p.MP3Rate
	}
	a.Release = p.Release
	if p.Volume > 0 {
		a.Volume = p.Volume
	}
	a.Match, a.AlbumPlay, a.Follow, a.View, a.Looping = p.Match, p.AlbumPlay, p.Follow, p.View, p.Looping
	if p.Curves != nil {
		a.Curves = *p.Curves
	}
	for i, k := range p.Tracks {
		k.File = found(a.file, k.File, k.At)
		id := a.restore(k, false)
		if i == p.Current {
			a.Current = id
		}
	}
	// Session B's track, as it was left.
	if b := p.B; b != nil {
		switch {
		case b.Track >= 0 && b.Track < len(a.Tracks):
			a.Away = Session{Current: a.Tracks[b.Track].ID, Looping: b.Looping}
		case b.Ref >= 0 && b.Ref < len(a.References):
			a.Away = Session{Current: a.References[b.Ref].ID, Looping: b.Looping}
		}
	}
	a.measureAlbum()
}

// restore puts a track kept back on the album, or among the
// references, its notes, chain and measure as they were kept, and
// returns its ID.
func (a *app) restore(k keptTrack, ref bool) int {
	id := a.addTo(ref, k.File, k.Title, k.Edit)
	t := a.track(id)
	t.Note, t.Marks, t.Loop, t.Preset = k.Note, k.Marks, k.Loop, k.Preset
	// A reference kept with no silence of its own has none.
	if k.Silence != nil || !ref {
		t.Silence = k.Silence
	}
	for _, m := range k.Marks {
		a.markIDs = max(a.markIDs, m.ID)
	}
	for _, s := range k.Chain {
		a.slots++
		t.Chain = append(t.Chain, Slot{ID: a.slots, Path: s.Path, Class: s.Class, Name: s.Name,
			Vendor: s.Vendor, Label: s.Label, Bypass: s.Bypass, Gain: s.Gain, Gained: s.Gained, LRA: s.LRA,
			Ranged: s.Ranged})
		a.states[a.slots] = s.State
	}
	// Measured before, it waits for MeasureLoudness, unless its file
	// changed since.
	if m, ok := k.Measure.measure(k.File); ok {
		t.Measure, t.Measured, t.Measuring, t.Stale = m, true, false, k.Stale
		delete(a.settle, id)
	}
	return id
}

// kept is track t as a project keeps it, its file from the album's
// folder.
func (a *app) kept(t *Track) keptTrack {
	k := keptTrack{Title: t.Title, File: relative(a.file, t.File), At: t.File, Note: t.Note, Silence: t.Silence,
		Marks: t.Marks, Loop: t.Loop, Edit: t.Edit, Stale: t.Stale, Preset: t.Preset}
	if t.Measured {
		k.Measure = keep(t.File, t.Measure)
	}
	for _, s := range t.Chain {
		k.Chain = append(k.Chain, keptSlot{Path: s.Path, Class: s.Class, Name: s.Name, Vendor: s.Vendor,
			Label: s.Label, Bypass: s.Bypass, State: a.states[s.ID], Gain: s.Gain, Gained: s.Gained, LRA: s.LRA,
			Ranged: s.Ranged})
	}
	return k
}

// track returns the track with ID id, of the album or the references,
// or nil.
func (a *app) track(id int) *Track { return a.find(id) }

// place returns where track id is on the album, or -1.
func (a *app) place(id int) int {
	return slices.IndexFunc(a.Tracks, func(t Track) bool { return t.ID == id })
}

// add puts the file at path on the album and starts reading it.
func (a *app) add(path, title string) { a.addTo(false, path, title, Edit{}) }

// addTo puts the file at path on the album, or among the references,
// and starts reading it. A reference has no silence before it.
func (a *app) addTo(ref bool, path, title string, e Edit) int {
	a.ids++
	// A reference added as an MP3 is called as its tag says.
	if title == "" && ref && strings.EqualFold(filepath.Ext(path), ".mp3") {
		title = readTags(path).name()
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	t := Track{ID: a.ids, Title: title, File: path, Edit: e}
	if ref {
		t.Silence = new(time.Duration)
		a.References = append(a.References, t)
	} else {
		a.Tracks = append(a.Tracks, t)
		a.Loudness = Measure{}
	}
	if a.Current == 0 {
		a.Current = t.ID
	}
	a.scan(t.ID, path)
	a.remeasure(t.ID)
	a.dirty = true
	return t.ID
}

// scan reads track id's file, path, in the background, or takes what
// reading it told before, where the file is as it was.
func (a *app) scan(id int, path string) {
	go func() {
		sc, ok := a.scanCache.get(path)
		var err error
		if !ok {
			if sc, err = scanTrack(a.ctx, path); err == nil {
				a.scanCache.put(path, sc)
			}
		}
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
// wait for MeasureLoudness, as measuring through heavy plugins after every
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

// measureLoudness measures every track changed since it was measured, and
// not being measured as it is now.
func (a *app) measureLoudness() {
	for _, t := range slices.Concat(a.Tracks, a.References) {
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
	a.takeSteps(t)
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
			m.sum = fileSum(path)
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
	a.RecentAlbums, a.Recent, a.lame, a.window, a.zoom = st.Albums, st.Plugins, st.LAME, st.Window, st.Zoom
	a.Background, a.Spectrum, a.Carry = st.Background, st.spectrum(), st.Carry
	if a.settingsFile != "" {
		a.refsFile = filepath.Join(filepath.Dir(a.settingsFile), "references.json")
		a.presetsFile = filepath.Join(filepath.Dir(a.settingsFile), "presets.json")
	}
	a.loadPresets()
	useLAME(findLAME(a.lame))
	a.scanCache = scanCache{dir: scanCacheDir()}
	if a.settingsFile != "" {
		a.drafts = filepath.Join(filepath.Dir(a.settingsFile), "drafts")
	}
	a.loadRefs()
	a.load()
	a.opened(a.file)
	for _, p := range o.paths {
		a.add(p, "")
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
	// quit closes the window, where it is kept for the next time: it
	// leaves as a program quitting does, fading as it goes.
	quit := func() {
		if o.placement != nil {
			if p, ok := o.placement(); ok {
				a.window = &p
				a.writeSettings()
			}
		}
		c.Leave()
	}
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
		case ch := <-a.albums:
			a.chosenAlbum(ch)
			// Saved where the user said, as the window closes: closed.
			if _, as := ch.in.(SaveAlbumAs); as && a.quitAfterSave {
				a.quitAfterSave = false
				if ch.path != "" && !a.Unsaved {
					quit()
					continue
				}
			}
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
			a.record("adding tracks", 0, "")
			a.addPaths(paths)
		case paths := <-a.chosenRefs:
			a.handleAB(AddReferences{Paths: paths})
		case dir := <-a.dirs:
			a.record("the export folder", 0, "")
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
			a.handle(r)
		case id := <-a.d.turns:
			// Album play ran on into the next track.
			a.turned(id)
		case <-watch.C:
			if !a.watchPlugins() {
				continue
			}
		case n := <-updateNews:
			a.newsOf(n)
		case <-a.d.done():
			// The track played to its end.
			a.Playing = false
			a.d.stop()
		case ev, ok := <-c.Intents():
			if !ok {
				return c.Err()
			}
			switch in := ev.Intent.(type) {
			case Quit, RestartToUpdate:
				if _, ok := in.(RestartToUpdate); ok {
					restartAfter.Store(true)
				}
				// With changes not saved, the user says what to do.
				if a.Unsaved {
					a.askToClose()
					break
				}
				quit()
				continue
			case CloseAnswer:
				if a.answered(in.Choice) {
					quit()
					continue
				}
				if in.Choice == CloseCancel {
					// Staying, so no restart either.
					restartAfter.Store(false)
				}
			default:
				a.handle(ev.Intent)
			}
		}
		a.queueNext()
		a.applyLoop()
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
	for _, f := range soundFiles(paths) {
		a.add(f, "")
	}
}

// soundFiles returns the sound files among paths, and in the folders
// among them, in name order.
func soundFiles(paths []string) []string {
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
	return files
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
	match := map[int]float32{}
	for _, ts := range [][]Track{a.Tracks, a.References} {
		for i := range ts {
			if db, ok := a.matchDB(&ts[i]); ok {
				match[ts[i].ID] = db
			}
		}
	}
	a.d.setLevel(a.Volume, match)
	for _, r := range a.racks {
		r.setMatch(a.Match)
	}
}

// takeSteps takes how much louder each plugin of track t made it, as
// last measured, onto its slots and its chain playing. A plugin
// bypassed as it was measured keeps its gain from before, which the
// track plays louder by with it matched.
func (a *app) takeSteps(t *Track) {
	gains := map[int]float32{}
	t.Measure.Restore = 0
	for i := range t.Chain {
		s := &t.Chain[i]
		if r, ok := t.Measure.lras[s.ID]; ok {
			s.LRA, s.Ranged = r, true
		}
		if g, ok := t.Measure.steps[s.ID]; ok {
			s.Gain, s.Gained = g, true
		} else if s.Gained {
			t.Measure.Restore += s.Gain
		}
		if s.Gained {
			gains[s.ID] = s.Gain
		}
	}
	if r := a.racks[t.ID]; r != nil {
		r.setGains(gains)
	}
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
	// A change of the album's is a step to undo, and not yet saved.
	if label, track, key, ok := a.change(in); ok {
		a.record(label, track, key)
	}
	switch in := in.(type) {
	case FetchUpdate:
		a.fetchUpdate()
	case SaveAlbum:
		if a.untitled() {
			a.chooseAlbum(SaveAlbumAs{})
			return
		}
		if err := a.saveNow(); err != nil {
			a.Note = err.Error()
		}
	case Undo:
		a.undoStep(false)
	case Redo:
		a.undoStep(true)
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
		a.remove(in.ID)
	case MoveTrack:
		ts := &a.Tracks
		if in.Refs {
			ts = &a.References
		}
		if in.From >= 0 && in.From < len(*ts) && in.To >= 0 && in.To < len(*ts) && in.From != in.To {
			t := (*ts)[in.From]
			*ts = slices.Insert(slices.Delete(*ts, in.From, in.From+1), in.To, t)
			a.dirty = true
		}
	case RenameTrack:
		if t := a.track(in.ID); t != nil && strings.TrimSpace(in.Title) != "" {
			t.Title = strings.TrimSpace(in.Title)
			a.dirty = true
		}
	case Pick:
		to := a.track(in.ID)
		if to == nil || in.ID == a.Current {
			return
		}
		at, _, _ := a.d.position()
		at = a.carried(a.track(a.Current), to, at)
		a.Current = in.ID
		a.dirty = true
		if a.Playing {
			// Where Carry says in the other track, with a crossfade too
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
		a.Dither, a.ExportWAV, a.ExportMP3, a.ExportReport = in.Dither, in.WAV, in.MP3, in.Report
		if in.MP3Rate > 0 {
			a.MP3Rate = in.MP3Rate
		}
		a.dirty = true
		a.openExport(false, nil)
		a.export(in.IDs)
	case MeasureLoudness:
		a.measureLoudness()
	case MatchTarget:
		a.match(in.ID)
	case gunim.Zoomed:
		// Ctrl and the wheel, or + and -, zoomed the window: kept for the
		// next time.
		a.zoom = in.Zoom
		a.writeSettings()
	case ShowHelp:
		a.showHelp(true)
	case HelpClosed:
		a.showHelp(false)
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
	case SetCarry:
		a.Carry = in.Carry % carries
		a.writeSettings()
	case SetSpectrum:
		a.Spectrum = in.View
		a.writeSettings()
	case SetListen:
		a.Listen = in.Listen
		a.d.listen(in.Listen)
	case SetBypassAll:
		a.Bypass = in.On
		a.d.setBypass(in.On)
		a.applyLevel()
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
		if a.handleMarks(in) || a.handleLoop(in) || a.handleAB(in) || a.handlePresets(in) {
			return
		}
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

// matchDB is the gain that brings track t to the target as it is heard,
// in decibels, while levels are matched, and whether there is one: the
// master's, or, bypassed, the mix's, as each was measured.
func (s *Album) matchDB(t *Track) (float32, bool) {
	if !s.Match || !t.Measured {
		return 0, false
	}
	m := t.Measure
	switch {
	case s.Bypass && m.DryLoud:
		return max(-24, min(s.Target-m.DryLUFS, 24)), true
	case !s.Bypass && m.Loud:
		return max(-24, min(s.Target-m.LUFS-m.Restore, 24)), true
	}
	return 0, false
}
