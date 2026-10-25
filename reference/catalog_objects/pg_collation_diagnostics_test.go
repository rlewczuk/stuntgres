package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

var pgCollationDiagnosticCases = []struct{ query, code, message, position string }{
	{"SELECT missing FROM pg_catalog.pg_collation", "42703", "column \"missing\" does not exist", "8"},
	{"SELECT missing FROM pg_catalog.pg_collation WHERE FALSE", "42703", "column \"missing\" does not exist", "8"},
	{"SELECT /*é*/ missing FROM pg_catalog.pg_collation", "42703", "column \"missing\" does not exist", "14"},
	{"SELECT collname FROM pg_catalog.pg_collation_missing", "42P01", "relation \"pg_catalog.pg_collation_missing\" does not exist", "22"},
	{"SELECT 'bad'::oid FROM pg_collation WHERE FALSE", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
	{"SELECT '4294967296'::oid FROM pg_collation", "22003", "value \"4294967296\" is out of range for type oid", "8"},
}

func TestReferencePGCollationDiagnostics(t *testing.T) {
	pgCollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range pgCollationDiagnosticCases {
			s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
			s.SelectRows = true
			fixture.CollationRoutes(t, p, b, s)
		}
		q := "SELECT collname FROM pg_catalog.pg_collation WHERE oid=$1"
		held := fixture.ViewHold(t, p, b, q)
		for _, value := range []string{"bad", ""} {
			for _, plain := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					s := fixture.PGCastParameterError(q, "oid", value, value)
					s.Prepared, s.NonContext = prepared, plain
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.PGCastParameterError(q, "oid", value, value))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"collname"}, []string{"NAME"}, [][]any{{"pg_c_utf8"}}, int64(811)))
		}
		for _, value := range []any{nil, int64(4294967295)} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"collname"}, []string{"NAME"}, nil, value))
		}
		if !b.PG {
			// Virtual-source locking is unsupported even with no visible rows.
			// Catalog mutation already has the sibling authorization witness.
			for _, q := range []string{"SELECT * FROM pg_collation WHERE oid=811 FOR UPDATE", "SELECT * FROM pg_collation WHERE FALSE FOR UPDATE"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.CollationRoutes(t, p, b, s)
			}
		}
	})
}
