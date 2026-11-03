package api

import (
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"
)

// A forgotten event is applied once more and then refused again; an event signed ahead of the
// clock is remembered until its own window ends.
func TestKyPostReplayForgetsOnlyWhatItIsTold(t *testing.T) {
	g := &kypostReplay{seen: map[string]time.Time{}}
	now := time.Now()
	if err := g.Check("e1", now); err != nil {
		t.Fatal(err)
	}
	if err := g.Check("e1", now); !errors.Is(err, syncauth.ErrReplay) {
		t.Fatalf("replay: %v", err)
	}
	g.forget("e1")
	if err := g.Check("e1", now.Add(time.Minute)); err != nil {
		t.Fatalf("retry after forget: %v", err)
	}
	if err := g.Check("e1", now.Add(time.Minute)); !errors.Is(err, syncauth.ErrReplay) {
		t.Fatalf("replay of the retry: %v", err)
	}
	g.seen["old"] = now.Add(-syncauth.DefaultWindow - time.Second)
	if err := g.Check("old", now); err != nil {
		t.Fatalf("an ID past its window: %v", err)
	}
	ahead := now.Add(syncauth.DefaultWindow / 2)
	if err := g.Check("ahead", ahead); err != nil || !g.seen["ahead"].Equal(ahead) {
		t.Fatalf("ahead: %v %v", err, g.seen["ahead"])
	}
}
