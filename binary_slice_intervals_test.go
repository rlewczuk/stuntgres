package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

func TestBinarySliceIntervalsAndForms(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, tc := range []struct {
		s, n int
		want string
	}{{1, 3, "abc"}, {0, 3, "ab"}, {-2, 4, "a"}, {-3, 2, ""}, {2, 0, ""}, {7, 2, ""}, {2, 2147483646, "bcdef"}, {-10, 2147483646, "abcdef"}, {-2147483648, 2147483647, ""}} {
		for _, form := range []string{"substring('abcdef'::bytea,%d,%d)", "substr('abcdef'::bytea,%d,%d)", "substring('abcdef'::bytea FROM %d FOR %d)", "substring('abcdef'::bytea FOR %[2]d FROM %[1]d)"} {
			q := "SELECT " + fmt.Sprintf(form, tc.s, tc.n)
			wantTuples(t, db, q, [][]any{{[]byte(tc.want)}})
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal(q, err)
			}
			var got []byte
			err = stmt.QueryRow().Scan(&got)
			stmt.Close()
			if err != nil || string(got) != tc.want {
				t.Fatal(q, got, err)
			}
		}
	}
	for _, tc := range []struct {
		s    int
		want string
	}{{-2, "abcdef"}, {0, "abcdef"}, {1, "abcdef"}, {2, "bcdef"}, {7, ""}, {2147483647, ""}} {
		wantTuples(t, db, fmt.Sprintf("SELECT substring('abcdef'::bytea FROM %d),substr('abcdef'::bytea,%d)", tc.s, tc.s), [][]any{{[]byte(tc.want), []byte(tc.want)}})
	}
	for _, count := range []string{"2::smallint", "2::bigint", "1.5::numeric", "'2'::text", "TRUE"} {
		want := "ab"
		if count == "TRUE" {
			want = "a"
		}
		wantTuples(t, db, "SELECT substring('abcdef'::bytea FOR "+count+")", [][]any{{[]byte(want)}})
	}
	wantTuples(t, db, "SELECT substr('abcdef'::bytea,2::smallint,3::smallint)", [][]any{{[]byte("bcd")}})
	for _, tc := range []struct{ form, want string }{{"trim(%s FROM %s)", "A\x00\x80B"}, {"trim(BOTH %s FROM %s)", "A\x00\x80B"}, {"trim(LEADING %s FROM %s)", "A\x00\x80B\xff\x00"}, {"trim(TRAILING %s FROM %s)", "\x00\xff\x80A\x00\x80B"}} {
		wantTuples(t, db, "SELECT "+fmt.Sprintf(tc.form, `'\xff8000'::bytea`, `'\x00ff8041008042ff00'::bytea`), [][]any{{[]byte(tc.want)}})
	}
	for _, direction := range []string{"", "BOTH ", "LEADING ", "TRAILING "} {
		want := "A\x00\x80B"
		if direction == "LEADING " {
			want += "\xff\x00"
		}
		if direction == "TRAILING " {
			want = "\x00\xff\x80" + want
		}
		for _, from := range []string{"", "FROM "} {
			wantTuples(t, db, `SELECT trim(`+direction+from+`'\x00ff8041008042ff00'::bytea,'\xff8000'::bytea)`, [][]any{{[]byte(want)}})
		}
	}
}

func TestBinarySliceHeldBinds(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	stmt, err := db.Prepare("SELECT overlay($1::bytea,$2::bytea,$3,$4),substr($1::bytea,$3,$4),pg_catalog.position($1::bytea,$2::bytea)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, tc := range []struct {
		a, b         any
		s, n         int
		overlay, sub any
		pos          any
	}{{[]byte("abcdef"), []byte("XY"), 3, 2, []byte("abXYef"), []byte("cd"), int64(0)}, {[]byte{}, []byte{}, 1, 0, []byte{}, []byte{}, int64(1)}, {[]byte(nil), []byte("x"), 1, 1, nil, nil, nil}, {`\x41`, []byte("x"), 2, 1, []byte(`\x41`), []byte("x"), int64(2)}} {
		rows, err := stmt.Query(tc.a, tc.b, tc.s, tc.n)
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var a, b, c any
		if err := rows.Scan(&a, &b, &c); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !binarySliceEqual(a, tc.overlay) || !binarySliceEqual(b, tc.sub) || c != tc.pos {
			t.Fatal(a, b, c, tc)
		}
	}
}

func binarySliceEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	x, xok := a.([]byte)
	y, yok := b.([]byte)
	return xok && yok && x != nil && y != nil && string(x) == string(y)
}
