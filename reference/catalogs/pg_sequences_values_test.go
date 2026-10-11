package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func pgSequencesScalar(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q, label string, n int64) {
	t.Helper()
	fixture.PgDescriptionExecutorRows(t, p, b, e, fixture.Select(q, []string{label}, []string{"INT8"}, [][]any{{n}}))
}

func TestReferencePGSequencesValues(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			owner := pgSequencesOwner(t, p, a)
			pgSequenceExec(t, p, b, a, "CREATE SEQUENCE pgsv_values")
			row := pgSequencesDefault(owner, "pgsv_values", nil)
			pgSequencesCheck(t, p, b, a, "pgsv_values", row)
			for _, q := range []string{"SELECT currval('pgsv_values')", "SELECT lastval()"} {
				message := `currval of sequence "pgsv_values" is not yet defined in this session`
				if q == "SELECT lastval()" {
					message = "lastval is not yet defined in this session"
				}
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "55000", Message: message})
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
			}
			for _, c := range []struct {
				q, label string
				n        int64
				last     any
			}{
				{"SELECT nextval('pgsv_values')", "nextval", 1, int64(1)},
				{"SELECT setval('pgsv_values',42,true)", "setval", 42, int64(42)},
				{"SELECT nextval('pgsv_values')", "nextval", 43, int64(43)},
				{"SELECT setval('pgsv_values',42,false)", "setval", 42, nil},
				{"SELECT nextval('pgsv_values')", "nextval", 42, int64(42)},
			} {
				pgSequencesScalar(t, p, b, a, c.q, c.label, c.n)
				row[10] = c.last
				pgSequencesCheck(t, p, b, peer, "pgsv_values", row)
			}
			pgSequenceExec(t, p, b, a, "ALTER SEQUENCE pgsv_values START 7")
			row[4] = int64(7)
			pgSequencesCheck(t, p, b, a, "pgsv_values", row)
			old, err := a.QueryContext(p.Ctx, pgSequencesProbe, "pgsv_values")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			pgSequencesScalar(t, p, b, peer, "SELECT nextval('pgsv_values')", "nextval", 43)
			pgSequenceExec(t, p, b, peer, "DROP SEQUENCE pgsv_values")
			pgSequencesMetadata(t, old, b.PG)
			if !old.Next() {
				t.Fatal("retained row missing", old.Err())
			}
			var oldLast int64
			var schema, name, role, typ string
			var start, min, max, inc, cache int64
			var cycle bool
			if err := old.Scan(&schema, &name, &role, &typ, &start, &min, &max, &inc, &cycle, &cache, &oldLast); err != nil {
				t.Fatal(err)
			}
			if schema != "public" || name != "pgsv_values" || role != owner || typ != "bigint" || start != 7 || min != 1 || max != 9223372036854775807 || inc != 1 || cycle || cache != 1 || oldLast != 42 {
				t.Fatal("retained values changed", schema, name, role, typ, start, min, max, inc, cycle, cache, oldLast)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal("retained cardinality", old.Err())
			}
			pgSequencesCheck(t, p, b, a, "pgsv_values", nil)
			pgSequenceExec(t, p, b, a, "CREATE SEQUENCE pgsv_cached CACHE 10")
			cached := pgSequencesDefault(owner, "pgsv_cached", nil)
			cached[9] = int64(10)
			for _, c := range []struct {
				peer     bool
				q, label string
				n, high  int64
			}{
				{false, "SELECT nextval('pgsv_cached')", "nextval", 1, 10},
				{true, "SELECT nextval('pgsv_cached')", "nextval", 11, 20},
				{false, "SELECT nextval('pgsv_cached')", "nextval", 2, 20},
				{true, "SELECT setval('pgsv_cached',50,true)", "setval", 50, 50},
				{false, "SELECT nextval('pgsv_cached')", "nextval", 3, 50},
				{true, "SELECT nextval('pgsv_cached')", "nextval", 51, 60},
			} {
				e := a
				if c.peer {
					e = peer
				}
				pgSequencesScalar(t, p, b, e, c.q, c.label, c.n)
				cached[10] = c.high
				pgSequencesCheck(t, p, b, a, "pgsv_cached", cached)
			}
			pgSequenceExec(t, p, b, a, "CREATE SEQUENCE pgsv_desc INCREMENT -2 MINVALUE -20 MAXVALUE -1 START -1 CACHE 3")
			pgSequencesScalar(t, p, b, a, "SELECT nextval('pgsv_desc')", "nextval", -1)
			pgSequencesCheck(t, p, b, a, "pgsv_desc", []any{"public", "pgsv_desc", owner, "bigint", int64(-1), int64(-20), int64(-1), int64(-2), false, int64(3), int64(-5)})
			pgSequenceExec(t, p, b, a, "CREATE SEQUENCE pgsv_cycle MINVALUE 1 MAXVALUE 5 START 4 CACHE 3 CYCLE")
			for _, c := range []struct{ n, high int64 }{{4, 5}, {5, 5}, {1, 3}} {
				pgSequencesScalar(t, p, b, a, "SELECT nextval('pgsv_cycle')", "nextval", c.n)
				pgSequencesCheck(t, p, b, a, "pgsv_cycle", []any{"public", "pgsv_cycle", owner, "bigint", int64(4), int64(1), int64(5), int64(1), true, int64(3), c.high})
			}
			pgSequenceExec(t, p, b, a, "CREATE VIEW pgsv_value_view AS SELECT sequencename,last_value FROM pg_sequences")
			fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Select("SELECT v.last_value FROM pgsv_value_view v JOIN pg_class c ON c.relname=v.sequencename WHERE v.sequencename='pgsv_cached'", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(60)}}))
			pgSequenceExec(t, p, b, a, "CREATE TABLE pgsv_value_sink(v BIGINT)")
			fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Exec("INSERT INTO pgsv_value_sink SELECT last_value FROM pg_sequences WHERE sequencename='pgsv_cached'", 1))
			fixture.PgDescriptionExecutorRows(t, p, b, a, fixture.Select("SELECT v FROM pgsv_value_sink", []string{"v"}, []string{"INT8"}, [][]any{{int64(60)}}))
		})
	}
}
