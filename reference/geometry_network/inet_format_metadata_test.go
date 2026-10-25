package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceINETFormatMetadataAndBinds(t *testing.T) {
	inetFormatScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		q := "SELECT host($1::inet) AS h,family($1::inet) AS f,inet_same_family($1::inet,INET '::1') AS s,broadcast($1::inet) AS b"
		names := []string{"h", "f", "s", "b"}
		types := []string{"TEXT", "INT4", "BOOL", "INET"}
		makeStep := func(query string, arg any, want [][]any) fixture.Step {
			s := fixture.Select(query, names, types, want, arg)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				c, err := r.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				fixture.ScalarAssertDescription(t, c, names, types)
				fixture.ScalarAssertMetadata(t, b.PG, c, 0, 1, 2, 3)
				for i, col := range c {
					if _, _, ok := col.DecimalSize(); ok {
						t.Fatal("unexpected precision", i)
					}
					length, known := col.Length()
					if i == 0 {
						if !known || length != 9223372036854775807 {
							t.Fatal("text length", length, known)
						}
					} else if known || length != 0 {
						t.Fatal("unexpected length", i, length, known)
					}
				}
				scan := reflect.TypeFor[string]()
				if b.PG {
					scan = reflect.TypeFor[any]()
				}
				if c[3].ScanType() != scan {
					t.Fatal("INET ScanType", c[3].ScanType())
				}
			}
			s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
				fixture.AssertRawValue(t, b.PG, types[col], value)
				return true
			}
			return s
		}
		for _, tc := range []struct {
			tail string
			arg  any
			rows [][]any
		}{
			{"", "192.168.1.5/24", [][]any{{"192.168.1.5", int64(4), false, "192.168.1.255/24"}}},
			{"", []byte("::1"), [][]any{{"::1", int64(6), true, "::1"}}}, {"", nil, [][]any{{nil, nil, nil, nil}}}, {"", []byte(nil), [][]any{{nil, nil, nil, nil}}}, {" WHERE FALSE", "::1", nil},
		} {
			inetFormatBoth(t, p, b, makeStep(q+tc.tail, tc.arg, tc.rows))
		}
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, arg := range []any{"192.168.1.5/24", []byte("192.168.1.5/24")} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, makeStep(q, arg, [][]any{{"192.168.1.5", int64(4), false, "192.168.1.255/24"}}))
		}
		for _, arg := range []any{"bad", []byte{}} {
			input, context := "bad", "unnamed portal parameter $1 = '...'"
			if _, ok := arg.([]byte); ok {
				input = ""
				context = "unnamed portal parameter $1 = ''"
			}
			d := inetDiagnostic("inet", input, false)
			d.Context = context
			s := fixture.ErrorDiagnostic(q, d, arg)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, makeStep(q, "::1", [][]any{{"::1", int64(6), true, "::1"}}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, makeStep(q, nil, [][]any{{nil, nil, nil, nil}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE nf_owned(ip inet)", 0))
		source := []byte("192.168.1.5/24")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO nf_owned VALUES($1)", 1, source)))
		source[0] = '0'
		inetFormatBoth(t, p, b, fixture.Select("SELECT host(ip) AS h FROM nf_owned", []string{"h"}, []string{"TEXT"}, [][]any{{"192.168.1.5"}}))
	})
}
