package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var ismn13ControlRows = [][]any{
	{int64(1), "978-0-12-345678-6", "979-0-1234-5678-5"},
	{int64(2), "012-345678901-2", "979-0-1234-5678-5"},
	{int64(3), "979-123456789-6", "979-0-1234-5678-5!"},
	{int64(4), "123-456789012-8!", "979-0-1234-5678-5!"},
	{int64(5), "978-0-12-345678-6!", "979-0-1234-5678-5"},
	{int64(6), "000-000000000-0", "979-0-000-00000-1"},
	{int64(7), nil, nil},
	{int64(8), "978-0-00-000000-2", "979-0-9999999-9-7"},
}

const ismn13ControlQuery = "SELECT id,e,v FROM ismn13_control ORDER BY id"

func ismn13ControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE EXTENSION isn",
		"CREATE TABLE ismn13_control(id integer NOT NULL,e ean13,v ismn13)",
		"CREATE TABLE ismn13_required(e isbn NOT NULL,l isbn13 NOT NULL,v ismn13 NOT NULL)",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ismn13_control VALUES(1,'9780123456786','M123456785'),(2,'0123456789012','9790123456785'),(3,'9791234567896','M123456785!'),(4,'1234567890120!','M123456780!'),(5,'9780123456786!','M12345678?!'),(6,'0000000000000','M000000001'),(7,NULL,NULL),(8,'9780000000002','9790999999997')", 8))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ismn13_required VALUES('1234567890!','1234567890!','M123456780!')", 1))
}

func ismn13ControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ query string; rows [][]any }{
		{ismn13ControlQuery, ismn13ControlRows},
		{"SELECT id,e,v FROM ismn13_control WHERE id=99", [][]any{}},
	} {
		s := ean13Select(b, tc.query, []string{"id", "e", "v"}, []string{"INT4", "EAN13", "ISMN13"}, tc.rows, []bool{false, true, true})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		held := ean13Held(t, p, c, tc.query)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	for _, empty := range []bool{false, true} {
		q, expected := "SELECT e,l,v FROM ismn13_required", [][]any{{"1-234-56789-X!", "978-1-234-56789-7!", "979-0-1234-5678-5!"}}
		if empty {
			q, expected = "SELECT e,l,v FROM ismn13_required WHERE false", [][]any{}
		}
		s := ean13Select(b, q, []string{"e", "l", "v"}, []string{"ISBN", "ISBN13", "ISMN13"}, expected, []bool{false, false, false})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		held := ean13Held(t, p, c, q)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	isbnIdentity(t, p, b, c, "public")
}
