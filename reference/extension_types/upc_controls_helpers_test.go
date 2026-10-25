package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var upcControlRows = [][]any{
	{int64(1), "978-0-12-345678-6", "123456789012"},
	{int64(2), "978-0-12-345678-6", "123456789012!"},
	{int64(3), nil, nil},
	{int64(4), "978-0-12-345678-6", "000000000000"},
	{int64(5), "978-0-12-345678-6", "999999999993"},
}

const upcControlQuery = "SELECT id,e,v FROM upc_control ORDER BY id"

func upcControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE EXTENSION isn",
		"CREATE TABLE upc_control(id integer NOT NULL,e ean13,v upc)",
		"CREATE TABLE upc_required(v upc NOT NULL)",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO upc_control VALUES(1,'9780123456786','123456789012'),(2,'9780123456786','123456789010!'),(3,NULL,NULL),(4,'9780123456786','000000000000'),(5,'9780123456786','0999999999993')", 5))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO upc_required VALUES('0999999999993!')", 1))
}

func upcControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ query string; rows [][]any }{
		{upcControlQuery, upcControlRows},
		{"SELECT id,e,v FROM upc_control WHERE id=99", [][]any{}},
	} {
		s := upcSelect(b, tc.query, []string{"id", "e", "v"}, []string{"INT4", "EAN13", "UPC"}, tc.rows, []bool{false, true, true})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		held := ean13Held(t, p, c, tc.query)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	for _, empty := range []bool{false, true} {
		q, expected := "SELECT v FROM upc_required", [][]any{{"999999999993!"}}
		if empty {
			q, expected = "SELECT v FROM upc_required WHERE false", [][]any{}
		}
		s := upcSelect(b, q, []string{"v"}, []string{"UPC"}, expected, []bool{false})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		held := ean13Held(t, p, c, q)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v::ean13::text AS e FROM upc_control WHERE id=2", []string{"e"}, []string{"TEXT"}, [][]any{{"012-345678901-2!"}}))
	isbnIdentity(t, p, b, c, "public")
}