package main

import (
	"context"
	"os"
	"testing"

	"github.com/marrasen/gunim/audio"
)

func TestTheDemoOpensAsAnAlbumOfSixSongs(t *testing.T) {
	dir := t.TempDir()
	path, err := writeDemo(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), path)
	a.load()
	if len(a.Tracks) != len(demoSongs) || a.Artist != "The Quiet Harbour" {
		t.Fatalf("the demo opens with %d tracks, by %q", len(a.Tracks), a.Artist)
	}
	first := a.Tracks[0]
	if first.Title != "Lantern Season" || len(first.Marks) != 2 || first.Note == "" {
		t.Fatalf("the first track is %q, with %d notes at times and a note of %q", first.Title, len(first.Marks), first.Note)
	}
	for _, tr := range a.Tracks {
		f, err := os.Open(tr.File)
		if err != nil {
			t.Fatal(err)
		}
		src, format, err := audio.DecodeNative(f)
		if err != nil {
			t.Fatalf("%s: %v", tr.Title, err)
		}
		if secs := float64(src.Len()) / float64(format.SampleRate); secs < 90 || secs > 150 {
			t.Fatalf("%s plays %.0f seconds", tr.Title, secs)
		}
		_ = f.Close()
	}
}
