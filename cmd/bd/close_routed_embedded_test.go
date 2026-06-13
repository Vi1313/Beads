//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
)

// TestEmbeddedCloseRoutesThroughContributorPlanningStore is the regression
// test for the "embeddeddolt: store is read-only" bug surfaced after the
// depends_on_id fix (task.md follow-up): bd close on an issue that lives in
// a contributor auto-routed planning store (e.g. ~/.beads-planning) failed
// when both ends were embedded-dolt because resolveCloseTargets opened the
// routed store via openRoutedReadStore. The embedded RO open marks
// readOnly=true and refuses any write transaction, so the close write hit
// errReadOnly = "embeddeddolt: store is read-only" at commit time.
//
// The fix routes write-intent contributor auto-routing through
// openRoutedWriteStore (mirroring the prefix-routed #4141 path). This test
// asserts:
//   - bd close succeeds end-to-end against an auto-routed planning issue
//   - the close actually commits in the planning store (status flips to closed)
//   - the read-only error string never appears in stderr/stdout (would be a
//     regression even if the command somehow appeared to succeed)
//
// The dolt-server-mode TestResolveCloseTargets does not exercise this path
// because dolt server-mode RO only skips schema init; it does not refuse
// writes at the store level. Embedded mode is where the read-only refusal
// actually fires.
func TestEmbeddedCloseRoutesThroughContributorPlanningStore(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 to run embedded dolt routed-close tests")
	}
	t.Parallel()

	bd := buildEmbeddedBD(t)

	projectDir, planningDir := initContributor(t, bd, "cl")

	// Create an issue — contributor auto-routing puts it in the planning store.
	issue := bdCreate(t, bd, projectDir, "Close me through routing")
	if issue.ID == "" {
		t.Fatal("expected issue ID")
	}
	if !strings.HasPrefix(issue.ID, "cl-") {
		t.Fatalf("ID should have prefix cl-, got %q", issue.ID)
	}
	planningBeadsDir := filepath.Join(planningDir, ".beads")
	assertIssueInStore(t, planningBeadsDir, "cl", issue.ID)

	// Close it. Pre-fix this failed with:
	//   "embeddeddolt: store is read-only"
	// because ensureShared() in resolveCloseTargets opened the planning store
	// via openRoutedReadStore.
	out, err := bdRunWithFlockRetry(t, bd, projectDir, "close", issue.ID, "--reason", "regression")
	if err != nil {
		t.Fatalf("bd close %s failed: %v\n%s", issue.ID, err, out)
	}
	if strings.Contains(string(out), "store is read-only") {
		t.Fatalf("bd close output contains the read-only error: %s", out)
	}

	// The close must have actually committed in the planning store, not just
	// printed a success message. Query the embedded planning store directly.
	assertIssueClosedInStore(t, planningBeadsDir, "cl", issue.ID)
}

// assertIssueClosedInStore verifies that the issue in the embedded planning
// store has status = "closed". This catches the case where the routing
// opened a writable store but a stale handle / wrong store / silent rollback
// left the row open.
func assertIssueClosedInStore(t *testing.T, beadsDir, database, issueID string) {
	t.Helper()
	dataDir := filepath.Join(beadsDir, "embeddeddolt")
	db, cleanup, err := embeddeddolt.OpenSQL(t.Context(), dataDir, database, "main")
	if err != nil {
		t.Fatalf("OpenSQL(%s, %s): %v", beadsDir, database, err)
	}
	defer cleanup()

	var status string
	if err := db.QueryRowContext(t.Context(),
		"SELECT status FROM issues WHERE id = ?", issueID).Scan(&status); err != nil {
		t.Fatalf("query status for %s: %v", issueID, err)
	}
	if status != "closed" {
		t.Fatalf("issue %s in planning store: status = %q, want %q", issueID, status, "closed")
	}
}
