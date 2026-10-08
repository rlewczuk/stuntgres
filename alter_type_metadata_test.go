package stuntgres_test

import (
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func TestAlterTypePreparedMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, decl, seed, action, typ string
		length                        int64
		precision, scale              int64
	}{
		{name: "integer", decl: "INT", seed: "7", action: "TYPE BIGINT", typ: "INT8"},
		{name: "typmod", decl: "VARCHAR(5)", seed: "'ab'", action: "TYPE VARCHAR(2)", typ: "VARCHAR", length: 2},
		{name: "collation", decl: "TEXT", seed: "'a'", action: `TYPE TEXT COLLATE pg_catalog."C"`, typ: "TEXT"},
		{name: "numeric", decl: "NUMERIC(8,3)", seed: "1.235", action: "TYPE NUMERIC(5,2)", typ: "NUMERIC", precision: 5, scale: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(id INT,c "+tc.decl+")")
			execSQL(t, db, "INSERT INTO t VALUES(1,"+tc.seed+")")
			conn, err := db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			held, err := conn.PrepareContext(t.Context(), "SELECT c FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			unaffected, err := conn.PrepareContext(t.Context(), "SELECT id FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer unaffected.Close()
			if _, err = conn.ExecContext(t.Context(), "ALTER TABLE t ALTER c "+tc.action); err != nil {
				t.Fatal(err)
			}
			_, err = held.QueryContext(t.Context())
			notNullDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "cached plan must not change result type"})
			rows, err := unaffected.QueryContext(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			rows.Close()
			fresh, err := conn.PrepareContext(t.Context(), "SELECT c FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					rows, err = fresh.QueryContext(t.Context())
				} else {
					rows, err = conn.QueryContext(t.Context(), "SELECT c FROM t")
				}
				if err != nil {
					t.Fatal(err)
				}
				metadata, err := rows.ColumnTypes()
				rows.Close()
				if err != nil {
					t.Fatal(err)
				}
				c := metadata[0]
				if c.DatabaseTypeName() != tc.typ {
					t.Fatal(c)
				}
				if tc.typ == "INT8" && c.ScanType() != reflect.TypeFor[int64]() {
					t.Fatal(c.ScanType())
				}
				if tc.length != 0 {
					if n, ok := c.Length(); !ok || n != tc.length {
						t.Fatal(n, ok)
					}
				}
				if tc.precision != 0 {
					if p, s, ok := c.DecimalSize(); !ok || p != tc.precision || s != tc.scale {
						t.Fatal(p, s, ok)
					}
				}
				if n, ok := c.Nullable(); !ok || !n {
					t.Fatal(n, ok)
				}
			}
		})
	}
}
