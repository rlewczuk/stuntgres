//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Independently specified expectations, not claims of observed results.
// This entrypoint executes PostgreSQL SQL only; native fixtures are not queried.
func TestReferenceStringCoreOracle(t *testing.T) {
	p := fixture.NewPair(t)
	stringCoreOracleParameterTypes(t, p)
	for _, tc := range []stringCoreReferenceCase{
		{`SELECT 'x'::text||('\377'::"char")::text AS v`, `x\377`, "TEXT"},
		{`SELECT ('\200'::"char")::text||'x'::text AS v`, `\200x`, "TEXT"},
		{`SELECT 'x'::text||('\000'::"char")::text AS v`, "x", "TEXT"},
	} {
		step := stringCoreSelect(tc)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(step))
	}

	// Parameter inference is checked independently via PostgreSQL's logical type function.
	for _, q := range []string{"SELECT pg_typeof($1||$2)::text AS v", "SELECT pg_typeof($1||42)::text AS v", "SELECT pg_typeof(42||$1)::text AS v"} {
		args := []any{"x"}
		if q == "SELECT pg_typeof($1||$2)::text AS v" {
			args = append(args, "y")
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"text"}}, args...)))
	}
}

func stringCoreOracleParameterTypes(t *testing.T, p *fixture.Pair) {
	c, err := p.Postgres.Conn(p.Ctx)
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
		if _, err = c.ExecContext(p.Ctx, "PREPARE "+tc.name+" AS SELECT "+tc.expression); err != nil {
			t.Fatal(err)
		}
		var got string
		err = c.QueryRowContext(p.Ctx, "SELECT parameter_types::text FROM pg_catalog.pg_prepared_statements WHERE name=$1", tc.name).Scan(&got)
		if _, e := c.ExecContext(p.Ctx, "DEALLOCATE "+tc.name); e != nil {
			t.Error(e)
		}
		if err != nil || got != tc.want {
			t.Fatal(tc.name, got, tc.want, err)
		}
	}
}
