package dispatch

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// The control plane closes beads through the embedded store, below the bd CLI's
// validation.on-close seam, so every one of its close sites must stamp a
// non-empty close_reason itself — a closed bead with a NULL reason is
// unauditable (sc-o55xbc: mol-polecat-* workflow roots closed reason-less via
// the completion path). These tests pin the stamps on the attempt-close and
// source-close paths.

func TestProcessRetryControlPassStampsCloseReason(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()

	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})
	control := mustCreate(t, store, beads.Bead{
		Title: "review",
		Metadata: map[string]string{
			"gc.kind":             "retry",
			"gc.root_bead_id":     root.ID,
			"gc.step_ref":         "mol-test.review",
			"gc.step_id":          "review",
			"gc.max_attempts":     "3",
			"gc.on_exhausted":     "hard_fail",
			"gc.source_step_spec": `{"id":"review","title":"Review","type":"task","retry":{"max_attempts":3}}`,
			"gc.control_epoch":    "1",
		},
	})
	attempt1 := mustCreate(t, store, beads.Bead{
		Title: "review attempt 1",
		Metadata: map[string]string{
			"gc.root_bead_id": root.ID,
			"gc.step_ref":     "mol-test.review.attempt.1",
			"gc.attempt":      "1",
			"gc.outcome":      "pass",
		},
	})
	mustClose(t, store, attempt1.ID)
	mustDep(t, store, control.ID, attempt1.ID, "blocks")

	if _, err := processRetryControl(store, mustGet(t, store, control.ID), ProcessOptions{}); err != nil {
		t.Fatalf("processRetryControl: %v", err)
	}

	after := mustGet(t, store, control.ID)
	if after.Status != "closed" {
		t.Fatalf("control status = %q, want closed", after.Status)
	}
	if got := after.Metadata["close_reason"]; got != attemptPassedCloseReason {
		t.Fatalf("control close_reason = %q, want %q", got, attemptPassedCloseReason)
	}
}

func TestCloseSourceBeadPreservingOutcomeStampsCloseReason(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()

	source := mustCreate(t, store, beads.Bead{
		Title:    "source",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})
	if err := closeSourceBeadPreservingOutcome(store, mustGet(t, store, source.ID)); err != nil {
		t.Fatalf("closeSourceBeadPreservingOutcome: %v", err)
	}
	after := mustGet(t, store, source.ID)
	if after.Status != "closed" {
		t.Fatalf("source status = %q, want closed", after.Status)
	}
	if got := after.Metadata[beadmeta.OutcomeMetadataKey]; got != beadmeta.OutcomePass {
		t.Fatalf("source outcome = %q, want backfilled pass", got)
	}
	if got := after.Metadata["close_reason"]; got != sourceCompletedCloseReason {
		t.Fatalf("source close_reason = %q, want %q", got, sourceCompletedCloseReason)
	}
}

func TestCloseSourceBeadPreservingOutcomePreservesExistingReasonAndOutcome(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()

	source := mustCreate(t, store, beads.Bead{
		Title: "source",
		Metadata: map[string]string{
			"gc.kind":                   "workflow",
			beadmeta.OutcomeMetadataKey: beadmeta.OutcomeFail,
			"close_reason":              "recorded upstream of the completion path",
		},
	})
	if err := closeSourceBeadPreservingOutcome(store, mustGet(t, store, source.ID)); err != nil {
		t.Fatalf("closeSourceBeadPreservingOutcome: %v", err)
	}
	after := mustGet(t, store, source.ID)
	if got := after.Metadata[beadmeta.OutcomeMetadataKey]; got != beadmeta.OutcomeFail {
		t.Fatalf("source outcome = %q, want preserved fail", got)
	}
	if got := after.Metadata["close_reason"]; got != "recorded upstream of the completion path" {
		t.Fatalf("source close_reason = %q, want preserved", got)
	}
}
