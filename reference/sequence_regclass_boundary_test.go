package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceSequenceRegclassInputPrefixProtection(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			for _, route := range []string{"Literal", "Direct", "Prepared"} {
				t.Run(route, func(t *testing.T) {
					for _, input := range []string{"s--suffix", "s/*suffix*/"} {
						for _, consumer := range []string{"nextval(%s)", "nextval(%s) FROM empty_table", "setval(%s,$2)", "setval(%s,v) FROM nullable_table", "setval(%s,nextval('t'))"} {
							t.Run(input+"/"+consumer, func(t *testing.T) {
								ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
								c := sequenceRegclassBoundaryConn(t, ctx, db)
								for _, q := range []string{"CREATE TABLE empty_table(v INT)", "CREATE TABLE nullable_table(v BIGINT)", "CREATE SEQUENCE s START 17 CACHE 1", "CREATE SEQUENCE t START 31 CACHE 1"} {
									sequenceProbeExec(t, ctx, c, q)
								}
								result, e := c.ExecContext(ctx, "INSERT INTO nullable_table VALUES(NULL)")
								if e != nil {
									t.Fatal(e)
								}
								if n, e := result.RowsAffected(); e != nil || n != 1 {
									t.Fatal(n, e)
								}
								argument := "$1"
								args := []any{input}
								want := referenceDiagnostic{Code: "42P01", Message: `relation "` + input + `" does not exist`, Context: "unnamed portal parameter $1 = '...'"}
								if route == "Literal" {
									argument = "'" + input + "'"
									args = nil
									want.Context = ""
									want.Position = "16"
								}
								query := "SELECT " + strings.Replace(consumer, "%s", argument, 1)
								if strings.Contains(query, "$2") {
									if route == "Literal" {
										query = strings.Replace(query, "$2", "NULL", 1)
									} else {
										args = append(args, nil)
									}
								}
								if route == "Literal" && strings.HasPrefix(consumer, "setval") {
									want.Position = "15"
								}
								var rows *sql.Rows
								var err error
								if route == "Prepared" {
									stmt, e := c.PrepareContext(ctx, query)
									if e != nil {
										t.Fatal("Prepare must not import parameters", e)
									}
									defer stmt.Close()
									rows, err = stmt.QueryContext(ctx, args...)
								} else {
									rows, err = c.QueryContext(ctx, query, args...)
								}
								if rows != nil {
									rows.Close()
								}
								sequenceRegclassBoundaryDiagnostic(t, err, want)
								var n int64
								for _, name := range []string{"s", "t"} {
									sequenceRegclassBoundaryDiagnostic(t, c.QueryRowContext(ctx, "SELECT currval('"+name+"')").Scan(&n), referenceDiagnostic{Code: "55000", Message: `currval of sequence "` + name + `" is not yet defined in this session`})
								}
								sequenceRegclassBoundaryDiagnostic(t, c.QueryRowContext(ctx, "SELECT lastval()").Scan(&n), referenceDiagnostic{Code: "55000", Message: "lastval is not yet defined in this session"})
								sequenceProbeValue(t, ctx, c, "SELECT nextval('s')", 17)
								sequenceProbeValue(t, ctx, c, "SELECT nextval('t')", 31)
							})
						}
					}
				})
			}
		})
	}
}

func TestReferenceSequenceRegclassInputExactIdentity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			for _, route := range []string{"Literal", "Direct", "Prepared"} {
				t.Run(route, func(t *testing.T) {
					c := sequenceRegclassBoundaryConn(t, ctx, db)
					for _, q := range []string{"CREATE SCHEMA \"Odd.Schema\"", "CREATE SEQUENCE s START 17 CACHE 1", `CREATE SEQUENCE "s--suffix" START 31 CACHE 1`, `CREATE SEQUENCE "s/*suffix*/" START 41 CACHE 1`, `CREATE SEQUENCE "s-other" START 51 CACHE 1`, `CREATE SEQUENCE "0" START 61 CACHE 1`, `CREATE SEQUENCE "-" START 71 CACHE 1`, `CREATE SEQUENCE "4294967295" START 81 CACHE 1`, `CREATE SEQUENCE "Odd.Schema"."a""b" START 91 CACHE 1`, `CREATE SEQUENCE folded START 101 CACHE 1`, `CREATE SEQUENCE "` + strings.Repeat("é", 31) + `" START 111 CACHE 1`} {
						sequenceProbeExec(t, ctx, c, q)
					}
					lastQuery := ""
					query := func(expression, input string) (*sql.Rows, error) {
						q := "SELECT " + strings.Replace(expression, "%s", "$1", 1)
						if route == "Literal" {
							lastQuery = strings.Replace(q, "$1", "'"+input+"'", 1)
							return c.QueryContext(ctx, lastQuery)
						}
						lastQuery = q
						if route == "Direct" {
							return c.QueryContext(ctx, q, input)
						}
						stmt, err := c.PrepareContext(ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { stmt.Close() })
						return stmt.QueryContext(ctx, input)
					}
					for _, tc := range []struct {
						input string
						want  int64
					}{{"s--suffix", 31}, {"s/*suffix*/", 41}, {"s-other", 51}, {" 0 ", 61}, {" - ", 71}, {" 4294967295 ", 81}, {` "Odd.Schema" . "a""b" `, 91}, {"FoLdEd", 101}, {strings.Repeat("é", 32), 111}} {
						rows, err := query("nextval(%s)", tc.input)
						sequenceRegclassBoundaryRows(t, ctx, backend, lastQuery, rows, err, []string{"nextval"}, []string{"INT8"}, [][]any{{tc.want}}, false)
					}
					sequenceProbeValue(t, ctx, c, "SELECT nextval('s')", 17)
					for _, tc := range []struct{ input, output string }{{"0", "-"}, {"-", "-"}, {"4294967295", "4294967295"}} {
						rows, err := query("%s::regclass", tc.input)
						sequenceRegclassBoundaryRows(t, ctx, backend, lastQuery, rows, err, []string{"regclass"}, []string{"REGCLASS"}, [][]any{{tc.output}}, false)
						rows, err = query("nextval(%s) FROM s WHERE is_called=false", tc.input)
						sequenceRegclassBoundaryRows(t, ctx, backend, lastQuery, rows, err, []string{"nextval"}, []string{"INT8"}, [][]any{}, false)
						rows, err = query("setval(%s,NULL)", tc.input)
						sequenceRegclassBoundaryRows(t, ctx, backend, lastQuery, rows, err, []string{"setval"}, []string{"INT8"}, [][]any{{nil}}, false)
						rows, err = query("nextval(%s)", tc.input)
						if rows != nil {
							rows.Close()
						}
						oid := tc.input
						if oid == "-" {
							oid = "0"
						}
						sequenceRegclassBoundaryDiagnostic(t, err, referenceDiagnostic{Code: "XX000", Message: "could not open relation with OID " + oid})
					}
				})
			}
		})
	}
}

func TestReferenceSequenceRegclassInputSyntaxDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			for _, route := range []string{"Literal", "Direct", "Prepared"} {
				t.Run(route, func(t *testing.T) {
					c := sequenceRegclassBoundaryConn(t, ctx, db)
					for _, tc := range []struct{ input, code, message string }{
						{`"s`, "42602", "invalid name syntax"},
						{`"s"tail`, "42602", "invalid name syntax"},
						{"s.", "42602", "invalid name syntax"},
						{`""`, "42P01", `relation "" does not exist`},
						{" MiSsInG--Suffix ", "42P01", `relation "missing--suffix" does not exist`},
					} {
						want := referenceDiagnostic{Code: tc.code, Message: tc.message, Context: "unnamed portal parameter $1 = '...'"}
						q := "SELECT $1::regclass"
						args := []any{tc.input}
						if route == "Literal" {
							q = "SELECT '" + tc.input + "'::regclass"
							args = nil
							want.Context = ""
							want.Position = "8"
						}
						var rows *sql.Rows
						var err error
						if route == "Prepared" {
							stmt, e := c.PrepareContext(ctx, q)
							if e != nil {
								t.Fatal(e)
							}
							rows, err = stmt.QueryContext(ctx, args...)
							stmt.Close()
						} else {
							rows, err = c.QueryContext(ctx, q, args...)
						}
						if rows != nil {
							rows.Close()
						}
						sequenceRegclassBoundaryDiagnostic(t, err, want)
					}
				})
			}
		})
	}
}

// Both routes use fixture sqlmw drivers. Each record has release-derived full
// expectations before comparison; /Postgres remains independently runnable.
func sequenceRegclassBoundaryConn(t *testing.T, ctx context.Context, db *sql.DB) *sql.Conn {
	t.Helper()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{
			`DROP SCHEMA IF EXISTS "Odd.Schema" CASCADE`,
			`DROP TABLE IF EXISTS empty_table,nullable_table`,
			`DROP SEQUENCE IF EXISTS s,t,folded,"s--suffix","s/*suffix*/","s-other","0","-","4294967295","` + strings.Repeat("é", 31) + `"`,
		} {
			if _, err := c.ExecContext(ctx, q); err != nil {
				t.Error("fixture cleanup", q, err)
			}
		}
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func sequenceRegclassBoundaryDiagnostic(t *testing.T, err error, want referenceDiagnostic) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	referenceAssertDiagnostic(t, err, &want)
}

func sequenceRegclassBoundaryRows(t *testing.T, ctx context.Context, backend referenceBackend, query string, rows *sql.Rows, err error, columns, types []string, want [][]any, bag bool) {
	t.Helper()
	if err != nil {
		if rows != nil {
			rows.Close()
		}
		t.Fatal(err)
	}
	referenceRunStep(t, ctx, sequenceFrozenRows{rows: rows, query: query}, backend.recorder, referenceStep{query: query, selectRows: true, columns: columns, types: types, rows: want, bag: bag})
}
