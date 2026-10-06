//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func TestReferenceByteaOrderingSettingsAndLexing(t *testing.T) {
	p := newReferencePair(t)
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		conn, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := conn.ExecContext(p.ctx, q, args...); err != nil {
				t.Fatal(q, err)
			}
		}
		check := func(q string, want []any, args ...any) {
			t.Helper()
			rows, err := conn.QueryContext(p.ctx, q, args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			i := 0
			for rows.Next() {
				var got any
				if err := rows.Scan(&got); err != nil {
					t.Fatal(err)
				}
				if i >= len(want) || !reflect.DeepEqual(got, want[i]) {
					t.Fatalf("%s row %d: %#v want %#v", q, i, got, want)
				}
				i++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if i != len(want) {
				t.Fatal(i, len(want))
			}
		}
		show := func(want string) {
			t.Helper()
			var got string
			if err := conn.QueryRowContext(p.ctx, "SHOW bytea_output").Scan(&got); err != nil || got != want {
				t.Fatal(got, err)
			}
		}
		exec("CREATE TABLE t(v BYTEA)")
		exec(`INSERT INTO t VALUES('\xff'),('\x80'),('\x7f'),('\x0000'),('\x00'),(''),(NULL)`)
		asc := []any{[]byte{}, []byte{0}, []byte{0, 0}, []byte{127}, []byte{128}, []byte{255}, nil}
		check("SELECT v FROM t ORDER BY v", asc)
		check("SELECT v FROM t ORDER BY v DESC", []any{nil, []byte{255}, []byte{128}, []byte{127}, []byte{0, 0}, []byte{0}, []byte{}})
		s, err := conn.PrepareContext(p.ctx, "SELECT v FROM t WHERE v=$1")
		if err != nil {
			t.Fatal(err)
		}
		for _, arg := range []any{[]byte{0}, []byte{255}, nil} {
			rows, err := s.QueryContext(p.ctx, arg)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for rows.Next() {
				var got any
				if err := rows.Scan(&got); err != nil || !reflect.DeepEqual(got, arg) {
					t.Fatal(got, arg, err)
				}
				n++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			want := 1
			if arg == nil {
				want = 0
			}
			if n != want {
				t.Fatal(n, want)
			}
		}
		s.Close()
		show("hex")
		exec("SET bytea_output=ESCAPE")
		show("escape")
		escapeAsc := []any{[]byte{}, []byte{0}, []byte{0, 0}, []byte{127}, []byte{128}, []byte{255}, nil}
		if db == p.postgres {
			// pq's escape decoder returns a typed nil slice for empty bytea,
			// unlike its hex decoder. SQL NULL remains the nil interface.
			escapeAsc[0] = []byte(nil)
		}
		check("SELECT v FROM t ORDER BY v", escapeAsc)
		exec("SET bytea_output=DEFAULT")
		show("hex")
		check("SELECT v FROM t ORDER BY v", asc)
		exec("SET bytea_output=escape")
		exec("RESET bytea_output")
		show("hex")
		exec("CREATE TABLE nn(v BYTEA,n INTEGER NOT NULL)")
		exec("BEGIN")
		exec("SET LOCAL bytea_output=escape")
		show("escape")
		_, err = conn.ExecContext(p.ctx, `INSERT INTO nn VALUES('\x00',NULL)`)
		want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "n" of relation "nn" violates not-null constraint`, Detail: `Failing row contains (\000, null).`, Schema: "public", Table: "nn", Column: "n"}
		referenceAssertDiagnostic(t, err, &want)
		exec("ROLLBACK")
		show("hex")
		exec("CREATE TABLE lex(id INTEGER,v BYTEA)")
		for i, literal := range []string{`$$\x0080ff$$`, `E'\\x0080ff'`, `U&'\005Cx0080ff'`} {
			exec("INSERT INTO lex VALUES($1,"+literal+")", i)
		}
		exec("SET standard_conforming_strings=off")
		exec(`INSERT INTO lex VALUES(3,'\\x0080ff')`)
		exec("SET standard_conforming_strings=on")
		check("SELECT v FROM lex ORDER BY id", []any{[]byte{0, 128, 255}, []byte{0, 128, 255}, []byte{0, 128, 255}, []byte{0, 128, 255}})
		for _, literal := range []struct {
			sql  string
			want []byte
		}{
			{`''`, []byte{}}, {`'\x'`, []byte{}}, {`'\x00aAfF'`, []byte{0, 170, 255}},
			{"'\\x 00\tAA\nff\r'", []byte{0, 170, 255}}, {`'\000\177\200\377'`, []byte{0, 127, 128, 255}},
			{`'\\'`, []byte{92}}, {`''''`, []byte{39}}, {`'é'`, []byte{195, 169}},
		} {
			s, err := conn.PrepareContext(p.ctx, "INSERT INTO lex VALUES(99,"+literal.sql+")")
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.ExecContext(p.ctx)
			s.Close()
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			check("SELECT v FROM lex WHERE id=99", []any{literal.want})
			// Use a fresh table for the next independent literal without DELETE scope.
			exec("DROP TABLE lex")
			exec("CREATE TABLE lex(id INTEGER,v BYTEA)")
		}
		exec("CREATE TABLE repeats(a BYTEA,b BYTEA)")
		exec("INSERT INTO repeats VALUES($1,$1)", []byte{0, 255})
		rows, err := conn.QueryContext(p.ctx, "SELECT * FROM repeats")
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			t.Fatal("missing repeat")
		}
		var a, b any
		if err := rows.Scan(&a, &b); err != nil || !reflect.DeepEqual(a, []byte{0, 255}) || !reflect.DeepEqual(b, []byte{0, 255}) {
			t.Fatal(a, b, err)
		}
		rows.Close()
		conn.Close()
	}
}
