package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func inetMaskMetadataStep(b fixture.Backend, q string, names, types []string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, want, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		c, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.ScalarAssertDescription(t, c, names, types)
		indices := make([]int, len(c))
		for i := range indices {
			indices[i] = i
		}
		fixture.ScalarAssertMetadata(t, b.PG, c, indices...)
		scan := reflect.TypeFor[string]()
		if b.PG {
			scan = reflect.TypeFor[any]()
		}
		for _, col := range c {
			if col.ScanType() != scan {
				t.Fatal("network ScanType", col.ScanType(), scan)
			}
			if n, ok := col.Length(); n != 0 || ok {
				t.Fatal("network length", n, ok)
			}
			if p, s, ok := col.DecimalSize(); p != 0 || s != 0 || ok {
				t.Fatal("network precision", p, s, ok)
			}
		}
	}
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		fixture.AssertRawValue(t, b.PG, types[col], v)
		return true
	}
	return s
}

func TestReferenceINETMaskMetadataAndBinds(t *testing.T) {
	inetMaskScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			q            string
			names, types []string
			args         []any
			want         [][]any
			bad          []any
			parameter    int
		}{
			{"SELECT hostmask($1::inet) AS hm,netmask($1::inet) AS nm,network($1::inet) AS n", inetMaskNames[:3], inetMaskTypes[:3], []any{"192.168.1.5/24"}, [][]any{{"0.0.0.255", "255.255.255.0", "192.168.1.0/24"}}, []any{"bad"}, 1},
			{"SELECT set_masklen($1::inet,$2::integer) AS si", []string{"si"}, []string{"INET"}, []any{"192.168.1.5/24", 16}, [][]any{{"192.168.1.5/16"}}, []any{"bad", 16}, 1},
			{"SELECT set_masklen($1::cidr,$2::integer) AS sc", []string{"sc"}, []string{"CIDR"}, []any{"192.168.1.0/24", 16}, [][]any{{"192.168.0.0/16"}}, []any{"bad", 16}, 1},
			{"SELECT inet_merge($1::inet,$2::inet) AS m", []string{"m"}, []string{"CIDR"}, []any{"192.168.1.5/24", "192.168.2.5/24"}, [][]any{{"192.168.0.0/22"}}, []any{"192.168.1.5/24", "bad"}, 2},
		} {
			t.Run(tc.q, func(t *testing.T) {
				makeStep := func(q string, w [][]any, args ...any) fixture.Step {
					return inetMaskMetadataStep(b, q, tc.names, tc.types, w, args...)
				}
				nulls := make([]any, len(tc.names))
				inetFormatBoth(t, p, b, makeStep(tc.q, tc.want, tc.args...))
				inetFormatBoth(t, p, b, makeStep(tc.q+" WHERE FALSE", nil, tc.args...))
				stmt, err := b.DB.PrepareContext(p.Ctx, tc.q)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := stmt.Close(); err != nil {
						t.Error(err)
					}
				}()
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: tc.q}
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
				for _, bytesMode := range []bool{false, true} {
					args := append([]any(nil), tc.args...)
					if bytesMode {
						for i, a := range args {
							if v, ok := a.(string); ok {
								args[i] = []byte(v)
							}
						}
					}
					run(makeStep(tc.q, tc.want, args...))
				}
				for _, nilArg := range []any{nil, []byte(nil)} {
					for i, a := range tc.args {
						if _, ok := a.(string); !ok {
							continue
						}
						args := append([]any(nil), tc.args...)
						args[i] = nilArg
						inetFormatBoth(t, p, b, makeStep(tc.q, [][]any{nulls}, args...))
						run(makeStep(tc.q, [][]any{nulls}, args...))
					}
				}
				typ := "inet"
				if tc.types[0] == "CIDR" && len(tc.args) == 2 && tc.parameter == 1 {
					typ = "cidr"
				}
				d := inetDiagnostic(typ, "bad", false)
				if tc.parameter == 2 {
					d.Context = "unnamed portal parameter $2 = '...'"
				} else {
					d.Context = "unnamed portal parameter $1 = '...'"
				}
				s := fixture.ErrorDiagnostic(tc.q, d, tc.bad...)
				s.SelectRows = true
				s.ErrorStage = "execute"
				run(s)
				run(makeStep(tc.q, tc.want, tc.args...))
			})
		}
		for _, tc := range []struct {
			expr, typ string
			args      []any
		}{
			{"hostmask(NULL::inet)", "INET", nil}, {"netmask(NULL::inet)", "INET", nil}, {"network(NULL::inet)", "CIDR", nil},
			{"set_masklen(NULL::inet,33)", "INET", nil}, {"set_masklen(NULL::cidr,129)", "CIDR", nil},
			{"set_masklen(INET '::1',NULL::integer)", "INET", nil}, {"set_masklen(CIDR '::1/128',NULL::integer)", "CIDR", nil},
			{"inet_merge(NULL::inet,INET '::1')", "CIDR", nil}, {"inet_merge(INET '1.2.3.4',NULL::inet)", "CIDR", nil},
			{"set_masklen($1::inet,33)", "INET", []any{nil}}, {"set_masklen(INET '::1',$1::integer)", "INET", []any{nil}},
		} {
			inetFormatBoth(t, p, b, inetMaskMetadataStep(b, "SELECT "+tc.expr+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{nil}}, tc.args...))
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE nm_owned(ip inet)", 0))
		source := []byte("192.168.1.5/24")
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO nm_owned VALUES(set_masklen($1::inet,16))", 1, source)))
		source[0] = '0'
		inetFormatBoth(t, p, b, fixture.Select("SELECT ip FROM nm_owned", []string{"ip"}, []string{"INET"}, [][]any{{"192.168.1.5/16"}}))
		if !b.PG {
			// Native precancellation is an ordinary Go boundary, not a fabricated
			// server diagnostic or a pq transport-race expectation.
			for _, tc := range []struct {
				q, typ, w string
				args      []any
			}{
				{"SELECT hostmask($1::inet) AS v", "INET", "0.0.0.255", []any{"192.168.1.5/24"}},
				{"SELECT netmask($1::inet) AS v", "INET", "255.255.255.0", []any{"192.168.1.5/24"}},
				{"SELECT network($1::inet) AS v", "CIDR", "192.168.1.0/24", []any{"192.168.1.5/24"}},
				{"SELECT set_masklen($1::inet,$2::integer) AS v", "INET", "192.168.1.5/16", []any{"192.168.1.5/24", 16}},
				{"SELECT set_masklen($1::cidr,$2::integer) AS v", "CIDR", "192.168.0.0/16", []any{"192.168.1.0/24", 16}},
				{"SELECT inet_merge($1::inet,$2::inet) AS v", "CIDR", "::/126", []any{"::1", "::2"}},
			} {
				stmt, err := b.DB.PrepareContext(p.Ctx, tc.q)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, prepared := range []bool{false, true} {
					var rows *sql.Rows
					if prepared {
						rows, err = stmt.QueryContext(ctx, tc.args...)
					} else {
						rows, err = b.DB.QueryContext(ctx, tc.q, tc.args...)
					}
					if rows != nil {
						if e := rows.Close(); e != nil {
							t.Error(e)
						}
						t.Fatal("canceled mask call published rows")
					}
					var state interface{ SQLState() string }
					if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
						t.Fatal("expected ordinary native cancellation", err)
					}
				}
				held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: tc.q}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.w}}, tc.args...))
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}
