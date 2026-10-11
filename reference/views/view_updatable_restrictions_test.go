package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func viewWriteReadonly(q, op, name, reason string) fixture.Step {
	verb, hint := "insert into", "inserting into"
	if op == "UPDATE" {
		verb, hint = "update", "updating"
	}
	if op == "DELETE" {
		verb, hint = "delete from", "deleting from"
	}
	return fixture.ImplicitError(q, fixture.Diagnostic{Code: "55000", Message: fmt.Sprintf("cannot %s view %q", verb, name), Detail: reason, Hint: fmt.Sprintf("To enable %s the view, provide an INSTEAD OF %s trigger or an unconditional ON %s DO INSTEAD rule.", hint, op, op)})
}
func viewWriteComputed(q, op, name, column string) fixture.Step {
	verb := "insert into"
	if op == "UPDATE" {
		verb = "update"
	}
	return fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: fmt.Sprintf("cannot %s column %q of view %q", verb, column, name), Detail: "View columns that are not columns of their base relation are not updatable."})
}
func TestReferenceViewUpdatableRestrictions(t *testing.T) {
	shapes := []struct{ name, query, reason string }{
		{"distinct", "SELECT DISTINCT id,a FROM t", "Views containing DISTINCT are not automatically updatable."},
		{"distinct_on", "SELECT DISTINCT ON(id) id,a FROM t", "Views containing DISTINCT are not automatically updatable."},
		{"reason_precedence", "SELECT DISTINCT id,a FROM t GROUP BY id,a LIMIT 1", "Views containing DISTINCT are not automatically updatable."},
		{"grouped", "SELECT id,a FROM t GROUP BY id,a", "Views containing GROUP BY are not automatically updatable."},
		{"having", "SELECT count(*) AS id FROM t HAVING count(*)>0", "Views containing HAVING are not automatically updatable."},
		{"aggregate", "SELECT count(*) AS id FROM t", "Views that return aggregate functions are not automatically updatable."},
		{"windowed", "SELECT id,row_number() OVER() AS a FROM t", "Views that return window functions are not automatically updatable."},
		{"unioned", "SELECT id,a FROM t UNION ALL SELECT id,a FROM t", "Views containing UNION, INTERSECT, or EXCEPT are not automatically updatable."},
		{"limited", "SELECT id,a FROM t LIMIT 1", "Views containing LIMIT or OFFSET are not automatically updatable."},
		{"offsetted", "SELECT id,a FROM t OFFSET 0", "Views containing LIMIT or OFFSET are not automatically updatable."},
		{"joined", "SELECT t.id,t.a FROM t JOIN u ON t.id=u.id", "Views that do not select from a single table or view are not automatically updatable."},
		{"selfjoined", "SELECT x.id,x.a FROM t x,t y", "Views that do not select from a single table or view are not automatically updatable."},
		{"derived", "SELECT id,a FROM (SELECT id,a FROM t) x", "Views that do not select from a single table or view are not automatically updatable."},
		{"values_source", "SELECT column1 AS id FROM (VALUES(1)) x", "Views that do not select from a single table or view are not automatically updatable."},
		{"no_source", "SELECT 1 AS id", "Views that do not select from a single table or view are not automatically updatable."},
		{"sequence_source", "SELECT last_value AS id FROM seq", "Views that do not select from a single table or view are not automatically updatable."},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
				run := func(s fixture.Step) { viewWriteRun(t, p, b, held, s) }
				run(fixture.Exec("CREATE TABLE t(id INT,a INT)", 0))
				run(fixture.Exec("CREATE TABLE u(id INT)", 0))
				run(fixture.Exec("CREATE SEQUENCE seq", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,10),(1,10)", 2))
				run(fixture.Exec("CREATE VIEW v AS "+shape.query, 0))
				for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
					q := map[string]string{"INSERT": "INSERT INTO v(id) SELECT 9 WHERE FALSE", "UPDATE": "UPDATE v SET id=9 WHERE FALSE", "DELETE": "DELETE FROM v WHERE FALSE"}[op]
					s := viewWriteReadonly(q, op, "v", shape.reason)
					// Full reason and hint are independent release-source expectations.
					run(s)
				}
				run(viewWriteBag("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(10)}}))
			})
		})
	}
	viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s fixture.Step) { viewWriteRun(t, p, b, held, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE t(id INT,a INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(1,10)", 2), fixture.Exec("CREATE VIEW m AS SELECT id,a,a+1 AS calc,a+2 AS other FROM t", 0),
			fixture.Exec("INSERT INTO m(id,a) VALUES(2,20)", 1),
			viewWriteBag("UPDATE m SET a=calc WHERE id=1 RETURNING id,a,calc", []string{"id", "a", "calc"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(11), int64(12)}, {int64(1), int64(11), int64(12)}}),
			viewWriteComputed("INSERT INTO m(calc) VALUES(1)", "INSERT", "m", "calc"), viewWriteComputed("INSERT INTO m(calc) VALUES(NULL)", "INSERT", "m", "calc"), viewWriteComputed("INSERT INTO m(calc) VALUES(DEFAULT)", "INSERT", "m", "calc"), viewWriteComputed("INSERT INTO m(calc) SELECT 1 WHERE FALSE", "INSERT", "m", "calc"),
			viewWriteComputed("UPDATE m SET calc=1 WHERE FALSE", "UPDATE", "m", "calc"), viewWriteComputed("UPDATE m SET calc=NULL WHERE FALSE", "UPDATE", "m", "calc"), viewWriteComputed("UPDATE m SET calc=DEFAULT WHERE FALSE", "UPDATE", "m", "calc"), viewWriteComputed("UPDATE m SET other=1,calc=2 WHERE FALSE", "UPDATE", "m", "calc"),
			fixture.Exec("CREATE VIEW outer_m AS SELECT calc AS renamed FROM m", 0), viewWriteComputed("UPDATE outer_m SET renamed=1", "UPDATE", "m", "calc"),
			fixture.Exec("CREATE VIEW repeated AS SELECT a,a AS aa FROM t", 0), fixture.Exec("UPDATE repeated SET aa=aa+1 WHERE a=20", 1),
			fixture.ImplicitError("UPDATE repeated SET a=1,aa=2", fixture.Diagnostic{Code: "42601", Message: "multiple assignments to same column \"a\""}),
			fixture.ImplicitError("INSERT INTO repeated(a,aa) VALUES(1,2)", fixture.Diagnostic{Code: "42601", Message: "multiple assignments to same column \"a\""}),
			fixture.Exec("CREATE VIEW computed AS SELECT a+1 AS calc FROM t", 0),
			viewWriteReadonly("INSERT INTO computed VALUES(1)", "INSERT", "computed", "Views that have no updatable columns are not automatically updatable."),
			fixture.ImplicitError("UPDATE computed SET calc=1", fixture.Diagnostic{Code: "55000", Message: "cannot update view \"computed\"", Detail: "Views that have no updatable columns are not automatically updatable.", Hint: "To enable updating the view, provide an INSTEAD OF UPDATE trigger or an unconditional ON UPDATE DO INSTEAD rule."}),
			fixture.Exec("DELETE FROM computed WHERE calc=22", 1),
			fixture.Exec("CREATE VIEW zero AS SELECT FROM t", 0),
			viewWriteReadonly("INSERT INTO zero DEFAULT VALUES", "INSERT", "zero", "Views that have no updatable columns are not automatically updatable."),
			fixture.Exec("DELETE FROM zero", 2),
			fixture.Select("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, nil),
			fixture.Exec("INSERT INTO t VALUES(3,0)", 1), fixture.Exec("CREATE VIEW demand AS SELECT id,a,10/a AS unsafe FROM t ORDER BY id", 0), fixture.Exec("UPDATE demand SET id=4", 1), fixture.Exec("DELETE FROM demand", 1),
			fixture.Exec("CREATE VIEW scalar_aggregate AS SELECT id,(SELECT count(*) FROM u) AS n FROM t", 0),
		} {
			// Scalar subquery source is a separate existing table, not a top-level aggregate.
			if s.Query == "CREATE VIEW scalar_aggregate AS SELECT id,(SELECT count(*) FROM u) AS n FROM t" {
				run(fixture.Exec("CREATE TABLE u(id INT)", 0))
			}
			run(s)
		}
		run(fixture.Exec("INSERT INTO scalar_aggregate(id) VALUES(5)", 1))
		run(fixture.Exec("DELETE FROM scalar_aggregate", 1))
	})
}
func TestReferenceViewUpdatableDiagnostics(t *testing.T) {
	viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s fixture.Step) { viewWriteRun(t, p, b, held, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,a INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(fixture.Exec("CREATE VIEW v AS SELECT DISTINCT id,a FROM t", 0))
		run(fixture.Exec("CREATE VIEW \"vé\" AS SELECT DISTINCT id,a FROM t", 0))
		for _, s := range []fixture.Step{
			fixture.ImplicitError("UPDATE v SET missing=1", fixture.Diagnostic{Code: "42703", Message: "column \"missing\" of relation \"v\" does not exist", Position: "14"}),
			fixture.ImplicitError("UPDATE v SET id=1 WHERE 1", fixture.Diagnostic{Code: "42804", Message: "argument of WHERE must be type boolean, not type integer", Position: "25"}),
			fixture.ImplicitError("INSERT INTO absent VALUES(1)", fixture.Diagnostic{Code: "42P01", Message: "relation \"absent\" does not exist", Position: "13"}),
			fixture.ImplicitError("UPDATE \"vé\" SET missing=1", fixture.Diagnostic{Code: "42703", Message: "column \"missing\" of relation \"vé\" does not exist", Position: "17"}),
			fixture.ImplicitError("UPDATE v SET id=TRUE", fixture.Diagnostic{Code: "42804", Message: "column \"id\" is of type integer but expression is of type boolean", Hint: "You will need to rewrite or cast the expression.", Position: "17"}),
		} {
			run(s)
		}
		run(fixture.Select("SELECT id,a FROM t", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}}))
	})
}
