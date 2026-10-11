package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func stringVariadicContract(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, args ...any) {
	t.Helper()
	want, ok := stringVariadicExpectations[q]
	if !ok {
		t.Fatalf("missing independent contract for %q", q)
	}
	if want.diagnostic.Code != "" {
		formatReferenceFailure(t, p, b, c, q, want.diagnostic, want.preparedStage == "prepare")
		return
	}
	rows := want.rows
	if rows == nil {
		rows = [][]any{}
	}
	formatReferenceBoth(t, p, b, c, fixture.Select(q, want.columns, want.types, rows, args...))
}

func stringVariadicInquiries(t *testing.T, queries []string) {
	t.Helper()
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for i, q := range queries {
			t.Run(fmt.Sprintf("probe_%03d", i), func(t *testing.T) { stringVariadicContract(t, p, b, c, q) })
		}
	})
}

func TestReferenceStringVariadicDiagnostics(t *testing.T) {
	queries := []string{
		`SELECT concat()`, `SELECT concat_ws()`, `SELECT concat_ws(',')`,
		`SELECT quote_ident()`, `SELECT quote_literal()`, `SELECT quote_nullable()`,
		`SELECT quote_ident('a','b')`, `SELECT quote_literal('a','b')`, `SELECT quote_nullable('a','b')`, `SELECT pg_client_encoding(1)`,
		`SELECT concat(VARIADIC 1)`, `SELECT concat(VARIADIC NULL)`, `SELECT concat_ws(',',VARIADIC 1)`, `SELECT concat_ws(',',VARIADIC NULL)`,
		`SELECT concat('x',VARIADIC '{a,b}'::text[])`, `SELECT concat_ws(',','x',VARIADIC '{a,b}'::text[])`,
		`SELECT concat_ws(42,'x')`, `SELECT quote_ident(42)`, `SELECT quote_ident(true)`, `SELECT quote_ident('{a,b}'::text[])`,
		`SELECT quote_ident(VARIADIC 'x')`, `SELECT quote_literal(VARIADIC 42)`, `SELECT quote_nullable(VARIADIC NULL)`,
		`SELECT quote_literal(VARIADIC '{a,b}'::text[])`, `SELECT quote_nullable(VARIADIC NULL::text[])`,
		`/*é🙂*/ SELECT concat(VARIADIC 1)`, `/*é🙂*/ SELECT quote_ident(42)`,
		`SELECT concat_ws(NULL,format('%I',NULL))`, `SELECT concat(missing_sv_child())`,
		`SELECT concat(E'\\200'::"char"),quote_literal(E'\\200'::"char")`,
		`SELECT quote_literal('x'::"char")`, `SELECT quote_nullable('x'::"char")`,
	}
	for _, n := range []int{100, 101} {
		args := strings.TrimSuffix(strings.Repeat("1,", n), ",")
		queries = append(queries, "SELECT concat("+args+")", "SELECT concat_ws("+`'|'`+","+strings.TrimSuffix(strings.Repeat("1,", n-1), ",")+")")
	}
	args := strings.TrimSuffix(strings.Repeat("1,", 100), ",")
	queries = append(queries, "SELECT concat("+args+",'bad'::integer)", "SELECT sv_absent.concat("+args+",1)", "SELECT concat(VARIADIC '{"+strings.TrimSuffix(strings.Repeat("1,", 200), ",")+"}'::integer[])")
	stringVariadicInquiries(t, queries)
}

func TestReferenceStringVariadicParameters(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for i, tc := range []struct {
			q    string
			args []any
		}{
			{`SELECT concat($1)`, []any{"x"}}, {`SELECT concat_ws($1,'x')`, []any{"|"}}, {`SELECT concat_ws(',',$1)`, []any{"x"}},
			{`SELECT concat(VARIADIC $1)`, []any{"{1,2}"}}, {`SELECT concat($1),$1::text`, []any{"x"}}, {`SELECT concat($1::text),$1`, []any{"x"}},
			{`SELECT quote_ident($1)`, []any{"x"}}, {`SELECT quote_literal($1)`, []any{"x"}}, {`SELECT quote_nullable($1)`, []any{nil}},
			{`SELECT quote_literal($1),$1::integer`, []any{42}}, {`SELECT quote_literal($1::integer),$1`, []any{42}},
			{`SELECT concat_ws($1,$1)`, []any{"x"}}, {`SELECT concat_ws($1,$2::int4)`, []any{"|", 42}},
			{`SELECT concat(VARIADIC $1::int4[])`, []any{"{1,NULL,2}"}},
		} {
			t.Run(fmt.Sprintf("parameter_%02d", i), func(t *testing.T) {
				want := stringVariadicExpectations[tc.q]
				if want.diagnostic.Code != "" {
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(tc.q, want.diagnostic, tc.args...)
						s.SelectRows = true
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
							s.ErrorStage = want.preparedStage
						}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				} else {
					stringVariadicContract(t, p, b, c, tc.q, tc.args...)
				}
			})
		}
	})
}

func TestReferenceStringVariadicNamespaces(t *testing.T) {
	var queries []string
	for _, tc := range []struct{ name, args string }{{"concat", "'x'"}, {"concat_ws", "'|','x'"}, {"quote_ident", "'x'"}, {"quote_literal", "'x'"}, {"quote_nullable", "NULL"}, {"pg_client_encoding", ""}} {
		for _, head := range []string{tc.name, "pg_catalog." + tc.name, "public." + tc.name, "sv_absent." + tc.name, `"` + strings.ToUpper(tc.name) + `"`} {
			queries = append(queries, "SELECT "+head+"("+tc.args+")")
		}
	}
	queries = append(queries, `SELECT quote_literal(value => 'x')`, `SELECT concat(value => 'x')`, `SELECT pg_client_encoding(value => 1)`, `SELECT concat(value => 'x',value => 'y')`, `SELECT concat(value => 'x','y')`, `SELECT concat(VARIADIC '{a}'::text[],'x')`, `SELECT quote_ident('x') OVER ()`, `SELECT concat(DISTINCT 'x')`, `SELECT concat(ALL 'x')`, `SELECT concat('x') FILTER (WHERE true)`)
	stringVariadicInquiries(t, queries)
}

func TestReferenceStringVariadicMetadata(t *testing.T) {
	q := `SELECT concat(NULL) AS joined,concat_ws(NULL,'x') AS separated,quote_ident(NULL::text) AS identifier,quote_literal(NULL::text) AS literal,quote_nullable(NULL::text) AS nullable,pg_client_encoding() AS encoding`
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{q, q + ` WHERE false`, `SELECT concat('x'),concat_ws('|','a','b'),quote_ident('x'),quote_literal(42),quote_nullable(42),pg_client_encoding()`} {
			want := stringVariadicExpectations[query]
			for _, prepared := range []bool{false, true} {
				func() {
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, e := c.PrepareContext(p.Ctx, query)
						if e != nil {
							t.Fatal(e)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(p.Ctx)
					} else {
						rows, err = c.QueryContext(p.Ctx, query)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					check := func() {
						if len(columns) != 6 {
							t.Fatalf("column count %d", len(columns))
						}
						for i, col := range columns {
							scan := reflect.TypeFor[string]()
							length, known := int64(9223372036854775807), true
							if i == 5 {
								length, known = 0, false
								if b.PG {
									scan = reflect.TypeFor[any]()
								}
							}
							if col.Name() != want.columns[i] || col.DatabaseTypeName() != want.types[i] || col.ScanType() != scan {
								t.Fatalf("column %d identity: %s/%s/%v", i, col.Name(), col.DatabaseTypeName(), col.ScanType())
							}
							if n, k := col.Length(); n != length || k != known {
								t.Fatalf("column %d Length %d/%t", i, n, k)
							}
							if p, s, k := col.DecimalSize(); p != 0 || s != 0 || k {
								t.Fatalf("column %d DecimalSize %d/%d/%t", i, p, s, k)
							}
							if n, k := col.Nullable(); n || k {
								t.Fatalf("column %d Nullable %t/%t", i, n, k)
							}
						}
					}
					check()
					values := [][]any{}
					for rows.Next() {
						row := make([]any, 6)
						dest := make([]any, 6)
						for i := range row {
							dest[i] = &row[i]
						}
						if err := rows.Scan(dest...); err != nil {
							t.Fatal(err)
						}
						for i, v := range row {
							if bytes, ok := v.([]byte); ok {
								row[i] = string(bytes)
							}
						}
						values = append(values, row)
					}
					if err := rows.Err(); err != nil {
						t.Fatal(err)
					}
					expected := want.rows
					if expected == nil {
						expected = [][]any{}
					}
					if !reflect.DeepEqual(values, expected) {
						t.Fatalf("rows %#v want %#v", values, expected)
					}
					check()
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					check()
				}()
			}
		}
	})
}
