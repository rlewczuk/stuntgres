package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

const xml2GuardInsert = "INSERT INTO xml2_guard VALUES($1::integer,$2::text,$3::text)"

func xml2GuardError(schema, row string) fixture.Diagnostic {
	d := xml2Error("23514", `new row for relation "xml2_guard" violates check constraint "xml2_guard_xpath"`)
	d.Detail = "Failing row contains (" + row + ")."
	d.Schema, d.Table, d.Constraint = schema, "xml2_guard", "xml2_guard_xpath"
	return d
}

// Every invocation demands the stored CHECK with new writes. Old sentinel rows
// alone cannot demonstrate that a renamed or restored expression still executes.
func xml2Guard(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, schema, routine string) {
	t.Helper()
	read := func(extra ...[]any) {
		t.Helper()
		want := append([][]any{{int64(1), "<r>12</r>", "."}}, extra...)
		xml2Both(t, p, b, c, fixture.Select("SELECT id,doc,path FROM xml2_guard ORDER BY id", []string{"id", "doc", "path"}, []string{"INT4", "TEXT", "TEXT"}, want))
	}
	read()
	dependencies := "SELECT p.proname,d.deptype FROM pg_depend d JOIN pg_constraint k ON d.classid=2606 AND d.objid=k.oid JOIN pg_namespace n ON n.oid=k.connamespace JOIN pg_proc p ON d.refclassid=1255 AND d.refobjid=p.oid WHERE n.nspname=$1 AND k.conname='xml2_guard_xpath' ORDER BY p.proname"
	xml2Both(t, p, b, c, fixture.Select(dependencies, []string{"proname", "deptype"}, []string{"NAME", "CHAR"}, [][]any{{routine, "n"}}, schema))
	stmt, err := c.PrepareContext(p.Ctx, xml2GuardInsert)
	if err != nil {
		t.Fatal("quiet held CHECK write preparation", err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: xml2GuardInsert}
	write := func(x fixture.Executor, prepared bool, s fixture.Step) {
		t.Helper()
		s.Prepared = prepared
		fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
	}
	failure := func(x fixture.Executor, prepared bool, q string, d fixture.Diagnostic, args ...any) {
		t.Helper()
		s := fixture.ErrorDiagnostic(q, d, args...)
		s.ErrorStage = "execute"
		write(x, prepared, s)
		// Check failed-statement publication before any ordinary cleanup.
		read()
	}
	success := func(x fixture.Executor, prepared bool, id int64, doc, path any) {
		t.Helper()
		write(x, prepared, fixture.Exec(xml2GuardInsert, 1, id, doc, path))
		read([]any{id, doc, path})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM xml2_guard WHERE id=$1", 1, id))
		read()
	}
	probe := func(x fixture.Executor, prepared bool) {
		t.Helper()
		failure(x, prepared, xml2GuardInsert, xml2GuardError(schema, "2, <r/>, ."), int64(2), "<r/>", ".")
		success(x, prepared, 2, "<r>12</r>", ".")
		success(x, prepared, 3, nil, "[")
		success(x, prepared, 4, "<r>", nil)
		// Malformed XML projects NULL before the malformed XPath is compiled.
		success(x, prepared, 5, "<r>", "[")
		failure(x, prepared, xml2GuardInsert, xml2GuardError(schema, "2, <r>12</r>, /missing"), int64(2), "<r>12</r>", "/missing")
		failure(x, prepared, xml2GuardInsert, xml2Compile("Invalid expression"), int64(5), "<r/>", "[")
		success(x, prepared, 2, "<r>12</r>", ".")
	}
	for _, prepared := range []bool{false, true} {
		probe(c, prepared)
		// Separate autocommit statements: a later failing row must not publish
		// the earlier valid row. No surrounding transaction hides atomicity.
		failure(c, prepared, "INSERT INTO xml2_guard VALUES (10,'<r>ok</r>','.'),(11,'<r/>','.')", xml2GuardError(schema, "11, <r/>, ."))
		failure(c, prepared, "INSERT INTO xml2_guard VALUES (12,'<r>ok</r>','.'),(13,'<r/>','[')", xml2Compile("Invalid expression"))
	}
	// The same real held INSERT survives failures and changing NULL inputs,
	// including healthy reuse after the separate atomicity failures above.
	probe(held, false)
}
