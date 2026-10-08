package stuntgres_test

import (
	"fmt"
	"stuntgres"
	"testing"
)

func TestAlterTypePreparedDML(t *testing.T) {
	for _, narrow := range []bool{false, true} {
		t.Run(fmt.Sprint(narrow), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			before, after, typ := "INT", "BIGINT", "INT8"
			if narrow {
				before, after, typ = "BIGINT", "INT", "INT4"
			}
			execSQL(t, db, "CREATE TABLE t(c "+before+")")
			conn, err := db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			old, err := conn.PrepareContext(t.Context(), "INSERT INTO t VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			explicit, err := conn.PrepareContext(t.Context(), "INSERT INTO t VALUES($1::"+before+")")
			if err != nil {
				t.Fatal(err)
			}
			defer explicit.Close()
			update, err := conn.PrepareContext(t.Context(), "UPDATE t SET c=$1")
			if err != nil {
				t.Fatal(err)
			}
			defer update.Close()
			if _, err = conn.ExecContext(t.Context(), "ALTER TABLE t ALTER c TYPE "+after); err != nil {
				t.Fatal(err)
			}
			if _, err = old.ExecContext(t.Context(), int64(7)); err != nil {
				t.Fatal("retained source parameter", err)
			}
			if _, err = explicit.ExecContext(t.Context(), int64(7)); err != nil {
				t.Fatal("retained explicit source", err)
			}
			if _, err = update.ExecContext(t.Context(), int64(8)); err != nil {
				t.Fatal("retained UPDATE source parameter", err)
			}
			if narrow {
				_, err = old.ExecContext(t.Context(), int64(2147483648))
				notNullDiagnostic(t, err, stuntgres.Error{Code: "22003", Message: "integer out of range"})
			}
			fresh, err := conn.PrepareContext(t.Context(), "INSERT INTO t VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			if _, err = fresh.ExecContext(t.Context(), int64(9)); err != nil {
				t.Fatal(err)
			}
			// Query on the same retained physical session after fresh assignment.
			rows, err := conn.QueryContext(t.Context(), "SELECT c FROM t ORDER BY c")
			if err != nil {
				t.Fatal(err)
			}
			rows.Close()
			alterTypePublicRows(t, db, "SELECT c FROM t ORDER BY c", []string{"c"}, []string{typ}, [][]any{{int64(8)}, {int64(8)}, {int64(9)}})
		})
	}
}
