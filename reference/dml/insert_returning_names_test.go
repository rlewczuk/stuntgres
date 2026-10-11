package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceInsertReturningAliases(t *testing.T) {
	for _, tc := range []struct {
		options, output string
		want            []any
	}{
		{"WITH (OLD AS o,NEW AS n)", "o.id,n.id,id,x.id", []any{nil, int64(2), int64(2), int64(2)}},
		{"WITH (OLD AS new)", "new.id,id", []any{nil, int64(2)}},
		{"WITH (NEW AS old)", "old.id,id", []any{int64(2), int64(2)}},
		{"WITH (OLD AS new,NEW AS old)", "new.id,old.id", []any{nil, int64(2)}},
		{"WITH (OLD AS \"O\",NEW AS \"N\")", "\"O\".id,\"N\".id", []any{nil, int64(2)}},
	} {
		t.Run(tc.options, func(t *testing.T) {
			insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				insertReturningRun(t, p, b, fixture.Exec("CREATE TABLE t(id INT)", 0))
				names, types := make([]string, len(tc.want)), make([]string, len(tc.want))
				for i := range names {
					names[i] = "id"
					types[i] = "INT4"
				}
				insertReturningRun(t, p, b, insertReturningBag("INSERT INTO t AS x VALUES(2) RETURNING "+tc.options+" "+tc.output, names, types, [][]any{tc.want}, prepared))
			})
		})
	}
	for _, target := range []string{"old", "new"} {
		t.Run("target_"+target, func(t *testing.T) {
			insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				insertReturningRun(t, p, b, fixture.Exec("CREATE TABLE "+target+"(id INT)", 0))
				insertReturningRun(t, p, b, insertReturningBag("INSERT INTO "+target+" VALUES(7) RETURNING "+target+".id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}, prepared))
			})
		})
	}
}
func insertReturningComposite(t *testing.T, b fixture.Backend, s fixture.Step) fixture.Step {
	t.Helper()
	s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
		t.Helper()
		if v == nil {
			return true
		}
		if b.PG {
			if _, ok := v.([]byte); !ok {
				t.Fatalf("pq composite raw %T", v)
			}
		} else {
			if _, ok := v.(string); !ok {
				t.Fatalf("native composite raw %T", v)
			}
		}
		return true
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		meta, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		want := reflect.TypeOf("")
		if b.PG {
			want = reflect.TypeOf((*any)(nil)).Elem()
		}
		for _, m := range meta {
			if m.DatabaseTypeName() != "" || m.ScanType() != want {
				t.Fatalf("composite %s %q %v want blank/%v", m.Name(), m.DatabaseTypeName(), m.ScanType(), want)
			}
		}
	}
	return s
}
func TestReferenceInsertReturningWholeRow(t *testing.T) {
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { insertReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,v TEXT)", 0))
		s := insertReturningBag("INSERT INTO t VALUES(1,'x') RETURNING old,new,t", []string{"old", "new", "t"}, []string{"", "", ""}, [][]any{{nil, "(1,x)", "(1,x)"}}, prepared)
		run(insertReturningComposite(t, b, s))
		s = insertReturningBag("INSERT INTO t AS x VALUES(1,'y'),(2,'z') ON CONFLICT(id) DO UPDATE SET v=excluded.v RETURNING WITH(OLD AS o,NEW AS n) o,n,x", []string{"o", "n", "x"}, []string{"", "", ""}, [][]any{{"(1,x)", "(1,y)", "(1,y)"}, {nil, "(2,z)", "(2,z)"}}, prepared)
		run(insertReturningComposite(t, b, s))
		run(fixture.Exec("CREATE TABLE nulls(id INT,v TEXT)", 0))
		run(insertReturningComposite(t, b, insertReturningBag("INSERT INTO nulls VALUES(NULL,NULL) RETURNING old,new", []string{"old", "new"}, []string{"", ""}, [][]any{{nil, "(,)"}}, prepared)))
		run(fixture.Exec("CREATE TABLE empty()", 0))
		run(insertReturningComposite(t, b, insertReturningBag("INSERT INTO empty DEFAULT VALUES RETURNING old,new", []string{"old", "new"}, []string{"", ""}, [][]any{{nil, "()"}}, prepared)))
		run(fixture.Exec("CREATE TABLE named(id INT,old TEXT,new TEXT)", 0))
		run(insertReturningBag("INSERT INTO named VALUES(3,'column-old','column-new') RETURNING old,new,old.id,new.id", []string{"old", "new", "id", "id"}, []string{"TEXT", "TEXT", "INT4", "INT4"}, [][]any{{"column-old", "column-new", nil, int64(3)}}, prepared))
		run(fixture.Exec("CREATE TYPE inner_row AS (a INT,b TEXT)", 0))
		run(fixture.Exec("CREATE TABLE nested(id INT,c inner_row,b BYTEA)", 0))
		run(insertReturningComposite(t, b, insertReturningBag(`INSERT INTO nested VALUES(1,'(2,x)','\x0102') RETURNING old,new`, []string{"old", "new"}, []string{"", ""}, [][]any{{nil, `(1,"(2,x)","\\x0102")`}}, prepared)))
		run(fixture.Exec("CREATE TABLE base(id INT,v TEXT,extra INT DEFAULT 99)", 0))
		run(fixture.Exec("CREATE VIEW live AS SELECT id,v FROM base", 0))
		run(insertReturningComposite(t, b, insertReturningBag("INSERT INTO live VALUES(4,'view') RETURNING old,new", []string{"old", "new"}, []string{"", ""}, [][]any{{nil, "(4,view)"}}, prepared)))
		run(fixture.Exec("CREATE TABLE parts(id INT,v TEXT) PARTITION BY RANGE(id)", 0))
		run(fixture.Exec("CREATE TABLE leaf PARTITION OF parts FOR VALUES FROM (0) TO (10)", 0))
		run(insertReturningComposite(t, b, insertReturningBag("INSERT INTO parts VALUES(5,'part') RETURNING old,new", []string{"old", "new"}, []string{"", ""}, [][]any{{nil, "(5,part)"}}, prepared)))
	})
}
func TestReferenceInsertReturningSubqueries(t *testing.T) {
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { insertReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(9,90)", 1))
		run(insertReturningBag("INSERT INTO t VALUES(1,10),(2,20) RETURNING (SELECT new.v) AS n,(SELECT old.v) AS o,(SELECT count(*) FROM t) AS frontier,(SELECT new.v FROM t AS new WHERE new.id=9) AS shadow,(SELECT (SELECT new.id)) AS nested", []string{"n", "o", "frontier", "shadow", "nested"}, []string{"INT4", "INT4", "INT8", "INT4", "INT4"}, [][]any{{int64(10), nil, int64(1), int64(90), int64(1)}, {int64(20), nil, int64(1), int64(90), int64(2)}}, prepared))
		insertReturningFailure(t, p, b, b.DB, "INSERT INTO t VALUES(3,30) RETURNING (SELECT id FROM t)", prepared, true, fixture.Diagnostic{Code: "21000", Message: "more than one row returned by a subquery used as an expression"})
		run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(9)}}))
	})
}
func insertReturningPosition(q, token string) string {
	i := strings.LastIndex(q, token)
	if i < 0 {
		panic("diagnostic token missing")
	}
	return fixture.Position(utf8.RuneCountInString(q[:i]) + 1)
}
func TestReferenceInsertReturningDiagnostics(t *testing.T) {
	cases := []struct{ q, code, message, token, hint string }{
		{"INSERT INTO t VALUES(2,'x') RETURNING WITH(OLD AS o,OLD AS z) id", "42601", "OLD cannot be specified multiple times", "OLD AS z", ""},
		{"INSERT INTO t VALUES(2,'x') RETURNING WITH(NEW AS n,NEW AS z) id", "42601", "NEW cannot be specified multiple times", "NEW AS z", ""},
		{"INSERT INTO t VALUES(2,'x') RETURNING WITH(OLD AS x,NEW AS x) id", "42712", `table name "x" specified more than once`, "NEW AS x", ""},
		{"INSERT INTO t AS x VALUES(2,'x') RETURNING WITH(OLD AS x) id", "42712", `table name "x" specified more than once`, "OLD AS x", ""},
		{"INSERT INTO t VALUES(2,'x') RETURNING WITH(OLD AS o) old.id", "42P01", `missing FROM-clause entry for table "old"`, "old.id", ""},
		{"INSERT INTO t VALUES(2,'x') RETURNING WITH(NEW AS n) new.id", "42P01", `missing FROM-clause entry for table "new"`, "new.id", ""},
		{"INSERT INTO t AS x VALUES(2,'x') RETURNING t.id", "42P01", `invalid reference to FROM-clause entry for table "t"`, "t.id", `Perhaps you meant to reference the table alias "x".`},
		{"INSERT INTO t VALUES(2,'x') ON CONFLICT(id) DO UPDATE SET v=excluded.v RETURNING excluded.v", "42P01", `invalid reference to FROM-clause entry for table "excluded"`, "excluded.v", `There is an entry for table "excluded", but it cannot be referenced from this part of the query.`},
		{"INSERT INTO t SELECT s.id,s.v FROM t AS s RETURNING s.id", "42P01", `missing FROM-clause entry for table "s"`, "s.id", ""},
		{"INSERT INTO t VALUES(2,'é') RETURNING missing", "42703", `column "missing" does not exist`, "missing", ""},
		{"INSERT INTO t VALUES(2,'x') RETURNING sum(id)", "42803", "aggregate functions are not allowed in RETURNING", "sum(id)", ""},
		{"INSERT INTO t VALUES(2,'x') RETURNING avg(id)", "42803", "aggregate functions are not allowed in RETURNING", "avg(id)", ""},
		{"INSERT INTO t VALUES(2,'x') RETURNING max(id)", "42803", "aggregate functions are not allowed in RETURNING", "max(id)", ""},
		{"INSERT INTO t VALUES(2,'x') RETURNING any_value(id)", "42803", "aggregate functions are not allowed in RETURNING", "any_value(id)", ""},
		{"INSERT INTO t VALUES(2,'x') RETURNING string_agg(v,',')", "42803", "aggregate functions are not allowed in RETURNING", "string_agg(v,',')", ""},
		{"INSERT INTO t VALUES(2,'x') RETURNING sum(missing)", "42703", `column "missing" does not exist`, "missing", ""},
		{"INSERT INTO t VALUES(2,'x') RETURNING sum(v)", "42883", "function sum(text) does not exist", "sum(v)", "No function matches the given name and argument types. You might need to add explicit type casts."},
		{"INSERT INTO t VALUES(2,'x') RETURNING row_number() OVER ()", "42P20", "window functions are not allowed in RETURNING", "row_number()", ""},
		{"INSERT INTO t VALUES(2,'x') RETURNING generate_series(1,2)", "0A000", "set-returning functions are not allowed in RETURNING", "generate_series(1,2)", ""},
	}
	for i, tc := range cases {
		t.Run(tc.code+"/"+fixture.Position(i+1), func(t *testing.T) {
			insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := func(s fixture.Step) { insertReturningRun(t, p, b, s) }
				run(fixture.Exec("CREATE TABLE t(id INT PRIMARY KEY,v TEXT)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,'sentinel')", 1))
				d := fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: insertReturningPosition(tc.q, tc.token)}
				if tc.token == "excluded.v" {
					d.Detail, d.Hint = d.Hint, ""
				}
				if tc.token == "generate_series(1,2)" && !b.PG {
					// Generic calls retain lexer.call's parser admission boundary
					// (internal/parser/parser.go); no native SRF provider is added.
					d.Message = "functions are unsupported"
					d.Hint = ""
				}
				s := fixture.ImplicitError(tc.q, d)
				s.Prepared = prepared
				s.SelectRows = true
				run(s)
				run(fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "sentinel"}}))
			})
		})
	}
	insertReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		insertReturningRun(t, p, b, fixture.Exec("CREATE TABLE empty()", 0))
		q := "INSERT INTO empty DEFAULT VALUES RETURNING *"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "RETURNING must have at least one column", Position: insertReturningPosition(q, "*")})
		s.Prepared = prepared
		s.SelectRows = true
		insertReturningRun(t, p, b, s)
	})
}
