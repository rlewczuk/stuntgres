package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGEnumVisibilityAndLifetime(t *testing.T) {
	pgEnumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		pgEnumExec(t, p, b, a, "CREATE SCHEMA pge_life")
		defer a.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS pge_life CASCADE")
		pgEnumExec(t, p, b, a, "BEGIN")
		defer a.ExecContext(p.Ctx, "ROLLBACK")
		id := pgEnumCreate(t, p, b, a, "pge_life.mood", "'old','last'")
		initial := pgEnumQuery(t, p, b, a, pgEnumProbe, id)
		pgEnumCheck(t, initial, id, []string{"old", "last"}, []float64{1, 2})
		pgEnumCheck(t, pgEnumQuery(t, p, b, peer, pgEnumProbe, id), id, nil, nil)
		pgEnumExec(t, p, b, a, "COMMIT")
		pgEnumExec(t, p, b, a, "BEGIN")
		pgEnumExec(t, p, b, a, "ALTER TYPE pge_life.mood ADD VALUE 'middle' BEFORE 'last'")
		outer := pgEnumQuery(t, p, b, a, pgEnumProbe, id)
		pgEnumCheck(t, outer, id, []string{"old", "middle", "last"}, []float64{1, 1.5, 2})
		pgEnumCheck(t, pgEnumQuery(t, p, b, peer, pgEnumProbe, id), id, []string{"old", "last"}, []float64{1, 2})
		pgEnumExec(t, p, b, a, "SAVEPOINT child")
		pgEnumExec(t, p, b, a, "ALTER TYPE pge_life.mood RENAME VALUE 'old' TO 'renamed'")
		pgEnumExec(t, p, b, a, "ALTER TYPE pge_life.mood ADD VALUE 'child'")
		childID := pgEnumCreate(t, p, b, a, "pge_life.child", "'child'")
		pgEnumCheck(t, pgEnumQuery(t, p, b, a, pgEnumProbe, id), id, []string{"renamed", "middle", "last", "child"}, []float64{1, 1.5, 2, 3})
		pgEnumExec(t, p, b, a, "ROLLBACK TO child")
		if got := pgEnumQuery(t, p, b, a, pgEnumProbe, id); !reflect.DeepEqual(got, outer) {
			t.Fatal("savepoint restore", got, outer)
		}
		pgEnumCheck(t, pgEnumQuery(t, p, b, a, pgEnumProbe, childID), childID, nil, nil)
		pgEnumExec(t, p, b, a, "SAVEPOINT unsafe_use")
		s := fixture.ImplicitError("SELECT 'middle'::pge_life.mood", fixture.Diagnostic{Code: "55P04", Message: `unsafe use of new value "middle" of enum type pge_life.mood`, Hint: "New enum values must be committed before they can be used.", Position: "8"})
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
		pgEnumExec(t, p, b, a, "ROLLBACK TO unsafe_use")
		rollbackRows, rollbackErr := peer.QueryContext(p.Ctx, pgEnumProbe, id)
		if rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		defer rollbackRows.Close()
		pgEnumExec(t, p, b, a, "ROLLBACK")
		if got := pgEnumQuery(t, p, b, a, pgEnumProbe, id); !reflect.DeepEqual(got, initial) {
			t.Fatal("full rollback", got, initial)
		}
		if got := pgEnumRead(t, rollbackRows, b.PG); !reflect.DeepEqual(got, initial) {
			t.Fatal("retained through rollback", got, initial)
		}
		stmt, err := a.PrepareContext(p.Ctx, pgEnumProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: pgEnumProbe}
		old, err := a.QueryContext(p.Ctx, pgEnumProbe, id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		pgEnumExec(t, p, b, peer, "BEGIN")
		pgEnumExec(t, p, b, peer, "ALTER TYPE pge_life.mood RENAME VALUE 'old' TO 'renamed'")
		pgEnumExec(t, p, b, peer, "ALTER TYPE pge_life.mood ADD VALUE 'middle' BEFORE 'last'")
		pgEnumExec(t, p, b, peer, "COMMIT")
		if got := pgEnumRead(t, old, b.PG); !reflect.DeepEqual(got, initial) {
			t.Fatal("retained before commit", got, initial)
		}
		current := pgEnumQuery(t, p, b, held, pgEnumProbe, id)
		pgEnumCheck(t, current, id, []string{"renamed", "middle", "last"}, []float64{1, 1.5, 2})
		pgEnumStable(t, initial, current, map[string]string{"old": "renamed"}, true)
		old, err = a.QueryContext(p.Ctx, pgEnumProbe, id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		pgEnumExec(t, p, b, peer, "DROP SCHEMA pge_life CASCADE")
		if got := pgEnumRead(t, old, b.PG); !reflect.DeepEqual(got, current) {
			t.Fatal("retained after retirement", got, current)
		}
		pgEnumCheck(t, pgEnumQuery(t, p, b, held, pgEnumProbe, id), id, nil, nil)
	})
}

func TestReferencePGEnumFeatureSmoke(t *testing.T) {
	pgEnumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		id := pgEnumCreate(t, p, b, a, "pge_smoke", "'z','q'")
		pgEnumExec(t, p, b, a, "CREATE TABLE pge_smoke_values(v pge_smoke)")
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO pge_smoke_values VALUES ('z'),('q')", 2))
		initial := pgEnumQuery(t, p, b, a, pgEnumProbe, id)
		pgEnumCheck(t, initial, id, []string{"z", "q"}, []float64{1, 2})
		// Keep the held statement on the peer: pq cannot execute another
		// command on the physical session that owns an unconsumed Rows.
		stmt, err := peer.PrepareContext(p.Ctx, pgEnumProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: peer, Statement: stmt, SQL: pgEnumProbe}
		old, err := a.QueryContext(p.Ctx, pgEnumProbe, id)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		for _, q := range []string{"ALTER TYPE pge_smoke ADD VALUE 'a' BEFORE 'q'", "ALTER TYPE pge_smoke ADD VALUE 'first' BEFORE 'z'", "ALTER TYPE pge_smoke RENAME VALUE 'q' TO 'end'"} {
			pgEnumExec(t, p, b, peer, q)
		}
		current := pgEnumQuery(t, p, b, held, pgEnumProbe, id)
		pgEnumCheck(t, current, id, []string{"first", "z", "a", "end"}, []float64{0, 1, 1.5, 2})
		pgEnumStable(t, initial, current, map[string]string{"q": "end"}, true)
		if got := pgEnumRead(t, old, b.PG); !reflect.DeepEqual(got, initial) {
			t.Fatal("smoke retained", got, initial)
		}
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO pge_smoke_values VALUES ('first'),('a')", 2))
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT v::text AS label FROM pge_smoke_values ORDER BY v", []string{"label"}, []string{"TEXT"}, [][]any{{"first"}, {"z"}, {"a"}, {"end"}}))
	})
}
