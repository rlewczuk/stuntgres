package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var isbn13ControlRows = [][]any{
	{int64(1), "978-0-12-345678-6", "978-0-12-345678-6"},
	{int64(2), "012-345678901-2", "978-1-234-56789-7"},
	{int64(3), "979-123456789-6", "979-123456789-6"},
	{int64(4), "123-456789012-8!", "978-0-12-345678-6!"},
	{int64(5), "978-0-12-345678-6!", "978-1-234-56789-7!"},
	{int64(6), "000-000000000-0", "978-1-234-56789-7"},
	{int64(7), nil, nil},
	{int64(8), "978-0-00-000000-2", "978-0-00-000000-2"},
}

const isbn13ControlQuery = "SELECT id,e,v FROM isbn13_control ORDER BY id"

func isbn13ControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE EXTENSION isn",
		"CREATE TABLE isbn13_control(id integer NOT NULL,e ean13,v isbn13)",
		"CREATE TABLE isbn13_required(e isbn NOT NULL,v isbn13 NOT NULL)",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO isbn13_control VALUES(1,'9780123456786','9780123456786'),(2,'0123456789012','123456789X'),(3,'9791234567896','9791234567896'),(4,'1234567890120!','9780123456780!'),(5,'9780123456786!','123456789X!'),(6,'0000000000000','123456789?!'),(7,NULL,NULL),(8,'9780000000002','0000000000')", 8))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO isbn13_required VALUES('1234567890!','1234567890!')", 1))
}

func isbn13ControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ query string; rows [][]any }{
		{isbn13ControlQuery, isbn13ControlRows},
		{"SELECT id,e,v FROM isbn13_control WHERE id=99", [][]any{}},
	} {
		s := fixture.Ean13Select(b, tc.query, []string{"id", "e", "v"}, []string{"INT4", "EAN13", "ISBN13"}, tc.rows, []bool{false, true, true})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		held := ean13Held(t, p, c, tc.query)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	for _, empty := range []bool{false, true} {
		q, expected := "SELECT e,v FROM isbn13_required", [][]any{{"1-234-56789-X!", "978-1-234-56789-7!"}}
		if empty {
			q, expected = "SELECT e,v FROM isbn13_required WHERE false", [][]any{}
		}
		s := fixture.Ean13Select(b, q, []string{"e", "v"}, []string{"ISBN", "ISBN13"}, expected, []bool{false, false})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		held := ean13Held(t, p, c, q)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	isbnIdentity(t, p, b, c, "public")
}
