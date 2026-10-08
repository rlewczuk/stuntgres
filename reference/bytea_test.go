//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)


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
