package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Expected values are independently specified from REL_18_0 and F.16.4/F.16.5.
// Vectors and diagnostics verified against the pinned PostgreSQL 18.0 oracle.
func metaphoneCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"phon_reader", "phon_owner"})
	fixture.SchemaReferenceCleanup(t, "phon_addon")
	fixture.SchemaReferenceCleanup(t, "phon_smoke")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP EXTENSION IF EXISTS fuzzystrmatch CASCADE", "DROP SCHEMA IF EXISTS phon_addon CASCADE", "DROP SCHEMA IF EXISTS phon_smoke CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			body(t, p, b, c)
		})
	}
}

func metaphoneSelect(q, label string, want any, args ...any) fixture.Step {
	s := fixture.Select(q, []string{label}, []string{"TEXT"}, [][]any{{want}}, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "TEXT" || meta[0].ScanType() != reflect.TypeFor[string]() {
			t.Fatal("TEXT descriptor", meta, err)
		}
		length, lengthKnown := meta[0].Length()
		if !lengthKnown || length != int64(9223372036854775807) {
			t.Fatal("TEXT length", length, lengthKnown)
		}
		precision, scale, decimalKnown := meta[0].DecimalSize()
		if precision != 0 || scale != 0 || decimalKnown {
			t.Fatal("TEXT decimal descriptor", precision, scale, decimalKnown)
		}
		nullable, nullableKnown := meta[0].Nullable()
		if nullable || nullableKnown {
			t.Fatal("computed TEXT nullability must be unknown", nullable, nullableKnown)
		}
	}
	return s
}

func metaphoneFailure(q, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func metaphoneFrames(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, frames []struct {
	args []any
	want any
}) {
	t.Helper()
	// Literal rendering and parameter numbering are shared test-only SQL helpers.
	q := levenshteinSQL(name, false, frames[0].args)
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, f := range frames {
		s := metaphoneSelect(q, name, f.want, f.args...)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, metaphoneSelect(levenshteinSQL(name, true, f.args), name, f.want))
	}
}
