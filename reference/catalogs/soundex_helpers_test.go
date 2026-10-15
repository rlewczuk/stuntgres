package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent REL_18_0/F.16.1 expectations confirmed against the pinned oracle.
func soundexCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"sx_reader"})
	fixture.SchemaReferenceCleanup(t, "sx_addon")
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
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP EXTENSION IF EXISTS fuzzystrmatch CASCADE", "DROP SCHEMA IF EXISTS sx_addon CASCADE"} {
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

func soundexSelect(b fixture.Backend, q, name string, want any, args ...any) fixture.Step {
	if name == "soundex" {
		return metaphoneSelect(q, name, want, args...)
	}
	s := levenshteinSelect(q, name, want, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		meta, err := rows.ColumnTypes()
		if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "INT4" {
			t.Fatal("INT4 descriptor", meta, err)
		}
		wantScan := reflect.TypeFor[int64]()
		if b.PG {
			wantScan = reflect.TypeFor[int32]()
		}
		if meta[0].ScanType() != wantScan {
			t.Fatal("INT4 ScanType", meta[0].ScanType(), wantScan)
		}
		length, lengthKnown := meta[0].Length()
		precision, scale, decimalKnown := meta[0].DecimalSize()
		nullable, nullableKnown := meta[0].Nullable()
		if length != 0 || lengthKnown || precision != 0 || scale != 0 || decimalKnown || nullable || nullableKnown {
			t.Fatal("computed INT4 metadata", length, lengthKnown, precision, scale, decimalKnown, nullable, nullableKnown)
		}
	}
	return s
}

func soundexFrames(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, frames []struct {
	args []any
	want any
}) {
	t.Helper()
	q := levenshteinSQL(name, false, frames[0].args)
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, f := range frames {
		s := soundexSelect(b, q, name, f.want, f.args...)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, soundexSelect(b, levenshteinSQL(name, true, f.args), name, f.want))
	}
}
