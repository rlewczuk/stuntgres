//go:build reference

package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceAlterRenameCancellation(t *testing.T) {
	for _, tc := range []struct{ q, table, column string }{
		{"ALTER TABLE t RENAME a TO renamed", "t", "renamed"},
		{"ALTER TABLE t RENAME TO renamed_table", "renamed_table", "a"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			p := fixture.NewPair(t)
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			a, b := fixture.LockConnections(t, ctx, p.Postgres)
			fixture.LockExec(t, ctx, a, "CREATE TABLE t(a INT CONSTRAINT nn NOT NULL,b TEXT)", 0)
			fixture.LockExec(t, ctx, a, "INSERT INTO t VALUES(7,'keep')", 1)
			var oid int64
			if err := fixture.Admin.QueryRowContext(ctx, "SELECT 't'::regclass::oid").Scan(&oid); err != nil {
				t.Fatal(err)
			}
			var pid int
			if err := b.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			holder, err := a.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			fixture.LockExec(t, ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
			// Successful Prepare behind AX establishes that cancellation belongs to
			// utility execution, never metadata-only preparation.
			stmt, err := b.PrepareContext(ctx, tc.q)
			if err != nil {
				t.Fatal("rename Prepare waited/failed", err)
			}
			defer stmt.Close()
			done := make(chan fixture.RelationOutcome, 1)
			go func() { r, err := stmt.ExecContext(ctx); done <- fixture.RelationOutcome{Result: r, Err: err} }()
			fixture.SchemaShadowWait(t, ctx, pid, "t")
			var canceled bool
			if err = fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
				t.Fatal(canceled, err)
			}
			out := fixture.LockResult(t, done)
			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"}
			fixture.AssertDiagnostic(t, out.Err, &want)
			if out.Result != nil {
				t.Fatal("canceled utility exposed result")
			}
			var pending int
			if err = fixture.Admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_locks WHERE pid=$1 AND NOT granted", pid).Scan(&pending); err != nil || pending != 0 {
				t.Fatal("canceled waiter remained", pending, err)
			}
			// Catalog inspection is not blocked by the retained table AX. It proves
			// neither relation nor attribute was relabeled while cancellation completed.
			var relation, column string
			if err = fixture.Admin.QueryRowContext(ctx, "SELECT relname,(SELECT attname FROM pg_attribute WHERE attrelid=pg_class.oid AND attnum=1) FROM pg_class WHERE oid=$1", oid).Scan(&relation, &column); err != nil || relation != "t" || column != "a" {
				t.Fatal("canceled rename changed catalog", relation, column, err)
			}
			if err = holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			record := &fixture.Recorder{}
			fixture.RunStep(t, ctx, b, record, fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "keep"}}))
			// Reuse the same canceled prepared handle and physical session after release.
			r, err := stmt.ExecContext(ctx)
			if err != nil {
				t.Fatal("canceled handle/session not reusable", err)
			}
			fixture.LockAffected(t, r, 0)
			fixture.RunStep(t, ctx, b, record, fixture.Prepared(fixture.Select("SELECT * FROM "+tc.table, []string{tc.column, "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "keep"}})))
		})
	}
}
