package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertConflictUpdateSubqueries(t *testing.T) {
	for _, source := range []string{"VALUES(2,20),(1,11)", "SELECT k,v FROM input_rows ORDER BY k DESC"} {
		t.Run("statement_snapshot_"+source, func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
				run(fixture.Exec("CREATE TABLE input_rows(k INT,v INT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(3,30)", 2))
				run(fixture.Exec("INSERT INTO input_rows VALUES(2,20),(1,11)", 2))
				// The first proposed row is a new identity. Neither scalar consumer may
				// see that private stage; both read the original two-row command snapshot.
				q := "INSERT INTO t AS dst " + source + " ON CONFLICT(k) DO UPDATE SET v=(SELECT count(*) FROM t) WHERE (SELECT count(*) FROM t)=2"
				run(fixture.Exec(q, 2))
				run(conflictUpdateRows([]any{int64(1), int64(2)}, []any{int64(2), int64(20)}, []any{int64(3), int64(30)}))
			})
		})
	}
	t.Run("correlated_target_excluded", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,10),(3,30)", 2))
			run(fixture.Exec("INSERT INTO t AS dst VALUES(2,20),(1,11) ON CONFLICT(k) DO UPDATE SET v=(SELECT dst.v+excluded.v+(SELECT v FROM t WHERE k=3)) WHERE (SELECT dst.k=excluded.k)", 2))
			run(conflictUpdateRows([]any{int64(1), int64(51)}, []any{int64(2), int64(20)}, []any{int64(3), int64(30)}))
		})
	})
	for _, condition := range []string{"FALSE", "NULL"} {
		t.Run("filtered_set_demand_"+condition, func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
				run(fixture.Exec("CREATE TABLE scalar_input(v INT,enabled BOOLEAN)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
				run(fixture.Exec("INSERT INTO scalar_input VALUES(1,"+condition+")", 1))
				q := "INSERT INTO t VALUES(1,11) ON CONFLICT(k) DO UPDATE SET v=(SELECT 1/(scalar_input.v-scalar_input.v) FROM scalar_input) WHERE (SELECT enabled FROM scalar_input)"
				run(fixture.Exec(q, 0))
				run(conflictUpdateRows([]any{int64(1), int64(10)}))
				// The same SET subquery is also not demanded on an ordinary insert path.
				run(fixture.Exec("INSERT INTO t VALUES(2,20) ON CONFLICT(k) DO UPDATE SET v=(SELECT 1/(scalar_input.v-scalar_input.v) FROM scalar_input) WHERE (SELECT enabled FROM scalar_input)", 1))
				run(fixture.Exec("UPDATE scalar_input SET enabled=TRUE", 1))
				run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22012", "division by zero")))
				run(conflictUpdateRows([]any{int64(1), int64(10)}, []any{int64(2), int64(20)}))
			})
		})
	}
	t.Run("scalar_cardinality_atomic", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
			run(fixture.Exec("CREATE TABLE scalar_input(v INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
			run(fixture.Exec("INSERT INTO scalar_input VALUES(5),(6)", 2))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(2,20),(1,11) ON CONFLICT(k) DO UPDATE SET v=(SELECT v FROM scalar_input)", fixture.ControlsReferenceDiagnostic("21000", "more than one row returned by a subquery used as an expression")))
			run(conflictUpdateRows([]any{int64(1), int64(10)}))
		})
	})
	t.Run("held_parameters_fresh_frames", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(k INT UNIQUE,v INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
			q := "INSERT INTO t AS dst VALUES($1,0) ON CONFLICT(k) DO UPDATE SET v=(SELECT COALESCE(dst.v,0)+$2::INT) WHERE (SELECT $3::BOOLEAN)"
			statement, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: statement, SQL: q}
			for _, c := range []struct {
				k                    int64
				increment, condition any
				count                int64
				value                any
			}{
				{1, int64(2), true, 1, int64(12)},
				{1, int64(100), false, 0, int64(12)},
				{1, nil, true, 1, nil},
				{1, int64(7), true, 1, int64(7)},
			} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, c.count, c.k, c.increment, c.condition))
				run(conflictUpdateRows([]any{int64(1), c.value}))
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, int64(2), int64(99), false))
			run(conflictUpdateRows([]any{int64(1), int64(7)}, []any{int64(2), int64(0)}))
		})
	})
}
