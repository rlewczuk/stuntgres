package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestAlterRenameTableStar(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)")
			execSQL(t, db, "INSERT INTO t VALUES(7)")
			tableCoreExec(t, db, prepared, "ALTER TABLE t* RENAME TO newtable", 0)
			wantTuples(t, db, "SELECT * FROM newtable", [][]any{{int64(7)}})
			_, err := db.Query("SELECT * FROM t")
			alterDropDiagnostic(t, err, "42P01", `relation "t" does not exist`, "15")
			tableCoreExec(t, db, prepared, "ALTER TABLE IF EXISTS newtable* RENAME TO t", 0)
			wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(7)}})
		})
	}
}

func TestAlterRenameTableOnly(t *testing.T) {
	for _, target := range []string{"ONLY t", "ONLY(t)"} {
		for _, prepared := range []bool{false, true} {
			t.Run(target+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)")
				execSQL(t, db, "INSERT INTO t VALUES(7)")
				tableCoreExec(t, db, prepared, "ALTER TABLE "+target+" RENAME TO newtable", 0)
				wantTuples(t, db, "SELECT * FROM newtable", [][]any{{int64(7)}})
				_, err := db.Query("SELECT * FROM t")
				alterDropDiagnostic(t, err, "42P01", `relation "t" does not exist`, "15")
			})
		}
	}
}
