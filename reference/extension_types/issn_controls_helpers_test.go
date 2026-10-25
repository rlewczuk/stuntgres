package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var issnControlRows = [][]any{
	{int64(1), "978-0-12-345678-6", "1234-5679"},
	{int64(2), "978-0-12-345678-6", "1234-5679!"},
	{int64(3), nil, nil},
	{int64(4), "978-0-12-345678-6", "977-1234-567-89-8"},
	{int64(5), "978-0-12-345678-6", "977-1234-567-89-8!"},
}

const issnControlQuery = "SELECT id,e,v FROM issn_control ORDER BY id"

func issnControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE EXTENSION isn",
		"CREATE TABLE issn_control(id integer NOT NULL,e ean13,v issn)",
		"CREATE TABLE issn_required(v issn NOT NULL)",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO issn_control VALUES(1,'9780123456786','12345679'),(2,'9780123456786','12345670!'),(3,NULL,NULL),(4,'9780123456786','9771234567898'),(5,'9780123456786','9771234567898!')", 5))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO issn_required VALUES('9771234567898!')", 1))
}

func issnControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ query string; rows [][]any }{
		{issnControlQuery, issnControlRows},
		{"SELECT id,e,v FROM issn_control WHERE id=99", [][]any{}},
	} {
		s := issnSelect(b, tc.query, []string{"id", "e", "v"}, []string{"INT4", "EAN13", "ISSN"}, tc.rows, []bool{false, true, true})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		held := ean13Held(t, p, c, tc.query)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	for _, empty := range []bool{false, true} {
		q, expected := "SELECT v FROM issn_required", [][]any{{"977-1234-567-89-8!"}}
		if empty {
			q, expected = "SELECT v FROM issn_required WHERE false", [][]any{}
		}
		s := issnSelect(b, q, []string{"v"}, []string{"ISSN"}, expected, []bool{false})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		held := ean13Held(t, p, c, q)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	isbnIdentity(t, p, b, c, "public")
}
