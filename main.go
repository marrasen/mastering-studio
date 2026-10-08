// Command mastering-studio is Marras Mastering Studio. It masters an
// album: its tracks, each cut, faded, and set apart from the one before
// by the same silence; played one at a time, a key or a click switching
// to another at the same moment, to compare; measured as they will be
// exported, loudness, its range and true peak, when Measure loudness asks, the
// tracks changed since marked; and exported, each at its own length,
// to WAV files of 16 or 24 bits, dithered, or 32-bit float, and MP3.
//
// Each track runs through a chain of VST3 plugins of its own: only the
// track heard has its plugins running, and a track is measured and
// exported through a copy of its chain, run offline.
//
//	go run .
//	go run . mix1.wav mix2.wav
//
// Files dropped on the list join the album, which is kept between runs;
// a file dropped on the waveform replaces the track's, its edit and
// chain kept. A double-click on the title over the waveform renames the
// track. Keys: Space plays and pauses, 1 to 9 pick a track, Up and Down
// step through them, Left and Right seek, with Alt to the loop's in and
// out, Home starts over, M matches
// levels, A plays on into the next track, B bypasses the chains, I and
// O set a loop's in and out at the playhead, L loops, and S switches
// between listening A and B, each its own track and moment, on the
// album or among the references kept for every album. Alt with the
// wheel, or the slider at the waveform's right, draws it louder, to see
// quiet sound.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/marrasen/gunim"
	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/driver"
	"github.com/marrasen/gunim/geom"
	"github.com/marrasen/gunim/install"
)

// version is the studio's version, set as a release is built.
var version = "dev"

func main() {
	// Started from anywhere but its own folder, the studio opens its
	// installer; installed, it goes on.
	install.Run(installer())
	state := flag.String("project", "", "the album to open; by default the one open last, or the untitled one")
	shot := flag.String("shot", "", "write the window to this PNG file after -after, and quit")
	after := flag.Duration("after", 2*time.Second, "how long -shot waits")
	play := flag.Bool("play", false, "start playing the track picked")
	size := flag.String("size", "", "the window's size, as 1680x1040; by default where it was last, or 1680x1040")
	plugins := flag.String("plugins", "", "more folders of VST3 plugins, beside the system's, as a list like PATH")
	iconOut := flag.String("write-icon", "", "write the icon, 256 pixels square, to this PNG file, and quit")
	demo := flag.String("demo", "", "write six demo songs and a project of them to this folder, and open it")
	look := flag.String("theme", "", "the theme to open in: mastering, light, contrast or dim")
	flag.Parse()
	paths := flag.Args()
	// A project opened from the file manager comes as its path alone.
	if *state == "" && len(paths) == 1 && strings.EqualFold(filepath.Ext(paths[0]), albumExt) {
		*state, paths = paths[0], nil
	}
	if *demo != "" {
		path, err := writeDemo(*demo)
		if err != nil {
			log.Fatal(err)
		}
		*state = path
	}
	if *iconOut != "" {
		if err := writeIcon(*iconOut, 256); err != nil {
			log.Fatal(err)
		}
		return
	}
	moveSettings()
	w, h := float32(1680), float32(1040)
	if *size != "" {
		if _, err := fmt.Sscanf(*size, "%gx%g", &w, &h); err != nil || w <= 0 || h <= 0 {
			log.Fatalf("mastering: -size %q: want a width and a height, as 1680x1040", *size)
		}
	}
	o := options{file: *state, paths: paths, play: *play, shot: *shot, after: *after, size: geom.Sz(w, h), theme: *look}
	if d := configDir(); d != "" {
		o.settings = filepath.Join(d, "settings.json")
	}
	// Where the window was as it closed last, unless a size is asked.
	st := readSettings(o.settings)
	if st.Window != nil && *size == "" {
		o.place = st.Window
	}
	o.zoom = st.Zoom
	if o.file == "" {
		// The album open last, where it still is.
		o.file = untitled()
		if s := readSettings(o.settings); len(s.Albums) > 0 {
			if _, err := os.Stat(s.Albums[0]); err == nil {
				o.file = s.Albums[0]
			}
		}
	}
	if *plugins != "" {
		o.plugins = filepath.SplitList(*plugins)
	}
	err := run(o)
	if restartAfter.Load() {
		// The release put in place, started once this one has closed.
		if rerr := install.Restart(installer()); rerr != nil {
			log.Printf("mastering: starting the new release: %v", rerr)
		}
	}
	if err != nil {
		log.Fatal(err)
	}
}

// options are how the program runs.
type options struct {
	// file is where the album is kept, paths the files to add, and play
	// says to start playing.
	file  string
	paths []string
	play  bool
	// shot is a PNG file to write the window to, after after.
	shot  string
	after time.Duration
	size  geom.Size
	// theme is the name of the theme to open in, or empty for the
	// default.
	theme string
	// plugins are more folders of plugins.
	plugins []string
	// settings is the file of what is kept across albums, and place
	// where the window opens.
	settings string
	place    *driver.Placement
	// zoom is how large the window draws its content, as Ctrl and the
	// wheel last left it.
	zoom float32
	// placement says where the window is, to keep as it closes.
	placement func() (driver.Placement, bool)
}

func run(o options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	mix := audio.NewMixer()
	d := newDeck(mix)
	if err := d.openOutput(readSettings(o.settings).Output); err != nil {
		log.Printf("mastering: no sound: %v", err)
	}
	err := gunim.Main(ctx, func(a *gunim.App) error {
		w, err := a.NewWindow(gunim.WindowOptions{Title: appName, Size: o.size, Place: o.place, Icons: icons(),
			AskToClose: Quit{}, ZoomKeys: true, Zoom: o.zoom})
		if err != nil {
			return fmt.Errorf("mastering: %w", err)
		}
		registerViews(w, d)
		o.placement = w.Placement
		c := w.Client()
		if o.shot != "" {
			go func() {
				select {
				case <-time.After(o.after):
				case <-ctx.Done():
					return
				}
				if err := writeShot(ctx, c, o.shot); err != nil {
					log.Print(err)
				}
				c.Close()
			}()
		}
		return serve(ctx, c, d, o)
	})
	if errors.Is(err, driver.ErrNoDriver) {
		log.Print("gunim has no driver for this operating system yet")
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// writeShot writes what the window shows to a PNG file.
func writeShot(ctx context.Context, c gunim.Client, path string) error {
	img, err := c.Shot(ctx)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	return errors.Join(png.Encode(f, img), f.Close())
}
