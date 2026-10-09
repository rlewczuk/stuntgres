//go:build reference

package reference_test

import "testing"

// Independently specified expectations, not claims of observed results.
// This entrypoint executes PostgreSQL SQL only; native fixtures are not queried.
func TestReferenceStringCoreOracle(t *testing.T) {
	p := newReferencePair(t)
	stringCoreOracleParameterTypes(t, p)
	for _, tc := range []stringCoreReferenceCase{
		{`SELECT 'x'::text||('\377'::"char")::text AS v`, `x\377`, "TEXT"},
		{`SELECT ('\200'::"char")::text||'x'::text AS v`, `\200x`, "TEXT"},
		{`SELECT 'x'::text||('\000'::"char")::text AS v`, "x", "TEXT"},
	} {
		step := stringCoreSelect(tc)
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, step)
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(step))
	}

	// Parameter inference is checked independently via PostgreSQL's logical type function.
	for _, q := range []string{"SELECT pg_typeof($1||$2)::text AS v", "SELECT pg_typeof($1||42)::text AS v", "SELECT pg_typeof(42||$1)::text AS v"} {
		args := []any{"x"}
		if q == "SELECT pg_typeof($1||$2)::text AS v" {
			args = append(args, "y")
		}
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referencePrepared(referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{"text"}}, args...)))
	}
}

func stringCoreOracleParameterTypes(t *testing.T, p *referencePair) {
	c, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, tc := range []struct{ name, expression, want string }{
		{"string_core_two", "$1||$2", "{text,text}"},
		{"string_core_left", "$1||42", "{text}"},
		{"string_core_right", "42||$1", "{text}"},
		{"string_core_text", "'x'::text||$1", "{text}"},
	} {
		if _, err = c.ExecContext(p.ctx, "PREPARE "+tc.name+" AS SELECT "+tc.expression); err != nil {
			t.Fatal(err)
		}
		var got string
		err = c.QueryRowContext(p.ctx, "SELECT parameter_types::text FROM pg_catalog.pg_prepared_statements WHERE name=$1", tc.name).Scan(&got)
		if _, e := c.ExecContext(p.ctx, "DEALLOCATE "+tc.name); e != nil {
			t.Error(e)
		}
		if err != nil || got != tc.want {
			t.Fatal(tc.name, got, tc.want, err)
		}
	}
}
