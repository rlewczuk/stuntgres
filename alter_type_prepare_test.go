package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestAlterTypeEffectFreePrepareAndCurrentDescriptor(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	utility, err := db.Prepare("ALTER TABLE future ALTER c TYPE BIGINT USING c+1")
	if err != nil {
		t.Fatal("utility Prepare resolved absent target", err)
	}
	defer utility.Close()
	execSQL(t, db, "CREATE TABLE future(c INT DEFAULT 7)")
	execSQL(t, db, "INSERT INTO future DEFAULT VALUES")
	alterTypePublicRows(t, db, "SELECT c FROM future", []string{"c"}, []string{"INT4"}, [][]any{{int64(7)}})
	result, err := utility.Exec()
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	alterTypePublicRows(t, db, "SELECT c FROM future", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}})
	execSQL(t, db, "INSERT INTO future DEFAULT VALUES")
	alterTypePublicRows(t, db, "SELECT c FROM future ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(7)}, {int64(8)}})
	execSQL(t, db, "ALTER TABLE future ALTER c TYPE BIGINT USING c+1")
	alterTypePublicRows(t, db, "SELECT c FROM future ORDER BY c", []string{"c"}, []string{"INT8"}, [][]any{{int64(8)}, {int64(9)}})
	execSQL(t, db, "CREATE TABLE p(c INT)")
	s, err := db.Prepare("ALTER TABLE p ALTER c TYPE INT USING $1")
	if err != nil {
		t.Fatal("effect-free parameter preparation", err)
	}
	defer s.Close()
	_, err = s.Exec()
	notNullDiagnostic(t, err, stuntgres.Error{Code: "42P02", Message: "there is no parameter $1", Position: "38"})
}
