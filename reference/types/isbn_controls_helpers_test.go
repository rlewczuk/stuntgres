package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

var isbnControlRows = [][]any{
	{int64(1), "978-0-12-345678-6", "0-12-345678-9"},
	{int64(2), "012-345678901-2", "1-234-56789-X"},
	{int64(3), "979-123456789-6", "979-123456789-6"},
	{int64(4), "123-456789012-8!", "0-12-345678-9!"},
	{int64(5), "978-0-12-345678-6!", "1-234-56789-X!"},
	{int64(6), "000-000000000-0", "1-234-56789-X"},
	{int64(7), nil, nil},
	{int64(8), "978-0-00-000000-2", "0-00-000000-0"},
}

const isbnControlDDL = "CREATE TABLE isbn_control(id integer NOT NULL,e ean13,v isbn)"
const isbnControlInsert = "INSERT INTO isbn_control VALUES(1,'9780123456786','9780123456786'),(2,'0123456789012','123456789X'),(3,'9791234567896','9791234567896'),(4,'1234567890120!','9780123456780!'),(5,'9780123456786!','123456789X!'),(6,'0000000000000','123456789?!'),(7,NULL,NULL),(8,'9780000000002','0000000000')"
const isbnControlQuery = "SELECT id,e,v FROM isbn_control ORDER BY id"

func isbnControlSelect(b fixture.Backend, q string, expected [][]any) fixture.Step {
	return ean13Select(b, q, []string{"id", "e", "v"}, []string{"INT4", "EAN13", "ISBN"}, expected, []bool{false, true, true})
}

func isbnControlSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{"CREATE EXTENSION isn", isbnControlDDL, "CREATE TABLE isbn_required(e ean13 NOT NULL,v isbn NOT NULL)"} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(isbnControlInsert, 8))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO isbn_required VALUES('1234567890120!','1234567890!')", 1))
}

func isbnControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ q string; rows [][]any }{{isbnControlQuery, isbnControlRows}, {"SELECT id,e,v FROM isbn_control WHERE id=99", [][]any{}}} {
		s := isbnControlSelect(b, tc.q, tc.rows)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		held := ean13Held(t, p, c, tc.q)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	for _, empty := range []bool{false, true} {
		q, expected := "SELECT e,v FROM isbn_required", [][]any{{"123-456789012-8!", "1-234-56789-X!"}}
		if empty {
			q, expected = "SELECT e,v FROM isbn_required WHERE false", [][]any{}
		}
		s := ean13Select(b, q, []string{"e", "v"}, []string{"EAN13", "ISBN"}, expected, []bool{false, false})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		held := ean13Held(t, p, c, q)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	}
	isbnIdentity(t, p, b, c, "public")
}

func isbnRetain(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) (*sql.Rows, []*sql.ColumnType) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rows.Close() })
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	return rows, metadata
}

func isbnConsumeRetained(t *testing.T, rows *sql.Rows, metadata []*sql.ColumnType, expected [][]any) {
	t.Helper()
	ean13ColumnMetadata(t, false, metadata, []string{"id", "e", "v"}, []string{"INT4", "EAN13", "ISBN"}, []bool{false, true, true})
	got := [][]any{}
	for rows.Next() {
		var id, e, v any
		if err := rows.Scan(&id, &e, &v); err != nil {
			t.Fatal(err)
		}
		for _, value := range []any{e, v} {
			if value != nil {
				if _, ok := value.(string); !ok {
					t.Fatalf("retained ISN raw = %T, want string", value)
				}
			}
		}
		got = append(got, []any{fixture.Normalize(id), e, v})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("retained rows = %#v, want %#v", got, expected)
	}
}

// PostgreSQL ignores native ownership comments; this executes the actual dump
// SQL, including ordinary type identity/owner directives, not reconstructed DDL.
func isbnReplay(t *testing.T, p *fixture.Pair, dump string, setup []string, verify func(*sql.Conn, fixture.Backend)) {
	t.Helper()
	c, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "RESET ROLE", "DROP EXTENSION IF EXISTS isn CASCADE", "DROP SCHEMA IF EXISTS isbn_src CASCADE", "DROP SCHEMA IF EXISTS isbn_dst CASCADE", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public", "DROP ROLE IF EXISTS isbn_e_owner", "DROP ROLE IF EXISTS isbn_v_owner", "DROP ROLE IF EXISTS isbn_io_owner", "DISCARD ALL"} {
			if _, err := c.ExecContext(ctx, q); err != nil {
				t.Errorf("ISBN replay cleanup %s: %v", q, err)
			}
		}
	}()
	for _, q := range setup {
		if _, err := c.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.ExecContext(p.Ctx, dump); err != nil {
		t.Fatal("actual native ISBN Dump PostgreSQL replay", err)
	}
	for _, q := range []string{"RESET SESSION AUTHORIZATION", "RESET ROLE"} {
		if _, err := c.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	verify(c, fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord})
}
