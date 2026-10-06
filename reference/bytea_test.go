//go:build reference

package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
)

func TestReferenceByteaInputBindsMetadata(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		db *sql.DB
		pg bool
	}{{p.memory, false}, {p.postgres, true}} {
		db := backend.db
		exec := func(q string, n int64, args ...any) {
			t.Helper()
			r, err := db.ExecContext(p.ctx, q, args...)
			if err != nil {
				t.Fatal(q, err)
			}
			got, err := r.RowsAffected()
			if err != nil || got != n {
				t.Fatal(got, err)
			}
		}
		exec("CREATE TABLE t(id INTEGER,v BYTEA)", 0)
		exec("INSERT INTO t VALUES(1,''),(2,'\\x'),(3,'\\x00aAfF'),(4,'\\x 00\tAA\nff\r'),(5,'\\000\\177\\200\\377'),(6,'\\\\'),(7,'''') ,(8,'é')", 8)
		want := []any{[]byte{}, []byte{}, []byte{0, 170, 255}, []byte{0, 170, 255}, []byte{0, 127, 128, 255}, []byte{92}, []byte{39}, []byte{195, 169}}
		for _, prepared := range []bool{false, true} {
			s, err := db.PrepareContext(p.ctx, "INSERT INTO t VALUES($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct{ arg, want any }{
				{string([]byte{0, 128, 255}), []byte{0, 128, 255}}, {[]byte{0, 128, 255}, []byte{0, 128, 255}},
				{`\x41`, []byte{92, 120, 52, 49}}, {[]byte(`\000`), []byte{92, 48, 48, 48}},
				{nil, nil}, {[]byte(nil), nil}, {[]byte{}, []byte{}}, {"", []byte{}},
			} {
				arg := tc.arg
				var r sql.Result
				if prepared {
					r, err = s.ExecContext(p.ctx, len(want)+1, arg)
				} else {
					r, err = db.ExecContext(p.ctx, "INSERT INTO t VALUES($1,$2)", len(want)+1, arg)
				}
				if err != nil {
					t.Fatal(err)
				}
				if n, e := r.RowsAffected(); e != nil || n != 1 {
					t.Fatal(n, e)
				}
				want = append(want, tc.want)
			}
			s.Close()
		}
		for _, prepared := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				q := "SELECT v FROM t ORDER BY id"
				if empty {
					q = "SELECT v FROM t WHERE v=NULL"
				}
				var rows *sql.Rows
				var err error
				if prepared {
					s, e := db.PrepareContext(p.ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					rows, err = s.QueryContext(p.ctx)
					s.Close()
				} else {
					rows, err = db.QueryContext(p.ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if types[0].Name() != "v" || types[0].DatabaseTypeName() != "BYTEA" || types[0].ScanType() != reflect.TypeFor[[]byte]() {
					t.Fatal(types[0])
				}
				length, known := types[0].Length()
				if backend.pg {
					if !known || length != math.MaxInt64 {
						t.Fatal(length, known)
					}
				} else if known {
					t.Fatal(length, known)
				}
				nullable, known := types[0].Nullable()
				if backend.pg {
					if known {
						t.Fatal(nullable, known)
					}
				} else if !nullable || !known {
					t.Fatal(nullable, known)
				}
				if _, _, known := types[0].DecimalSize(); known {
					t.Fatal("decimal size")
				}
				i := 0
				for rows.Next() {
					var v any
					if err := rows.Scan(&v); err != nil {
						t.Fatal(err)
					}
					if empty || i >= len(want) || !reflect.DeepEqual(v, want[i]) {
						t.Fatalf("backend PG=%v row %d got %T %#v want %#v", backend.pg, i, v, v, want)
					}
					i++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				count := len(want)
				if empty {
					count = 0
				}
				if i != count || types[0].DatabaseTypeName() != "BYTEA" {
					t.Fatal(i, count, types[0])
				}
			}
		}
	}
}

func TestReferenceByteaDiagnosticsAndOutput(t *testing.T) {
	p := newReferencePair(t)
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		conn, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(p.ctx, "CREATE TABLE t(v BYTEA)"); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ q, code, message, pos string }{
			{`INSERT INTO t VALUES('\x0')`, "22023", "invalid hexadecimal data: odd number of digits", "22"},
			{`INSERT INTO t VALUES('\xGG')`, "22023", `invalid hexadecimal digit: "G"`, "22"},
			{`INSERT INTO t VALUES('\q')`, "22P02", "invalid input syntax for type bytea", "22"},
			{`/*é*/ INSERT INTO t VALUES('\q')`, "22P02", "invalid input syntax for type bytea", "28"},
			{`INSERT INTO t VALUES('\x0 0')`, "22023", `invalid hexadecimal digit: " "`, "22"},
			{"INSERT INTO t VALUES('\\x\f00')", "22023", "invalid hexadecimal digit: \"\f\"", "22"},
			{"INSERT INTO t VALUES('\\x\v00')", "22023", "invalid hexadecimal digit: \"\v\"", "22"},
			{`INSERT INTO t VALUES('\X41')`, "22P02", "invalid input syntax for type bytea", "22"},
			{`INSERT INTO t VALUES('\400')`, "22P02", "invalid input syntax for type bytea", "22"},
			{`INSERT INTO t VALUES('\08')`, "22P02", "invalid input syntax for type bytea", "22"},
			{`INSERT INTO t VALUES('\00')`, "22P02", "invalid input syntax for type bytea", "22"},
			{`INSERT INTO t VALUES('\')`, "22P02", "invalid input syntax for type bytea", "22"},
			{`CREATE TABLE bad(v BYTEA(1))`, "42601", `type modifier is not allowed for type "bytea"`, "20"},
			{`CREATE TABLE bad(v "bytea"(1))`, "42601", `type modifier is not allowed for type "bytea"`, "20"},
			{`/*é*/ CREATE TABLE bad(v BYTEA(1))`, "42601", `type modifier is not allowed for type "bytea"`, "26"},
		} {
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.pos}
			_, err := conn.ExecContext(p.ctx, tc.q)
			referenceAssertDiagnostic(t, err, &want)
			rows, err := conn.QueryContext(p.ctx, tc.q)
			if rows != nil {
				rows.Close()
			}
			referenceAssertDiagnostic(t, err, &want)
			stmt, err := conn.PrepareContext(p.ctx, tc.q)
			if tc.code == "42601" {
				// CREATE is semantically checked at Execute, not Parse/Prepare.
				if err != nil {
					t.Fatal(err)
				}
				_, err = stmt.ExecContext(p.ctx)
			}
			if stmt != nil {
				stmt.Close()
			}
			referenceAssertDiagnostic(t, err, &want)
		}
		if _, err := conn.ExecContext(p.ctx, "CREATE TABLE nn(v BYTEA,n INTEGER NOT NULL)"); err != nil {
			t.Fatal(err)
		}
		for _, mode := range []struct{ name, detail string }{
			{"hex", `Failing row contains (\x0020275c7e7f80ff, null).`},
			{"escape", "Failing row contains (\\000 '\\\\~\\177\\200\\377, null)."},
		} {
			if _, err := conn.ExecContext(p.ctx, "SET bytea_output="+mode.name); err != nil {
				t.Fatal(err)
			}
			var shown string
			if err := conn.QueryRowContext(p.ctx, "SHOW bytea_output").Scan(&shown); err != nil || shown != mode.name {
				t.Fatal(shown, err)
			}
			_, err := conn.ExecContext(p.ctx, `INSERT INTO nn VALUES('\x0020275c7e7f80ff',NULL)`)
			want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "n" of relation "nn" violates not-null constraint`, Detail: mode.detail, Schema: "public", Table: "nn", Column: "n"}
			referenceAssertDiagnostic(t, err, &want)
		}
		_, err = conn.ExecContext(p.ctx, "SET bytea_output=bogus")
		want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `invalid value for parameter "bytea_output": "bogus"`, Hint: "Available values: escape, hex."}
		referenceAssertDiagnostic(t, err, &want)
		var shown string
		if err := conn.QueryRowContext(p.ctx, "SHOW bytea_output").Scan(&shown); err != nil || shown != "escape" {
			t.Fatal(shown, err)
		}
		if _, err := conn.ExecContext(p.ctx, `INSERT INTO t VALUES('\x41'),('\q')`); err == nil {
			t.Fatal("malformed later tuple")
		}
		rows, err := conn.QueryContext(p.ctx, "SELECT v FROM t")
		if err != nil {
			t.Fatal(err)
		}
		if rows.Next() {
			t.Fatal("published prefix")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		conn.Close()
	}
}
