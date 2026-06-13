//go:build cgo

package embeddeddolt_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/storage/schema"
)

// TestOpenSucceedsOnV49WithDirtyEventsTable mirrors the real-world embedded
// Dolt workspace state where a v49 database already has the split dependency
// target schema but cannot apply the v50/v51 hardening migrations because the
// `events` aux table is dirty (uncommitted writes from prior writes that did
// not run Commit). The open path must defer those aux-table migrations rather
// than refuse to open — dependency reads and writes still need to work.
//
// Regression coverage for the second compatibility blocker described in
// task.md: previously embedded mode returned
// `embeddeddolt: init schema: embeddeddolt: migrate: pending schema
// migrations alter pre-existing dirty tables: events`.
func TestOpenSucceedsOnV49WithDirtyEventsTable(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	ctx := t.Context()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	dataDir := filepath.Join(beadsDir, "embeddeddolt")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}

	const dbName = "compatdb"

	// 1. Bring the database up to v49 with a clean working set, including a
	//    committed seed issue so the events table has something to reference.
	seedIssueID := "bd-dirty-events-seed"
	bringUpToV49(t, ctx, dataDir, dbName, seedIssueID)

	// 2. Insert a dirty row into events without committing the Dolt working
	//    set. The store opens its own short-lived SQL connection later, so
	//    the dirty status persists until something explicitly commits or
	//    reverts it.
	insertDirtyEventRow(t, ctx, dataDir, dbName, seedIssueID)

	// 3. Re-open through the production store path. Pre-fix this returned:
	//    "embeddeddolt: migrate: pending schema migrations alter
	//    pre-existing dirty tables: events".
	store, err := embeddeddolt.Open(ctx, beadsDir, dbName, "main")
	if err != nil {
		t.Fatalf("Open with dirty events table: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// 4. Schema must still be v49 — the deferred migrations are *not* applied
	//    while the table remains dirty. This guards against a future change
	//    that silently advances the schema cursor without doing the work.
	assertSchemaVersion(t, ctx, dataDir, dbName, 49)

	// 5. The dirty events row must still be there, untouched. The compat
	//    path must not have stamped or dropped it.
	if got := countEventsByID(t, ctx, dataDir, dbName, "ev-dirty"); got != 1 {
		t.Fatalf("dirty events row count: got %d, want 1", got)
	}

	// 6. A simple committed read through the opened store must work. We do
	//    not depend on the store's typed API here — issuing a raw SELECT
	//    against the same database via the cached connector is enough to
	//    prove the connection is healthy after the deferred-migration
	//    branch returned successfully.
	if got := countIssuesByID(t, ctx, dataDir, dbName, seedIssueID); got != 1 {
		t.Fatalf("seed issue %q not visible after open: got %d, want 1", seedIssueID, got)
	}
}

// bringUpToV49 creates the database, runs main-source migrations up to v49,
// inserts a committed seed issue, and runs DOLT_COMMIT so dolt_status is clean.
func bringUpToV49(t *testing.T, ctx context.Context, dataDir, dbName, seedIssueID string) {
	t.Helper()

	db, cleanup, err := embeddeddolt.OpenSQL(ctx, dataDir, "", "")
	if err != nil {
		t.Fatalf("OpenSQL: %v", err)
	}
	defer func() { _ = cleanup() }()

	if _, err := db.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+dbName+"`"); err != nil {
		t.Fatalf("create database: %v", err)
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "USE `"+dbName+"`"); err != nil {
		t.Fatalf("use database: %v", err)
	}
	if _, err := schema.MigrateUpTo(ctx, conn, 49); err != nil {
		t.Fatalf("MigrateUpTo(49): %v", err)
	}

	if _, err := conn.ExecContext(ctx, `
		INSERT INTO issues (
			id, title, description, design, acceptance_criteria, notes,
			status, priority, issue_type, created_by, owner
		) VALUES (?, ?, '', '', '', '', 'open', 2, 'task', 'tester', 'tester')
	`, seedIssueID, "seed"); err != nil {
		t.Fatalf("insert seed issue: %v", err)
	}

	if _, err := conn.ExecContext(ctx, `CALL DOLT_ADD('-A')`); err != nil {
		t.Fatalf("dolt add: %v", err)
	}
	if _, err := conn.ExecContext(ctx, `CALL DOLT_COMMIT('--allow-empty', '-m', 'seed v49')`); err != nil {
		t.Fatalf("dolt commit seed: %v", err)
	}
}

// insertDirtyEventRow writes a single events row but does NOT call DOLT_COMMIT,
// leaving the events table dirty in the working set. This is the realistic
// shape of the user's broken Notes/planning store, where pending migrations
// (v50/v51) try to ALTER events while the table is uncommitted.
func insertDirtyEventRow(t *testing.T, ctx context.Context, dataDir, dbName, seedIssueID string) {
	t.Helper()

	db, cleanup, err := embeddeddolt.OpenSQL(ctx, dataDir, dbName, "main")
	if err != nil {
		t.Fatalf("OpenSQL: %v", err)
	}
	defer func() { _ = cleanup() }()

	if _, err := db.ExecContext(ctx, `
		INSERT INTO events (id, issue_id, event_type, actor, old_value, new_value, comment)
		VALUES (?, ?, 'status_change', 'tester', 'open', 'in_progress', '')
	`, "ev-dirty", seedIssueID); err != nil {
		t.Fatalf("insert dirty event: %v", err)
	}

	// Sanity: events must be reported dirty by dolt_status before we close.
	if got := scanDirty(t, ctx, db, "events"); got == 0 {
		t.Fatalf("events table not reported dirty after insert")
	}
}

func assertSchemaVersion(t *testing.T, ctx context.Context, dataDir, dbName string, want int) {
	t.Helper()

	db, cleanup, err := embeddeddolt.OpenSQL(ctx, dataDir, dbName, "main")
	if err != nil {
		t.Fatalf("OpenSQL: %v", err)
	}
	defer func() { _ = cleanup() }()

	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	got, err := schema.CurrentVersion(ctx, conn)
	if err != nil {
		t.Fatalf("CurrentVersion: %v", err)
	}
	if got != want {
		t.Fatalf("schema version = %d, want %d", got, want)
	}
}

func countEventsByID(t *testing.T, ctx context.Context, dataDir, dbName, id string) int {
	t.Helper()
	return countWhereID(t, ctx, dataDir, dbName, "events", id)
}

func countIssuesByID(t *testing.T, ctx context.Context, dataDir, dbName, id string) int {
	t.Helper()
	return countWhereID(t, ctx, dataDir, dbName, "issues", id)
}

func countWhereID(t *testing.T, ctx context.Context, dataDir, dbName, table, id string) int {
	t.Helper()
	db, cleanup, err := embeddeddolt.OpenSQL(ctx, dataDir, dbName, "main")
	if err != nil {
		t.Fatalf("OpenSQL: %v", err)
	}
	defer func() { _ = cleanup() }()

	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM `"+table+"` WHERE id = ?", id,
	).Scan(&count); err != nil {
		t.Fatalf("count %s where id=%q: %v", table, id, err)
	}
	return count
}

func scanDirty(t *testing.T, ctx context.Context, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM dolt_status WHERE table_name = ?", table,
	).Scan(&count); err != nil {
		t.Fatalf("dolt_status probe for %s: %v", table, err)
	}
	return count
}
