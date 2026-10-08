package sling

import (
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/convoy"
	"github.com/gastownhall/gascity/internal/runtime"
)

// A plain sling to a second target must not take a bead that is already
// routed to, or claimed through, a different target. Before this fence the
// pre-flight check only printed "already routed to" and the direct route
// overwrote gc.routed_to, so a bead could be dispatched to two executors.
func TestDirectSlingRefusesBeadRoutedToAnotherTarget(t *testing.T) {
	for _, state := range []string{"routed", "claimed"} {
		t.Run(state, func(t *testing.T) {
			deps, runner, bead := directRouteOwnerFixture(t, state)
			reviewer := config.Agent{Name: "reviewer", MaxActiveSessions: intPtr(1)}

			_, err := DoSling(SlingOpts{Target: reviewer, BeadOrFormula: bead.ID, NoFormula: true}, deps, deps.Store)
			if err == nil {
				t.Fatal("direct sling to a second target succeeded over an existing route")
			}
			if !strings.Contains(err.Error(), "planner") || !strings.Contains(err.Error(), "--force") {
				t.Fatalf("refusal should name the current route and --force: %v", err)
			}
			if len(runner.calls) != 0 {
				t.Fatalf("refused sling ran the route command: %v", runner.calls)
			}
			got, err := deps.Store.Get(bead.ID)
			if err != nil {
				t.Fatal(err)
			}
			if route := got.Metadata[beadmeta.RoutedToMetadataKey]; route != "planner" {
				t.Fatalf("gc.routed_to = %q, want planner", route)
			}
			if got.Assignee != bead.Assignee {
				t.Fatalf("assignee = %q, want %q", got.Assignee, bead.Assignee)
			}
		})
	}
}

// --force keeps its documented meaning: the operator moves the route.
func TestDirectSlingForceMovesRouteFromAnotherTarget(t *testing.T) {
	deps, runner, bead := directRouteOwnerFixture(t, "routed")
	reviewer := config.Agent{Name: "reviewer", MaxActiveSessions: intPtr(1)}

	if _, err := DoSling(SlingOpts{Target: reviewer, BeadOrFormula: bead.ID, NoFormula: true, Force: true}, deps, deps.Store); err != nil {
		t.Fatalf("forced sling: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("forced sling route calls = %d, want 1", len(runner.calls))
	}
	got, err := deps.Store.Get(bead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if route := got.Metadata[beadmeta.RoutedToMetadataKey]; route != "reviewer" {
		t.Fatalf("gc.routed_to = %q, want reviewer", route)
	}
}

// The convoy batch path routes each child through the same fence.
func TestDirectSlingBatchRefusesChildRoutedToAnotherTarget(t *testing.T) {
	deps, runner, bead := directRouteOwnerFixture(t, "routed")
	batch, err := deps.Store.Create(beads.Bead{Title: "batch", Type: "convoy"})
	if err != nil {
		t.Fatal(err)
	}
	if err := convoy.TrackItem(deps.Store, batch.ID, bead.ID); err != nil {
		t.Fatal(err)
	}
	reviewer := config.Agent{Name: "reviewer", MaxActiveSessions: intPtr(1)}

	result, err := DoSlingBatch(SlingOpts{Target: reviewer, BeadOrFormula: batch.ID, NoFormula: true}, deps, deps.Store)
	if err == nil || result.Routed != 0 || result.Failed != 1 {
		t.Fatalf("batch sling = %+v, %v; want the child refused", result, err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("refused child ran the route command: %v", runner.calls)
	}
	got, err := deps.Store.Get(bead.ID)
	if err != nil {
		t.Fatal(err)
	}
	if route := got.Metadata[beadmeta.RoutedToMetadataKey]; route != "planner" {
		t.Fatalf("gc.routed_to = %q, want planner", route)
	}
}

// A closed bead keeps its old route as history; it is not a live owner.
func TestDirectSlingAllowsClosedBeadRoutedToAnotherTarget(t *testing.T) {
	deps, _, bead := directRouteOwnerFixture(t, "closed")
	reviewer := config.Agent{Name: "reviewer", MaxActiveSessions: intPtr(1)}

	if _, err := DoSling(SlingOpts{Target: reviewer, BeadOrFormula: bead.ID, NoFormula: true}, deps, deps.Store); err != nil {
		t.Fatalf("sling of a closed bead: %v", err)
	}
}

func directRouteOwnerFixture(t *testing.T, state string) (SlingDeps, *fakeRunner, beads.Bead) {
	t.Helper()
	bead := beads.Bead{
		ID: "BL-1", Title: "BL-1", Type: "task", Status: "open",
		Metadata: map[string]string{beadmeta.RoutedToMetadataKey: "planner"},
	}
	switch state {
	case "claimed":
		bead.Status, bead.Assignee = "in_progress", "planner-session"
	case "closed":
		bead.Status = "closed"
	}
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test"},
		Agents: []config.Agent{
			{Name: "planner", MaxActiveSessions: intPtr(1)},
			{Name: "reviewer", MaxActiveSessions: intPtr(1)},
		},
	}
	runner := newFakeRunner()
	deps := testDeps(cfg, runtime.NewFake(), runner.run)
	deps.CityPath = t.TempDir()
	deps.Store = beads.NewMemStoreFrom(0, []beads.Bead{bead}, nil)
	return deps, runner, bead
}
