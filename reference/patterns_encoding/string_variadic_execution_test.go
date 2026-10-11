package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringVariadicFirstExecution(t *testing.T) {
	formatReferenceScenario(t, stringVariadicFirstExecution)
}

// Prepare occurs under off/hex, but the first execution occurs under on/escape.
// Immutable physical literals retain that first execution's values; parameters,
// stable concat and format continue to consume each execution's settings.
func stringVariadicFirstExecution(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET quote_all_identifiers=off`, 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET bytea_output=hex`, 0))
	literal := `SELECT quote_ident('foo') AS identifier,quote_literal('\x00015c'::bytea) AS literal,quote_nullable('\x00015c'::bytea) AS nullable,concat('\x00015c'::bytea) AS raw,format('%I','foo') AS formatted`
	parameter := `SELECT quote_ident($1::text) AS identifier,quote_literal($2::bytea) AS literal,quote_nullable($2::bytea) AS nullable,concat($2::bytea) AS raw,format('%I',$1::text) AS formatted`
	ls, err := c.PrepareContext(p.Ctx, literal)
	if err != nil {
		t.Fatal(err)
	}
	defer ls.Close()
	ps, err := c.PrepareContext(p.Ctx, parameter)
	if err != nil {
		t.Fatal(err)
	}
	defer ps.Close()
	columns := []string{"identifier", "literal", "nullable", "raw", "formatted"}
	types := []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}
	for _, state := range []struct{ ident, bytea, quoted, raw, formatted string }{
		{"on", "escape", `E'\\000\\001\\\\'`, `\000\001\\`, `"foo"`},
		{"off", "hex", `E'\\x00015c'`, `\x00015c`, "foo"},
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET quote_all_identifiers="+state.ident, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET bytea_output="+state.bytea, 0))
		for range 2 {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: ls, SQL: literal}, b.Recorder, fixture.Select(literal, columns, types, [][]any{{`"foo"`, `E'\\000\\001\\\\'`, `E'\\000\\001\\\\'`, state.raw, state.formatted}}))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: ps, SQL: parameter}, b.Recorder, fixture.Select(parameter, columns, types, [][]any{{state.formatted, state.quoted, state.quoted, state.raw, state.formatted}}, "foo", []byte{0, 1, 92}))
		}
		// New direct/prepared executions capture the current settings, not a global cache.
		formatReferenceBoth(t, p, b, c, fixture.Select(literal, columns, types, [][]any{{state.formatted, state.quoted, state.quoted, state.raw, state.formatted}}))
	}
}
