package reference_test

import "testing"

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
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
					if c.name == "forward" {
						run(referenceImplicitError(c.query, referenceDiagnostic{Code: "42P07", Message: `relation "t_a_key" already exists`}))
					} else {
						run(referenceExec(c.query, 0))
					}
					namespace := uniqueReferencePublicNamespace(t, p.ctx, backend.db, backend.pg)
					run(referenceSelect("SELECT c.conname,c.contype,c.conbin,pg_get_constraintdef(c.oid),pg_get_constraintdef(c.oid,true) FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid WHERE r.relnamespace=$1 ORDER BY c.conname",
						[]string{"conname", "contype", "conbin", "pg_get_constraintdef", "pg_get_constraintdef"}, []string{"NAME", "CHAR", "PG_NODE_TREE", "TEXT", "TEXT"}, c.constraints, namespace))
					run(referenceSelect("SELECT relname,relkind,relnatts FROM pg_class WHERE relnamespace=$1 ORDER BY relname",
						[]string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, c.relations, namespace))
				})
			}
		})
	}
}
