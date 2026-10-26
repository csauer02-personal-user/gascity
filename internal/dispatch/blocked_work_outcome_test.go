package dispatch

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

// workerBlocked is a worker's close of a step it could not finish and on which
// it asks that no further work start: gc.outcome=fail, gc.work_outcome=blocked
// and a failure class that is neither hard nor transient.
var workerBlocked = map[string]string{
	beadmeta.OutcomeMetadataKey:       beadmeta.OutcomeFail,
	beadmeta.WorkOutcomeMetadataKey:   beadmeta.WorkOutcomeBlocked,
	beadmeta.FailureClassMetadataKey:  "parent-candidate-not-published",
	beadmeta.FailureReasonMetadataKey: "parent_candidate_not_published",
}

// TestClassifyRetryAttemptBlockedWorkOutcomeIsHard pins that a failed attempt
// closed gc.work_outcome=blocked ends the retry whatever its failure class, so
// readiness letting the control run behind it never re-runs the attempt.
func TestClassifyRetryAttemptBlockedWorkOutcomeIsHard(t *testing.T) {
	t.Parallel()
	for _, class := range []string{"", beadmeta.FailureClassTransient, beadmeta.FailureClassHard, "scope-mismatch"} {
		got := classifyRetryAttempt(beads.Bead{Metadata: map[string]string{
			beadmeta.OutcomeMetadataKey:       beadmeta.OutcomeFail,
			beadmeta.WorkOutcomeMetadataKey:   beadmeta.WorkOutcomeBlocked,
			beadmeta.FailureClassMetadataKey:  class,
			beadmeta.FailureReasonMetadataKey: "scope_mismatch",
		}})
		want := retryEvalResult{Outcome: "hard", Reason: "scope_mismatch"}
		if got != want {
			t.Errorf("classifyRetryAttempt(fail, blocked, class %q) = %+v, want %+v", class, got, want)
		}
	}
	// A blocked work outcome on a passed attempt is not a failure.
	if got := classifyRetryAttempt(beads.Bead{Metadata: map[string]string{
		beadmeta.OutcomeMetadataKey:     beadmeta.OutcomePass,
		beadmeta.WorkOutcomeMetadataKey: beadmeta.WorkOutcomeBlocked,
	}}); got.Outcome != "pass" {
		t.Errorf("classifyRetryAttempt(pass, blocked) = %+v, want pass", got)
	}
}

// TestProcessRetryControlBlockedScopeMismatchHardFails is a worker's
// scope-mismatch close: gc.outcome=fail, gc.work_outcome=blocked,
// gc.failure_class=scope-mismatch. The retry settles hard and schedules no
// second attempt.
func TestProcessRetryControlBlockedScopeMismatchHardFails(t *testing.T) {
	t.Parallel()
	store := beads.NewMemStore()

	root := mustCreate(t, store, beads.Bead{
		Title:    "workflow",
		Metadata: map[string]string{"gc.kind": "workflow"},
	})
	control := mustCreate(t, store, beads.Bead{
		Title: "implement",
		Metadata: map[string]string{
			"gc.kind":             "retry",
			"gc.root_bead_id":     root.ID,
			"gc.step_ref":         "mol-test.implement",
			"gc.step_id":          "implement",
			"gc.max_attempts":     "3",
			"gc.on_exhausted":     "hard_fail",
			"gc.source_step_spec": `{"id":"implement","title":"Implement","type":"task","retry":{"max_attempts":3}}`,
			"gc.control_epoch":    "1",
		},
	})
	attempt1 := mustCreate(t, store, beads.Bead{
		Title: "implement attempt 1",
		Metadata: map[string]string{
			"gc.root_bead_id":   root.ID,
			"gc.step_ref":       "mol-test.implement.attempt.1",
			"gc.attempt":        "1",
			"gc.outcome":        "fail",
			"gc.work_outcome":   "blocked",
			"gc.failure_class":  "scope-mismatch",
			"gc.failure_reason": "scope_mismatch",
		},
	})
	mustClose(t, store, attempt1.ID)
	mustDep(t, store, control.ID, attempt1.ID, "blocks")

	result, err := processRetryControl(store, mustGet(t, store, control.ID), ProcessOptions{})
	if err != nil {
		t.Fatalf("processRetryControl: %v", err)
	}
	if result.Action != "hard-fail" {
		t.Fatalf("action = %q, want hard-fail", result.Action)
	}
	after := mustGet(t, store, control.ID)
	if after.Status != "closed" || after.Metadata["gc.outcome"] != "fail" {
		t.Fatalf("control = status %q outcome %q, want closed/fail", after.Status, after.Metadata["gc.outcome"])
	}
	if after.Metadata["gc.final_disposition"] != "hard_fail" {
		t.Fatalf("disposition = %q, want hard_fail", after.Metadata["gc.final_disposition"])
	}
	if after.Metadata["gc.control_epoch"] != "1" {
		t.Fatalf("epoch = %q, want 1 (no second attempt)", after.Metadata["gc.control_epoch"])
	}
	deps, err := store.DepList(control.ID, "down")
	if err != nil {
		t.Fatalf("DepList: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("control deps = %d, want 1 (attempt 1 only, no attempt scheduled)", len(deps))
	}
}

// TestCompiledSimpleRalphBlockedIterationFails runs the reported shape: a ralph with
// gc.max_attempts above one whose first iteration closed gc.outcome=fail and
// gc.work_outcome=blocked with class parent-candidate-not-published. The ralph
// is offered behind the blocked step, hard-fails in iteration 1 without running
// its check, clones no iteration 2, and the run settles failed.
func TestCompiledSimpleRalphBlockedIterationFails(t *testing.T) {
	t.Parallel()
	city := newCompiledRalphCity(t, "simple-ralph-hard-fail", compiledSimpleRalphFormula, 0)
	fx := buildCompiledRalphFixture(t, identityStore, city, shapeUnlinked, func(string, int) map[string]string { return workerBlocked })
	serveCompiledRalph(t, fx)
	ledger := snapshotLedger(t, fx.ledger)

	requireRalphHardFailed(t, ledger, "implement", 1, "parent_candidate_not_published")
	requireClosedWithOutcome(t, ledger, 0, fx.rootID, beadmeta.OutcomeFail)
}

// TestCompiledRalphBlockedMemberEndsLoop covers the scoped iteration: a member
// of the iteration's scope closes blocked, in both the linked shape (its retry
// control reads the blocked attempt as hard) and the unlinked shape (the
// member itself is the culprit), and the loop stops in that iteration.
func TestCompiledRalphBlockedMemberEndsLoop(t *testing.T) {
	t.Parallel()
	for _, shape := range []ralphFixtureShape{shapeLinked, shapeUnlinked} {
		t.Run(shape.String(), func(t *testing.T) {
			policy := func(step string, iteration int) map[string]string {
				if step == "apply-fixes" && iteration == 1 {
					return workerBlocked
				}
				return workerPass
			}
			city := newCompiledRalphCity(t, "review-loop-hard-fail", compiledReviewLoopFormula, 0)
			fx := buildCompiledRalphFixture(t, identityStore, city, shape, policy)
			serveCompiledRalph(t, fx)
			ledger := snapshotLedger(t, fx.ledger)

			requireRalphHardFailed(t, ledger, "review-loop", 1, "parent_candidate_not_published")
			requireClosedWithOutcome(t, ledger, 0, fx.rootID, beadmeta.OutcomeFail)
		})
	}
}

// TestProcessRalphCheckBlockedSubjectEndsLoop is the KindCheck shape of a
// ralph: its subject closed gc.outcome=fail and gc.work_outcome=blocked with a
// class that is neither hard nor transient. The check settles the logical bead
// failed in that attempt and clones no attempt 2, though gc.max_attempts
// allows five.
func TestProcessRalphCheckBlockedSubjectEndsLoop(t *testing.T) {
	t.Parallel()

	cityPath := t.TempDir()
	checkPath := writeCheckScript(t, cityPath, "check.sh", "#!/bin/bash\nexit 0\n")
	store, logical, run1, check1 := newSimpleRalphLoop(t, "implement", checkPath, 5)
	if err := store.SetMetadataBatch(run1.ID, workerBlocked); err != nil {
		t.Fatalf("stamp blocked subject: %v", err)
	}
	if err := store.Close(run1.ID); err != nil {
		t.Fatalf("close run1: %v", err)
	}

	result, err := ProcessControl(store, check1, ProcessOptions{CityPath: cityPath})
	if err != nil {
		t.Fatalf("ProcessControl(check1): %v", err)
	}
	if !result.Processed || result.Action != "hard-fail" {
		t.Fatalf("result = %+v, want processed hard-fail", result)
	}
	logicalAfter := mustGetBead(t, store, logical.ID)
	if logicalAfter.Status != "closed" || logicalAfter.Metadata[beadmeta.OutcomeMetadataKey] != beadmeta.OutcomeFail {
		t.Fatalf("logical = status %q outcome %q, want closed/fail", logicalAfter.Status, logicalAfter.Metadata[beadmeta.OutcomeMetadataKey])
	}
	if got := logicalAfter.Metadata[beadmeta.FailureReasonMetadataKey]; got != "parent_candidate_not_published" {
		t.Fatalf("logical gc.failure_reason = %q, want parent_candidate_not_published", got)
	}
	all, err := beads.DirectMembers(store, run1.Metadata[beadmeta.RootBeadIDMetadataKey])
	if err != nil {
		t.Fatalf("beads.DirectMembers: %v", err)
	}
	for _, b := range all {
		if b.Metadata[beadmeta.AttemptMetadataKey] == "2" {
			t.Fatalf("a blocked subject must not clone another attempt; found %s (kind %q)", b.ID, b.Metadata[beadmeta.KindMetadataKey])
		}
	}
}
