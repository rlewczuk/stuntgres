//go:build !noservertests

package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceClientEncodingNativeUnicode(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if b.PG {
				// A PostgreSQL connection is a byte transport, not a Go-native API.
				// Supply actual literal client bytes; pq is not an implicit converter.
				w := serverAuthenticated(t, fixture.DSN)
				for _, c := range []struct{ encoding, raw string }{{"LATIN1", "\xe9"}, {"SJIS", "\x82\xa0"}, {"SQL_ASCII", "é🙂"}} {
					clientEncodingCommand(t, w, "SET client_encoding='"+c.encoding+"'", "SET")
					frames := w.query(t, "SELECT '"+c.raw+"'::text AS \""+c.raw+"\"")
					clientEncodingRow(t, frames, []string{c.raw}, []uint32{25}, []int16{0}, []byte(c.raw))
					serverWantCompletions(t, frames, "SELECT 1")
				}
				return
			}
			// Intentional native-control phase: this is the public embedded API's
			// Unicode contract, not a temporary backend filter or PG exception.
			c, err := p.Native.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			exec := func(q string) {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			exec(`CREATE TABLE "é🙂"("é🙂" text)`)
			exec(`INSERT INTO "é🙂" VALUES('é🙂')`)
			stmt, err := c.PrepareContext(p.Ctx, `SELECT $1::text AS "é🙂", v FROM (SELECT "é🙂" AS v FROM "é🙂") s`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			var retained string
			for _, encoding := range []string{"LATIN1", "SJIS", "SQL_ASCII"} {
				exec("SET client_encoding='" + encoding + "'")
				var literal string
				if err := c.QueryRowContext(p.Ctx, `SELECT 'é🙂'::text`).Scan(&literal); err != nil || literal != "é🙂" {
					t.Fatalf("%s SQL Unicode %q %v", encoding, literal, err)
				}
				for _, arg := range []any{"é🙂", sql.NullString{String: "é🙂", Valid: true}, []byte("é🙂")} {
					rows, err := stmt.QueryContext(p.Ctx, arg)
					if err != nil {
						t.Fatal(err)
					}
					columns, err := rows.Columns()
					if err != nil {
						rows.Close()
						t.Fatal(err)
					}
					types, err := rows.ColumnTypes()
					if err != nil {
						rows.Close()
						t.Fatal(err)
					}
					if len(columns) != 2 || columns[0] != "é🙂" || columns[1] != "v" || types[0].DatabaseTypeName() != "TEXT" || types[1].DatabaseTypeName() != "TEXT" {
						rows.Close()
						t.Fatalf("metadata %q %+v", columns, types)
					}
					var input, stored string
					if !rows.Next() {
						rows.Close()
						t.Fatal("missing Unicode row")
					}
					if err := rows.Scan(&input, &stored); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					if rows.Next() || rows.Err() != nil {
						rows.Close()
						t.Fatal("Unicode result EOF", rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					if input != "é🙂" || stored != "é🙂" {
						t.Fatalf("%s Unicode %q %q", encoding, input, stored)
					}
					if retained != "" && retained != "é🙂" {
						t.Fatalf("retained Unicode changed %q", retained)
					}
					retained = input
				}
			}
			exec("CREATE TABLE ce_native_copy(v text)")
			exec("SET client_encoding='LATIN1'")
			r, err := stuntgres.CopyFrom(p.Ctx, c, "COPY ce_native_copy FROM STDIN", bytes.NewReader([]byte{0xe9, '\n'}))
			if err != nil || r != (stuntgres.CopyResult{Count: 1, CommandTag: "COPY 1"}) {
				t.Fatalf("native CopyFrom %+v %v", r, err)
			}
			var stored string
			if err := c.QueryRowContext(p.Ctx, "SELECT v FROM ce_native_copy").Scan(&stored); err != nil || stored != "é" {
				t.Fatalf("native storage %q %v", stored, err)
			}
			for _, q := range []struct {
				option string
				want   []byte
			}{{"", []byte{0xe9, '\n'}}, {" (ENCODING 'UTF8')", []byte("é\n")}} {
				var out bytes.Buffer
				r, err := stuntgres.CopyTo(p.Ctx, c, "COPY ce_native_copy TO STDOUT"+q.option, &out)
				if err != nil || r != (stuntgres.CopyResult{Count: 1, CommandTag: "COPY 1"}) || !bytes.Equal(out.Bytes(), q.want) {
					t.Fatalf("native CopyTo %x %+v %v", out.Bytes(), r, err)
				}
			}
		})
	}
}
