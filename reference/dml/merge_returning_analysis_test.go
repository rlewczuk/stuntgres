package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
	"unicode/utf8"
)

func mergeReturningDiagnostic(q, token, code, message string) fixture.Diagnostic {
	pos := strings.LastIndex(q, token)
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: fixture.Position(utf8.RuneCountInString(q[:pos]) + 1)}
}
func TestReferenceMergeReturningActionPlacement(t *testing.T) {
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { mergeReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		for _, q := range []string{
			"SELECT merge_action()",
			"UPDATE t SET v=11 RETURNING merge_action()",
			"DELETE FROM t RETURNING merge_action()",
			"INSERT INTO t VALUES(2,20) RETURNING merge_action()",
			"MERGE INTO t USING (VALUES(1)) s(id) ON merge_action()='UPDATE' WHEN MATCHED THEN DELETE",
			"MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED AND merge_action()='UPDATE' THEN DELETE",
			"MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=length(merge_action())",
			"MERGE INTO t USING (SELECT merge_action() AS a) s ON TRUE WHEN MATCHED THEN DELETE",
			"MERGE INTO t USING (VALUES(2)) s(id) ON t.id=s.id WHEN NOT MATCHED THEN INSERT VALUES(s.id,length(merge_action()))",
		} {
			q = "/*é*/ " + q
			s := fixture.ImplicitError(q, mergeReturningDiagnostic(q, "merge_action()", "42601", "MERGE_ACTION() can only be used in the RETURNING list of a MERGE command"))
			s.Prepared = prepared
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			run(s)
		}
		// Generic-call spelling/arity probes are deliberately independent of special syntax.
		for _, call := range []string{`"merge_action"()`, "public.merge_action()", "merge_action(1)"} {
			q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DELETE RETURNING " + call
			message := "function " + strings.TrimSuffix(call, "()") + "() does not exist"
			if call == `"merge_action"()` {
				message = "function merge_action() does not exist"
			}
			d := mergeReturningDiagnostic(q, call, "42883", message)
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			if call == "merge_action(1)" {
				d = mergeReturningDiagnostic(q, "1)", "42601", `syntax error at or near "1"`)
			}
			if !b.PG && call != "merge_action(1)" {
				d = mergeReturningDiagnostic(q, call, "0A000", "functions are unsupported")
			}
			s := fixture.ImplicitError(q, d)
			s.Prepared = prepared
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			run(s)
		}
		run(fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
	})
}
func TestReferenceMergeReturningDiagnostics(t *testing.T) {
	for _, tc := range []struct{ tail, token, code, message string }{
		{"missing_return", "missing_return", "42703", `column "missing_return" does not exist`},
		{"id", "id", "42702", `column reference "id" is ambiguous`},
		{"hidden.id", "hidden.id", "42P01", `missing FROM-clause entry for table "hidden"`},
		{"WITH(OLD AS o,OLD AS p) o.id", "OLD AS p", "42601", `OLD cannot be specified multiple times`},
		{"WITH(OLD AS s) s.id", "OLD AS s", "42712", `table name "s" specified more than once`},
		{"count(*)", "count(*)", "42803", "aggregate functions are not allowed in RETURNING"},
		{"row_number() OVER()", "row_number()", "42P20", "window functions are not allowed in RETURNING"},
		{"generate_series(1,2)", "generate_series", "0A000", "set-returning functions are not allowed in RETURNING"},
	} {
		t.Run(tc.tail, func(t *testing.T) {
			mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := func(s fixture.Step) { mergeReturningRun(t, p, b, s) }
				run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
				q := "/*é*/ MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET absent=missing_rhs RETURNING " + tc.tail
				d := mergeReturningDiagnostic(q, tc.token, tc.code, tc.message)
				if !b.PG && tc.token == "generate_series" {
					d.Message = "functions are unsupported"
				}
				s := fixture.ImplicitError(q, d)
				s.Prepared = prepared
				s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
				run(s)
			})
		})
	}
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { mergeReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		for _, tail := range []string{"RETURNING", "RETURNING id garbage garbage", "RETURNING WITH() id"} {
			q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DELETE " + tail
			token := ""
			message := "syntax error at end of input"
			pos := utf8.RuneCountInString(q) + 1
			if strings.Contains(tail, "garbage") {
				token = "garbage"
				message = `syntax error at or near "garbage"`
				pos = strings.LastIndex(q, token) + 1
			}
			if strings.Contains(tail, "WITH") {
				token = ") id"
				message = `syntax error at or near ")"`
				pos = strings.LastIndex(q, token) + 1
			}
			s := fixture.ImplicitError(q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: message, Position: fixture.Position(pos)})
			s.Prepared = prepared
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			run(s)
		}
	})
}
func TestReferenceMergeReturningParameters(t *testing.T) {
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		q := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=11 RETURNING $1 AS label,$2::int AS n"
		s := fixture.Select(q, []string{"label", "n"}, []string{"TEXT", "INT4"}, [][]any{{"fresh", int64(7)}}, "fresh", 7)
		s.Prepared = prepared
		run(s)
		base := "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=$1 RETURNING "
		for _, out := range []string{"$1", "$1,$1::int4", "$1::int4,$1"} {
			query := base + out
			if out == "$1::int4,$1" {
				s := fixture.Select(query, []string{"int4", "?column?"}, []string{"INT4", "INT4"}, [][]any{{int64(12), int64(12)}}, 12)
				s.Prepared = prepared
				run(s)
				continue
			}
			d := mergeReturningDiagnostic(query, "$1", "42804", `column "v" is of type integer but expression is of type text`)
			d.Position = fixture.Position(strings.Index(query, "v=$1") + 3)
			d.Hint = "You will need to rewrite or cast the expression."
			if out == "$1,$1::int4" {
				d = mergeReturningDiagnostic(query, "$1,$1", "42P08", "inconsistent types deduced for parameter $1")
				d.Detail = "integer versus text"
			}
			s := fixture.ImplicitError(query, d, 12)
			s.Prepared = prepared
			s.ErrorStage = map[bool]string{false: "execute", true: "prepare"}[prepared]
			run(s)
		}
		run(fixture.Exec("PREPARE inferred AS MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=13 RETURNING $1 AS label", 0))
		// This catalog view is an external PostgreSQL oracle inspection, not a
		// native provider. The inferred PREPARE/EXECUTE value proof stays shared.
		if b.PG {
			run(fixture.Select("SELECT parameter_types::text FROM pg_prepared_statements WHERE name='inferred'", []string{"parameter_types"}, []string{"TEXT"}, [][]any{{"{text}"}}))
		}
		run(fixture.Select("EXECUTE inferred('sql')", []string{"label"}, []string{"TEXT"}, [][]any{{"sql"}}))
		run(fixture.Exec("DEALLOCATE inferred", 0))
	})
}
func TestReferenceMergeReturningMetadata(t *testing.T) {
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { mergeReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT NOT NULL,v VARCHAR(5),n NUMERIC(8,2),ts TIMESTAMP(3),b BYTEA)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'word',12.30,'2024-01-02 03:04:05.123','\\x0102')", 1))
		for _, condition := range []string{"TRUE", "FALSE"} {
			rows := [][]any{}
			if condition == "TRUE" {
				rows = [][]any{{"UPDATE", int64(1), int64(1), "word", "12.30", time.Date(2024, 1, 2, 3, 4, 5, 123000000, time.UTC), "\x01\x02"}}
			}
			s := insertReturningBag("MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED AND "+condition+" THEN UPDATE SET id=1 RETURNING merge_action() AS \"Action\",old.id AS dup,new.id AS dup,v,n,ts,b", []string{"Action", "dup", "dup", "v", "n", "ts", "b"}, []string{"TEXT", "INT4", "INT4", "VARCHAR", "NUMERIC", "TIMESTAMP", "BYTEA"}, rows, prepared)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				m, e := rows.ColumnTypes()
				if e != nil {
					t.Fatal(e)
				}
				if n, k := m[3].Length(); !k || n != 5 {
					t.Fatal("varchar", n, k)
				}
				if n, scale, k := m[4].DecimalSize(); !k || n != 8 || scale != 2 {
					t.Fatal("numeric", n, scale, k)
				}
				want := reflect.TypeOf("")
				if b.PG {
					want = reflect.TypeOf((*any)(nil)).Elem()
				}
				if m[4].ScanType() != want {
					t.Fatal("scan type", m[4].ScanType())
				}
				for _, i := range []int{1, 2} {
					n, k := m[i].Nullable()
					if b.PG && k {
						t.Fatal("pq nullability", i, n, k)
					}
					if !b.PG && (!k || !n) {
						t.Fatal("mixed transition nullability", i, n, k)
					}
				}
			}
			run(s)
		}
	})
}
