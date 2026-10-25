package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Random values have independently checked properties, never oracle goldens.
func pgcryptoRandomByteRows(t *testing.T, rows *sql.Rows, count int) []byte {
	t.Helper()
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "BYTEA" {
		t.Fatal("BYTEA metadata", meta, err)
	}
	if !rows.Next() {
		t.Fatal("missing byte row", rows.Err())
	}
	var raw any
	if err := rows.Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var result []byte
	if count < 0 {
		if raw != nil {
			t.Fatalf("NULL raw value: %T %v", raw, raw)
		}
	} else {
		value, ok := raw.([]byte)
		if !ok || len(value) != count {
			t.Fatalf("BYTEA raw %T length %d, want %d", raw, len(value), count)
		}
		result = bytes.Clone(value)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("byte row tail", rows.Err())
	}
	return result
}

func pgcryptoRandomInstall(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
}

func pgcryptoRandomBytes(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	q := "SELECT pgcrypto_part.gen_random_bytes($1) AS value"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, n := range []int{1, 16, 1023, 1024, -1, 16} {
		var arg any = n
		if n < 0 {
			arg = nil
		}
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			if prepared {
				rows, err = stmt.QueryContext(p.Ctx, arg)
			} else {
				rows, err = c.QueryContext(p.Ctx, q, arg)
			}
			if err != nil {
				t.Fatal(err)
			}
			pgcryptoRandomByteRows(t, rows, n)
		}
	}
	for _, arg := range []string{"1", "1024", "'1'", "1::smallint", "NULL::integer"} {
		n := 1
		if arg == "1024" {
			n = 1024
		}
		if arg == "NULL::integer" {
			n = -1
		}
		rows, err := c.QueryContext(p.Ctx, "SELECT pgcrypto_part.gen_random_bytes("+arg+") AS value")
		if err != nil {
			t.Fatal(err)
		}
		pgcryptoRandomByteRows(t, rows, n)
	}
	var first []byte
	allSame := true
	for range 4 {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT setseed(0.25)", []string{"setseed"}, []string{"VOID"}, [][]any{{""}}))
		rows, err := stmt.QueryContext(p.Ctx, 32)
		if err != nil {
			t.Fatal(err)
		}
		value := pgcryptoRandomByteRows(t, rows, 32)
		if first == nil {
			first = value
		} else if !bytes.Equal(first, value) {
			allSame = false
		}
	}
	if allSame {
		t.Fatal("strong random outputs all repeated after SQL setseed")
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT octet_length(pgcrypto_part.gen_random_bytes($1)) AS size", []string{"size"}, []string{"INT4"}, [][]any{{int64(1024)}}, 1024))
}

func TestReferencePgcryptoRandomBytes(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		pgcryptoRandomInstall(t, p, c, r)
		pgcryptoRandomBytes(t, p, c, r)
	})
}
