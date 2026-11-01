package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func crosstabCategoriesCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	crosstabCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		body(t, p, b, c)
	})
}

func crosstabCategoriesBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func crosstabCategoriesSQL(routine, declaration string) string {
	return "SELECT * FROM " + routine + "($1::text,$2::text) AS t(" + declaration + ")"
}

// The ordinal is explicit: category output order, not SQL literal bag order, is contractual.
func crosstabCategoriesOrder(categories ...string) string {
	rows := make([]string, len(categories))
	for i, category := range categories {
		rows[i] = "(" + strconv.Itoa(i) + "," + fixture.CrosstabQuote(category) + "::text)"
	}
	return "SELECT category FROM (VALUES " + strings.Join(rows, ",") + ") AS cats(ord,category) ORDER BY ord"
}

const crosstabCategoriesExtrasSource = "SELECT k,metadata,category,v FROM public.ct_extras ORDER BY seq"

func crosstabCategoriesExtras(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE ct_extras(seq integer,k text,metadata text,category text,v text)", 0),
		fixture.Exec("INSERT INTO ct_extras VALUES(1,'g1','first','ignored','not-an-integer'),(2,'g1','later','b','20'),(3,'g1','later','a','10')", 3),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func crosstabCategoriesExtrasWant(q string, b int64) fixture.Step {
	return fixture.Select(q, []string{"k", "metadata", "b", "a", "c"}, []string{"TEXT", "TEXT", "INT4", "INT4", "INT4"}, [][]any{{"g1", "first", b, int64(10), nil}}, crosstabCategoriesExtrasSource, crosstabCategoriesOrder("b", "a", "c"))
}

func crosstabCategoriesRows(q string, rows [][]any, source, categories string) fixture.Step {
	return fixture.Select(q, []string{"k", "a", "b"}, []string{"TEXT", "TEXT", "TEXT"}, rows, source, categories)
}

func crosstabCategoriesDiagnostic(code, message, detail string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Detail = detail
	return d
}
