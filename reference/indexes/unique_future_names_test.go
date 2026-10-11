package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUniqueFutureNames(t *testing.T) {
	for _, c := range []struct {
		name, query            string
		constraints, relations [][]any
	}{
		{"forward", "CREATE TABLE t(a INT UNIQUE,b INT CONSTRAINT t_a_key UNIQUE)", nil, nil},
		{"reverse", "CREATE TABLE t(b INT CONSTRAINT t_a_key UNIQUE,a INT UNIQUE)",
			[][]any{{"t_a_key", "u", nil, "UNIQUE (b)", "UNIQUE (b)"}, {"t_a_key1", "u", nil, "UNIQUE (a)", "UNIQUE (a)"}},
			[][]any{{"t", "r", int64(2)}, {"t_a_key", "i", int64(1)}, {"t_a_key1", "i", int64(1)}}},
		{"redundant_transfer", "CREATE TABLE t(a INT UNIQUE,b INT CONSTRAINT t_a_key UNIQUE,CONSTRAINT transferred UNIQUE(a))",
			[][]any{{"t_a_key", "u", nil, "UNIQUE (b)", "UNIQUE (b)"}, {"transferred", "u", nil, "UNIQUE (a)", "UNIQUE (a)"}},
			[][]any{{"t", "r", int64(2)}, {"t_a_key", "i", int64(1)}, {"transferred", "i", int64(1)}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
					if c.name == "forward" {
						run(fixture.ImplicitError(c.query, fixture.Diagnostic{Code: "42P07", Message: `relation "t_a_key" already exists`}))
					} else {
						run(fixture.Exec(c.query, 0))
					}
					namespace := fixture.UniqueReferencePublicNamespace(t, p.Ctx, backend.DB, backend.PG)
					run(fixture.Select("SELECT c.conname,c.contype,c.conbin,pg_get_constraintdef(c.oid),pg_get_constraintdef(c.oid,true) FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid WHERE r.relnamespace=$1 ORDER BY c.conname",
						[]string{"conname", "contype", "conbin", "pg_get_constraintdef", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "PG_NODE_TREE", "TEXT", "TEXT"}, c.constraints, namespace))
					run(fixture.Select("SELECT relname,relkind,relnatts FROM pg_class WHERE relnamespace=$1 ORDER BY relname",
						[]string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, c.relations, namespace))
				})
			}
		})
	}
}
