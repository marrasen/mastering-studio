package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/marrasen/gunim/audio"
	"github.com/marrasen/gunim/install"
)

// A newer release told of is asked about; fetched on Update, it is
// offered to restart into; and what kept it from being fetched is said.
func TestANewerReleaseIsAskedAbout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(ctx, newDeck(audio.NewMixer()), "")
	staged := make(chan install.Release, 1)
	was := stageUpdate
	stageUpdate = func(_ context.Context, r install.Release) error { staged <- r; return nil }
	t.Cleanup(func() { stageUpdate = was })

	a.newsOf(news{release: install.Release{Version: "v0.3.0"}})
	if a.Update.Seq != 1 || a.Update.Version != "0.3.0" || a.Update.Ready {
		t.Fatalf("told of a release, the window is told %+v", a.Update)
	}
	if to := updateToast(a.Update); !strings.Contains(to.Title, "0.3.0 is out") || to.Buttons[0].On(false) != (FetchUpdate{}) {
		t.Fatalf("the toast asks %+v", to)
	}
	a.handle(FetchUpdate{})
	select {
	case r := <-staged:
		if r.Version != "v0.3.0" {
			t.Fatalf("fetched %s", r.Version)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Update fetched nothing")
	}
	a.newsOf(<-updateNews)
	if !a.Update.Ready || a.Update.Seq != 2 {
		t.Fatalf("fetched, the window is told %+v", a.Update)
	}
	if to := updateToast(a.Update); !strings.Contains(to.Title, "0.3.0 is ready") || to.Buttons[0].On(false) != (RestartToUpdate{}) {
		t.Fatalf("the restart toast is %+v", to)
	}

	a.newsOf(news{release: install.Release{Version: "v0.4.0"}, err: errors.New("no network")})
	if !strings.Contains(a.Note, "0.4.0") || !strings.Contains(a.Note, "no network") {
		t.Fatalf("a failed update says %q", a.Note)
	}
}

// The news waits for the studio's own goroutine, and a burst of it does
// not hold the updates up.
func TestTellNeverBlocks(t *testing.T) {
	for range cap(updateNews) + 3 {
		tell(news{release: install.Release{Version: "v9.9.9"}})
	}
	for len(updateNews) > 0 {
		<-updateNews
	}
}
