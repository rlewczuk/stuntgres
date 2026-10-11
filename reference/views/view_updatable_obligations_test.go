package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewUpdatableObligations(t *testing.T) {
	viewWriteRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, held bool) {
		run := func(s fixture.Step) { viewWriteRun(t, p, b, held, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE t(id INT GENERATED ALWAYS AS IDENTITY,a INT,g INT GENERATED ALWAYS AS(a+1) STORED)", 0), fixture.Exec("CREATE VIEW v AS SELECT id AS key,a AS x,g AS calc FROM t", 0),
			fixture.ImplicitError("INSERT INTO v(key,x) VALUES(10,1)", fixture.Diagnostic{Code: "428C9", Message: "cannot insert a non-DEFAULT value into column \"id\"", Detail: "Column \"id\" is an identity column defined as GENERATED ALWAYS.", Hint: "Use OVERRIDING SYSTEM VALUE to override."}),
			fixture.ImplicitError("INSERT INTO v(x,calc) VALUES(1,2)", fixture.Diagnostic{Code: "428C9", Message: "cannot insert a non-DEFAULT value into column \"g\"", Detail: "Column \"g\" is a generated column."}),
			fixture.Exec("INSERT INTO v(x,calc) VALUES(5,DEFAULT)", 1),
			fixture.ImplicitError("UPDATE v SET calc=9", fixture.Diagnostic{Code: "428C9", Message: "column \"g\" can only be updated to DEFAULT", Detail: "Column \"g\" is a generated column."}),
			// The pinned oracle rejects explicit generated DEFAULT through this renamed
			// view target. Assert rejection and atomicity; an ordinary writable-column
			// update must still recompute the base generated value.
			fixture.ImplicitError("UPDATE v SET x=7,calc=DEFAULT", fixture.Diagnostic{Code: "428C9", Message: "column \"g\" can only be updated to DEFAULT", Detail: "Column \"g\" is a generated column."}),
			fixture.Select("SELECT id,a,g FROM t", []string{"id", "a", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(5), int64(6)}}),
			fixture.Exec("UPDATE v SET x=7", 1),
			fixture.Select("SELECT id,a,g FROM t", []string{"id", "a", "g"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(8)}}),
		} {
			run(s)
		}
	})
}
