package stuntgres_test

import (
	"database/sql"
	"stuntgres"
	"testing"
)

func TestBooleanAggregateSourceACL(t *testing.T) {
	driver := stuntgres.NewDriver()
	admin := openLocal(t, driver, t.Name())
	execSQL(t, admin, "CREATE USER boolean_reader")
	execSQL(t, admin, "CREATE TABLE source(b BOOL)")
	connector, err := driver.OpenConnectorAs(t.Name(), "boolean_reader")
	if err != nil {
		t.Fatal(err)
	}
	reader := sql.OpenDB(connector)
	defer reader.Close()
	execSQL(t, reader, "CREATE TABLE target(b BOOL)")
	for _, q := range []string{"SELECT bool_and(b) FROM source", "SELECT every(b) FROM source WHERE FALSE", "SELECT bool_or(b) FROM source WHERE NULL", "INSERT INTO target SELECT every(b) FROM source WHERE FALSE"} {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				stmt, e := reader.Prepare(q)
				if e != nil {
					t.Fatal("semantic Prepare", e)
				}
				_, err = stmt.Exec()
				stmt.Close()
			} else {
				_, err = reader.Exec(q)
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42501", Message: "permission denied for table source"})
		}
	}
	rows, err := reader.Query("SELECT b FROM target")
	queryCoreRows(t, rows, err, []string{"b"}, []string{"BOOL"}, nil, true)
}
