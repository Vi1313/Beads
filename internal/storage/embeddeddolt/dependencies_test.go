//go:build cgo

package embeddeddolt_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/storage/schema"
	"github.com/steveyegge/beads/internal/types"
)

func TestAddDependency(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	t.Run("basic_blocks", func(t *testing.T) {
		te := newTestEnv(t, "db")
		ctx := t.Context()

		a := &types.Issue{ID: "db-a", Title: "A", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		b := &types.Issue{ID: "db-b", Title: "B", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, a, "tester"); err != nil {
			t.Fatalf("CreateIssue A: %v", err)
		}
		if err := te.store.CreateIssue(ctx, b, "tester"); err != nil {
			t.Fatalf("CreateIssue B: %v", err)
		}

		dep := &types.Dependency{IssueID: "db-a", DependsOnID: "db-b", Type: types.DepBlocks}
		if err := te.store.AddDependency(ctx, dep, "tester"); err != nil {
			t.Fatalf("AddDependency: %v", err)
		}
	})

	t.Run("cycle_detection", func(t *testing.T) {
		te := newTestEnv(t, "cy")
		ctx := t.Context()

		a := &types.Issue{ID: "cy-a", Title: "A", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		b := &types.Issue{ID: "cy-b", Title: "B", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, a, "tester"); err != nil {
			t.Fatalf("CreateIssue A: %v", err)
		}
		if err := te.store.CreateIssue(ctx, b, "tester"); err != nil {
			t.Fatalf("CreateIssue B: %v", err)
		}

		// A blocks B
		dep1 := &types.Dependency{IssueID: "cy-a", DependsOnID: "cy-b", Type: types.DepBlocks}
		if err := te.store.AddDependency(ctx, dep1, "tester"); err != nil {
			t.Fatalf("AddDependency A->B: %v", err)
		}
		if err := te.store.Commit(ctx, "dep1"); err != nil {
			t.Fatalf("Commit: %v", err)
		}

		// B blocks A should fail (cycle)
		dep2 := &types.Dependency{IssueID: "cy-b", DependsOnID: "cy-a", Type: types.DepBlocks}
		err := te.store.AddDependency(ctx, dep2, "tester")
		if err == nil {
			t.Fatal("expected cycle detection error")
		}
		if !strings.Contains(err.Error(), "cycle") {
			t.Errorf("expected cycle error, got: %v", err)
		}
	})

	t.Run("mixed_table_cycle_permanent_endpoints", func(t *testing.T) {
		te := newTestEnv(t, "mp")
		ctx := t.Context()

		for _, issue := range []*types.Issue{
			{ID: "mp-a", Title: "A", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
			{ID: "mp-x", Title: "X", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
			{ID: "mp-wisp-w", Title: "W", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true},
		} {
			if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
				t.Fatalf("CreateIssue %s: %v", issue.ID, err)
			}
		}

		for _, dep := range []*types.Dependency{
			{IssueID: "mp-x", DependsOnID: "mp-wisp-w", Type: types.DepBlocks},
			{IssueID: "mp-wisp-w", DependsOnID: "mp-a", Type: types.DepBlocks},
		} {
			if err := te.store.AddDependency(ctx, dep, "tester"); err != nil {
				t.Fatalf("AddDependency %s->%s: %v", dep.IssueID, dep.DependsOnID, err)
			}
		}

		err := te.store.AddDependency(ctx, &types.Dependency{IssueID: "mp-a", DependsOnID: "mp-x", Type: types.DepBlocks}, "tester")
		if err == nil {
			t.Fatal("expected mixed-table cycle detection error")
		}
		if !strings.Contains(err.Error(), "cycle") {
			t.Errorf("expected cycle error, got: %v", err)
		}
	})

	t.Run("mixed_table_cycle_wisp_endpoints", func(t *testing.T) {
		te := newTestEnv(t, "mw")
		ctx := t.Context()

		for _, issue := range []*types.Issue{
			{ID: "mw-wisp-a", Title: "A", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true},
			{ID: "mw-wisp-x", Title: "X", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask, Ephemeral: true},
			{ID: "mw-b", Title: "B", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		} {
			if err := te.store.CreateIssue(ctx, issue, "tester"); err != nil {
				t.Fatalf("CreateIssue %s: %v", issue.ID, err)
			}
		}

		for _, dep := range []*types.Dependency{
			{IssueID: "mw-wisp-x", DependsOnID: "mw-b", Type: types.DepBlocks},
			{IssueID: "mw-b", DependsOnID: "mw-wisp-a", Type: types.DepBlocks},
		} {
			if err := te.store.AddDependency(ctx, dep, "tester"); err != nil {
				t.Fatalf("AddDependency %s->%s: %v", dep.IssueID, dep.DependsOnID, err)
			}
		}

		err := te.store.AddDependency(ctx, &types.Dependency{IssueID: "mw-wisp-a", DependsOnID: "mw-wisp-x", Type: types.DepBlocks}, "tester")
		if err == nil {
			t.Fatal("expected mixed-table cycle detection error")
		}
		if !strings.Contains(err.Error(), "cycle") {
			t.Errorf("expected cycle error, got: %v", err)
		}
	})

	t.Run("cross_type_validation", func(t *testing.T) {
		te := newTestEnv(t, "ct")
		ctx := t.Context()

		epic := &types.Issue{ID: "ct-epic", Title: "Epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic}
		task := &types.Issue{ID: "ct-task", Title: "Task", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, epic, "tester"); err != nil {
			t.Fatalf("CreateIssue epic: %v", err)
		}
		if err := te.store.CreateIssue(ctx, task, "tester"); err != nil {
			t.Fatalf("CreateIssue task: %v", err)
		}

		// Task blocking epic should fail.
		dep := &types.Dependency{IssueID: "ct-task", DependsOnID: "ct-epic", Type: types.DepBlocks}
		err := te.store.AddDependency(ctx, dep, "tester")
		if err == nil {
			t.Fatal("expected cross-type error")
		}
		if !strings.Contains(err.Error(), "epics") {
			t.Errorf("expected cross-type error message, got: %v", err)
		}
	})

	t.Run("idempotent_same_type", func(t *testing.T) {
		te := newTestEnv(t, "id")
		ctx := t.Context()

		a := &types.Issue{ID: "id-a", Title: "A", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		b := &types.Issue{ID: "id-b", Title: "B", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, a, "tester"); err != nil {
			t.Fatalf("CreateIssue A: %v", err)
		}
		if err := te.store.CreateIssue(ctx, b, "tester"); err != nil {
			t.Fatalf("CreateIssue B: %v", err)
		}

		dep := &types.Dependency{IssueID: "id-a", DependsOnID: "id-b", Type: types.DepBlocks}
		if err := te.store.AddDependency(ctx, dep, "tester"); err != nil {
			t.Fatalf("AddDependency (first): %v", err)
		}
		if err := te.store.Commit(ctx, "dep"); err != nil {
			t.Fatalf("Commit: %v", err)
		}

		// Same dep again should succeed (idempotent).
		if err := te.store.AddDependency(ctx, dep, "tester"); err != nil {
			t.Fatalf("AddDependency (second): %v", err)
		}
	})

	t.Run("type_conflict", func(t *testing.T) {
		te := newTestEnv(t, "tc")
		ctx := t.Context()

		a := &types.Issue{ID: "tc-a", Title: "A", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		b := &types.Issue{ID: "tc-b", Title: "B", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, a, "tester"); err != nil {
			t.Fatalf("CreateIssue A: %v", err)
		}
		if err := te.store.CreateIssue(ctx, b, "tester"); err != nil {
			t.Fatalf("CreateIssue B: %v", err)
		}

		dep1 := &types.Dependency{IssueID: "tc-a", DependsOnID: "tc-b", Type: types.DepBlocks}
		if err := te.store.AddDependency(ctx, dep1, "tester"); err != nil {
			t.Fatalf("AddDependency blocks: %v", err)
		}
		if err := te.store.Commit(ctx, "dep"); err != nil {
			t.Fatalf("Commit: %v", err)
		}

		// Same pair, different type should error.
		dep2 := &types.Dependency{IssueID: "tc-a", DependsOnID: "tc-b", Type: types.DepParentChild}
		err := te.store.AddDependency(ctx, dep2, "tester")
		if err == nil {
			t.Fatal("expected type conflict error")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Errorf("expected 'already exists' error, got: %v", err)
		}
	})

	t.Run("source_not_found", func(t *testing.T) {
		te := newTestEnv(t, "sn")
		ctx := t.Context()

		b := &types.Issue{ID: "sn-b", Title: "B", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, b, "tester"); err != nil {
			t.Fatalf("CreateIssue B: %v", err)
		}

		dep := &types.Dependency{IssueID: "sn-ghost", DependsOnID: "sn-b", Type: types.DepBlocks}
		err := te.store.AddDependency(ctx, dep, "tester")
		if err == nil {
			t.Fatal("expected source not found error")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("expected 'not found' error, got: %v", err)
		}
	})

	t.Run("target_not_found", func(t *testing.T) {
		te := newTestEnv(t, "tn")
		ctx := t.Context()

		a := &types.Issue{ID: "tn-a", Title: "A", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, a, "tester"); err != nil {
			t.Fatalf("CreateIssue A: %v", err)
		}

		dep := &types.Dependency{IssueID: "tn-a", DependsOnID: "tn-ghost", Type: types.DepBlocks}
		err := te.store.AddDependency(ctx, dep, "tester")
		if err == nil {
			t.Fatal("expected target not found error")
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("expected 'not found' error, got: %v", err)
		}
	})

	t.Run("external_ref_skips_target_validation", func(t *testing.T) {
		te := newTestEnv(t, "er")
		ctx := t.Context()

		a := &types.Issue{ID: "er-a", Title: "A", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, a, "tester"); err != nil {
			t.Fatalf("CreateIssue A: %v", err)
		}

		// external: prefix should skip target existence check.
		dep := &types.Dependency{IssueID: "er-a", DependsOnID: "external:other-repo/issue-1", Type: types.DepBlocks}
		if err := te.store.AddDependency(ctx, dep, "tester"); err != nil {
			t.Fatalf("AddDependency with external ref: %v", err)
		}
	})

	t.Run("cross_prefix_skips_target_validation", func(t *testing.T) {
		te := newTestEnv(t, "cp")
		ctx := t.Context()

		a := &types.Issue{ID: "cp-a", Title: "A", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, a, "tester"); err != nil {
			t.Fatalf("CreateIssue A: %v", err)
		}

		// Target has a different prefix — lives in another rig's database.
		dep := &types.Dependency{IssueID: "cp-a", DependsOnID: "other-xyz", Type: types.DepBlocks}
		if err := te.store.AddDependency(ctx, dep, "tester"); err != nil {
			t.Fatalf("AddDependency cross-prefix: %v", err)
		}
	})

	t.Run("parent_child_cross_type_allowed", func(t *testing.T) {
		te := newTestEnv(t, "pc")
		ctx := t.Context()

		epic := &types.Issue{ID: "pc-epic", Title: "Epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic}
		task := &types.Issue{ID: "pc-task", Title: "Task", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, epic, "tester"); err != nil {
			t.Fatalf("CreateIssue epic: %v", err)
		}
		if err := te.store.CreateIssue(ctx, task, "tester"); err != nil {
			t.Fatalf("CreateIssue task: %v", err)
		}

		// Parent-child between epic and task should succeed (cross-type restriction
		// only applies to blocks deps).
		dep := &types.Dependency{IssueID: "pc-task", DependsOnID: "pc-epic", Type: types.DepParentChild}
		if err := te.store.AddDependency(ctx, dep, "tester"); err != nil {
			t.Fatalf("AddDependency parent-child cross-type: %v", err)
		}
	})

	t.Run("same_type_blocks_succeeds", func(t *testing.T) {
		te := newTestEnv(t, "ss")
		ctx := t.Context()

		e1 := &types.Issue{ID: "ss-e1", Title: "Epic 1", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic}
		e2 := &types.Issue{ID: "ss-e2", Title: "Epic 2", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic}
		if err := te.store.CreateIssue(ctx, e1, "tester"); err != nil {
			t.Fatalf("CreateIssue E1: %v", err)
		}
		if err := te.store.CreateIssue(ctx, e2, "tester"); err != nil {
			t.Fatalf("CreateIssue E2: %v", err)
		}

		// Epic blocking epic should succeed.
		dep := &types.Dependency{IssueID: "ss-e1", DependsOnID: "ss-e2", Type: types.DepBlocks}
		if err := te.store.AddDependency(ctx, dep, "tester"); err != nil {
			t.Fatalf("AddDependency epic-blocks-epic: %v", err)
		}
	})

	t.Run("epic_blocks_task_fails", func(t *testing.T) {
		te := newTestEnv(t, "et")
		ctx := t.Context()

		epic := &types.Issue{ID: "et-epic", Title: "Epic", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic}
		task := &types.Issue{ID: "et-task", Title: "Task", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}
		if err := te.store.CreateIssue(ctx, epic, "tester"); err != nil {
			t.Fatalf("CreateIssue epic: %v", err)
		}
		if err := te.store.CreateIssue(ctx, task, "tester"); err != nil {
			t.Fatalf("CreateIssue task: %v", err)
		}

		// Epic blocking task should fail (reverse direction of existing cross_type_validation test).
		dep := &types.Dependency{IssueID: "et-epic", DependsOnID: "et-task", Type: types.DepBlocks}
		err := te.store.AddDependency(ctx, dep, "tester")
		if err == nil {
			t.Fatal("expected cross-type error")
		}
		if !strings.Contains(err.Error(), "epics") {
			t.Errorf("expected cross-type error message, got: %v", err)
		}
	})
}

func TestDependencyOpsOnSplitTargetSchemaWithoutDependsOnID(t *testing.T) {
	skipUnlessEmbeddedDolt(t)

	ctx := t.Context()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	dataDir := filepath.Join(beadsDir, "embeddeddolt")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	db, cleanup, err := embeddeddolt.OpenSQL(ctx, dataDir, "", "")
	if err != nil {
		t.Fatalf("OpenSQL: %v", err)
	}
	t.Cleanup(func() { _ = cleanup() })

	if _, err := db.ExecContext(ctx, "CREATE DATABASE compatdb"); err != nil {
		t.Fatalf("create database: %v", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "USE compatdb"); err != nil {
		t.Fatalf("use database: %v", err)
	}
	if _, err := schema.MigrateUpTo(ctx, conn, 49); err != nil {
		t.Fatalf("MigrateUpTo(49): %v", err)
	}
	createMinimalIgnoredSplitDependencySchema(t, ctx, conn)

	assertColumnAbsent(t, ctx, conn, "dependencies", "depends_on_id")
	assertColumnAbsent(t, ctx, conn, "wisp_dependencies", "depends_on_id")

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, issue := range []*types.Issue{
		{ID: "bd-compat-parent", Title: "Parent", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeEpic},
		{ID: "bd-compat-child", Title: "Child", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
		{ID: "bd-compat-blocker", Title: "Blocker", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask},
	} {
		if err := seedCompatIssue(ctx, tx, issue); err != nil {
			t.Fatalf("seed issue %s: %v", issue.ID, err)
		}
	}

	for _, dep := range []*types.Dependency{
		{IssueID: "bd-compat-child", DependsOnID: "bd-compat-parent", Type: types.DepParentChild},
		{IssueID: "bd-compat-child", DependsOnID: "bd-compat-blocker", Type: types.DepBlocks},
	} {
		if err := issueops.AddDependencyInTx(ctx, tx, dep, "tester", issueops.AddDependencyOpts{}); err != nil {
			t.Fatalf("AddDependencyInTx(%s -> %s): %v", dep.IssueID, dep.DependsOnID, err)
		}
	}

	deps, err := issueops.GetDependencyRecordsForIssuesInTx(ctx, tx, []string{"bd-compat-child"})
	if err != nil {
		t.Fatalf("GetDependencyRecordsForIssuesInTx: %v", err)
	}
	if got := depTargets(deps["bd-compat-child"]); !sameStringSet(got, []string{"bd-compat-blocker", "bd-compat-parent"}) {
		t.Fatalf("dependency targets = %v, want blocker and parent", got)
	}

	cycles, err := issueops.DetectCyclesInTx(ctx, tx)
	if err != nil {
		t.Fatalf("DetectCyclesInTx: %v", err)
	}
	if len(cycles) != 0 {
		t.Fatalf("cycles = %v, want none", cycles)
	}

	children, err := issueops.GetChildrenOfIssuesInTx(ctx, tx, []string{"bd-compat-parent"})
	if err != nil {
		t.Fatalf("GetChildrenOfIssuesInTx: %v", err)
	}
	if !sameStringSet(children, []string{"bd-compat-child"}) {
		t.Fatalf("children = %v, want bd-compat-child", children)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func seedCompatIssue(ctx context.Context, tx *sql.Tx, issue *types.Issue) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO issues (
			id, title, description, design, acceptance_criteria, notes,
			status, priority, issue_type, created_by, owner
		) VALUES (?, ?, '', '', '', '', ?, ?, ?, 'tester', 'tester')
	`, issue.ID, issue.Title, string(issue.Status), issue.Priority, string(issue.IssueType))
	return err
}

func createMinimalIgnoredSplitDependencySchema(t *testing.T, ctx context.Context, db schema.DBConn) {
	t.Helper()

	if _, err := db.ExecContext(ctx, `
		ALTER TABLE wisps ADD COLUMN is_blocked TINYINT(1) NOT NULL DEFAULT 0
	`); err != nil && !strings.Contains(err.Error(), "Duplicate column") {
		t.Fatalf("add wisps.is_blocked: %v", err)
	}

	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS wisp_dependencies (
			id CHAR(36) NOT NULL PRIMARY KEY,
			issue_id VARCHAR(255) NOT NULL,
			depends_on_issue_id VARCHAR(255) NULL,
			depends_on_wisp_id VARCHAR(255) NULL,
			depends_on_external VARCHAR(255) NULL,
			type VARCHAR(32) NOT NULL DEFAULT 'blocks',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			created_by VARCHAR(255) NOT NULL,
			metadata JSON DEFAULT (JSON_OBJECT()),
			thread_id VARCHAR(255) DEFAULT '',
			UNIQUE KEY uk_wisp_dep_issue_target (issue_id, depends_on_issue_id),
			UNIQUE KEY uk_wisp_dep_wisp_target (issue_id, depends_on_wisp_id),
			UNIQUE KEY uk_wisp_dep_external_target (issue_id, depends_on_external)
		)
	`)
	if err != nil {
		t.Fatalf("create wisp_dependencies: %v", err)
	}
}

func assertColumnAbsent(t *testing.T, ctx context.Context, db schema.DBConn, table, column string) {
	t.Helper()

	var count int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM INFORMATION_SCHEMA.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?
	`, table, column).Scan(&count); err != nil {
		t.Fatalf("column probe %s.%s: %v", table, column, err)
	}
	if count != 0 {
		t.Fatalf("%s.%s exists, want absent", table, column)
	}
}

func depTargets(deps []*types.Dependency) []string {
	targets := make([]string, 0, len(deps))
	for _, dep := range deps {
		targets = append(targets, dep.DependsOnID)
	}
	return targets
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	counts := make(map[string]int, len(got))
	for _, v := range got {
		counts[v]++
	}
	for _, v := range want {
		if counts[v] == 0 {
			return false
		}
		counts[v]--
	}
	return true
}
