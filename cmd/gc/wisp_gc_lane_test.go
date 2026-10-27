package main

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/runtime"
)

// countingWispGC counts purge runs; due says whether shouldRun answers yes.
type countingWispGC struct {
	due  bool
	runs int
}

func (c *countingWispGC) shouldRun(time.Time) bool { return c.due }

func (c *countingWispGC) runGC(beads.GraphStore, beads.SessionStore, beads.MailStore, time.Time) (int, error) {
	c.runs++
	return 0, nil
}

func newWispGCLaneRuntime(t *testing.T, wg wispGC) *CityRuntime {
	t.Helper()
	return &CityRuntime{
		cityPath:            t.TempDir(),
		cityName:            "test-city",
		cfg:                 &config.City{},
		sp:                  runtime.NewFake(),
		standaloneCityStore: beads.NewMemStore(),
		wg:                  wg,
		rec:                 events.Discard,
		logPrefix:           "test-city",
		stdout:              io.Discard,
		stderr:              io.Discard,
		buildFn: func(*config.City, runtime.Provider, beads.Store) DesiredStateResult {
			return DesiredStateResult{State: map[string]TemplateParams{}}
		},
	}
}

// The purge is not a step of the tick, so a long one cannot delay the
// session decisions that wait for the next tick (#2604).
func TestTick_DoesNotRunWispGC(t *testing.T) {
	wg := &countingWispGC{due: true}
	cr := newWispGCLaneRuntime(t, wg)

	var dirty atomic.Bool
	var lastProviderName string
	var prevPoolRunning map[string]bool
	cr.tick(context.Background(), &dirty, &lastProviderName, cr.cityPath, &prevPoolRunning, "test")

	if wg.runs != 0 {
		t.Fatalf("tick ran wisp GC %d time(s), want 0: the purge belongs to its lane", wg.runs)
	}
}

func TestWispGCLanePass_RunsOnlyWhenDue(t *testing.T) {
	wg := &countingWispGC{}
	cr := newWispGCLaneRuntime(t, wg)
	var pressureSkips int

	cr.runWispGCLanePass(&pressureSkips)
	if wg.runs != 0 {
		t.Fatalf("pass ran wisp GC %d time(s) before it was due, want 0", wg.runs)
	}
	wg.due = true
	cr.runWispGCLanePass(&pressureSkips)
	if wg.runs != 1 {
		t.Fatalf("pass ran wisp GC %d time(s) once due, want 1", wg.runs)
	}
}

func TestWispGCLanePass_SkipsQuiescentCity(t *testing.T) {
	wg := &countingWispGC{due: true}
	cr := newWispGCLaneRuntime(t, wg)
	cr.beadsQuiescent.Store(true)
	var pressureSkips int

	cr.runWispGCLanePass(&pressureSkips)
	if wg.runs != 0 {
		t.Fatalf("pass ran wisp GC %d time(s) on a quiescent city, want 0", wg.runs)
	}
}
