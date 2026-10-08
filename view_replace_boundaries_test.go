package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestViewReplaceCapabilityBoundaries(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW v AS SELECT id FROM t"} {
		execSQL(t, db, q)
	}
	for _, q := range []string{"CREATE VIEW excluded WITH(check_option=local) AS SELECT id FROM t", "CREATE OR REPLACE VIEW v WITH(check_option=local) AS SELECT id FROM t", "ALTER VIEW v SET(check_option=local)"} {
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "view check options are unsupported"})
	}
	for _, q := range []string{"ALTER VIEW v ALTER COLUMN id SET DEFAULT 7", "ALTER VIEW v ALTER COLUMN id DROP DEFAULT"} {
		_, err := db.Exec(q)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "ALTER VIEW column defaults are unsupported", Position: "14"})
	}
	execSQL(t, db, "ALTER VIEW v RESET(unknown_option,unknown_option,check_option)")
	rows, err := db.Query("SELECT id FROM v")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
}
