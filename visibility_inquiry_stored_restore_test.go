package stuntgres_test

import (
	"bytes"
	"context"
	"fmt"
	"stuntgres"
	"testing"
)

func TestVisibilityInquiryStoredCheckRestore(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	functions := []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"}
	for kind, fn := range functions {
		name := fmt.Sprintf("visibility_restore_%d", kind)
		execSQL(t, db, "CREATE TABLE "+name+"(id OID,CONSTRAINT "+name+"_check CHECK("+fn+"(id) IS NOT NULL))")
	}
	// Empty relation inquiries carry no stale OID row literals across replacement.
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	for kind, fn := range functions {
		name := fmt.Sprintf("visibility_restore_%d", kind)
		constraint := name + "_check"
		for _, pretty := range []bool{false, true} {
			expr := fn + "(id) IS NOT NULL"
			if !pretty {
				expr = "(" + expr + ")"
			}
			deparseText(t, db, false, fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), expr)
			deparseText(t, db, true, fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), "CHECK ("+expr+")")
		}
		valid := int64(23)
		switch kind {
		case 0:
			if err := db.QueryRow("SELECT oid FROM pg_catalog.pg_class WHERE relname='" + name + "'").Scan(&valid); err != nil {
				t.Fatal(err)
			}
		case 2:
			valid = 950
		case 3:
			valid = 4402
		}
		execSQL(t, db, fmt.Sprintf("INSERT INTO %s VALUES(%d)", name, valid))
		_, err := db.Exec("INSERT INTO " + name + " VALUES(0)")
		notNullDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: fmt.Sprintf(`new row for relation "%s" violates check constraint "%s"`, name, constraint), Detail: "Failing row contains (0).", Schema: "public", Table: name, Constraint: constraint})
		wantTuples(t, db, "SELECT id::bigint FROM "+name, [][]any{{valid}})
	}
}
