package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func viewWriteDiscoveryRun(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool, s fixture.Step) {
	t.Helper()
	if s.State != "" {
		viewWriteRun(t, p, b, held, s)
		return
	}
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
	var x fixture.Executor = b.DB
	if held {
		stmt, err := b.DB.PrepareContext(ctx, s.Query)
		if err != nil {
			t.Fatalf("discovery Prepare %s: %#v", s.Query, fixture.ExtractDiagnostic(t, err))
		}
		defer stmt.Close()
		x = fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: s.Query}
	}
	result, err := x.ExecContext(ctx, s.Query, s.Args...)
	if err != nil {
		t.Fatalf("discovery Execute %s: %#v", s.Query, fixture.ExtractDiagnostic(t, err))
	}
	fixture.LockAffected(t, result, s.Affected)
	if _, err := result.LastInsertId(); err == nil {
		t.Fatal("LastInsertId unexpectedly supported")
	}
}

// Independently specified cast, row-mark and demand expectations were observed
// on the pinned oracle. Keep complete diagnostics on unexpected failures.
func TestReferenceViewUpdatableDiscovery(t *testing.T) {
	for _, shape := range []struct {
		name, projection string
		writable         bool
	}{
		{"noop_cast", "CAST(a AS INT) AS x", true},
		{"different_cast", "CAST(a AS BIGINT) AS x", false},
		{"collate", "txt COLLATE \"C\" AS x", false},
	} {
		t.Run(shape.name, func(t *testing.T) {
			viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s fixture.Step) { viewWriteDiscoveryRun(t, p, b, held, s) }
				run(fixture.Exec("CREATE TABLE t(id INT,a INT,txt TEXT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10,'old')", 1))
				run(fixture.Exec("CREATE VIEW v AS SELECT id,"+shape.projection+" FROM t", 0))
				q := "UPDATE v SET x=11"
				if shape.name == "collate" {
					q = "UPDATE v SET x='new'"
				}
				if shape.writable {
					run(fixture.Exec(q, 1))
				} else {
					run(viewWriteComputed(q, "UPDATE", "v", "x"))
				}
			})
		})
	}
	for _, mark := range []string{"FOR UPDATE", "FOR SHARE", "FOR NO KEY UPDATE", "FOR KEY SHARE"} {
		t.Run(mark, func(t *testing.T) {
			viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s fixture.Step) { viewWriteDiscoveryRun(t, p, b, held, s) }
				run(fixture.Exec("CREATE TABLE t(id INT,a INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				run(fixture.Exec("CREATE VIEW v AS SELECT id,a FROM t "+mark, 0))
				run(fixture.Exec("UPDATE v SET a=11", 1))
				run(fixture.Exec("INSERT INTO v VALUES(2,20)", 1))
				run(fixture.Exec("DELETE FROM v", 2))
			})
		})
	}
	t.Run("constant_unused", func(t *testing.T) {
		viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
			run := func(s fixture.Step) { viewWriteDiscoveryRun(t, p, b, held, s) }
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
			run(fixture.Exec("CREATE VIEW v AS SELECT id,1/0 AS unused FROM t", 0))
			run(fixture.Exec("UPDATE v SET id=2", 1))
			run(fixture.Exec("DELETE FROM v", 1))
		})
	})
}
