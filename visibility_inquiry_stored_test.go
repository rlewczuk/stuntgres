package stuntgres_test

import (
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestVisibilityInquiryStoredChecks(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	for kind, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
		name := fmt.Sprintf("visibility_stored_%d", kind)
		constraint := name + "_check"
		execSQL(t, db, "CREATE TABLE "+name+"(id OID,CONSTRAINT "+constraint+" CHECK("+fn+"(id) IS NOT NULL))")
		var valid int64
		switch kind {
		case 0:
			if err := db.QueryRow("SELECT oid FROM pg_catalog.pg_class WHERE relname='" + name + "'").Scan(&valid); err != nil {
				t.Fatal(err)
			}
		case 1:
			valid = 23
		case 2:
			valid = 950
		case 3:
			valid = 4402
		}
		var tree string
		structural, err := db.Query("SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='" + constraint + "'")
		if err != nil {
			t.Fatal(err)
		}
		types, err := structural.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "PG_NODE_TREE" {
			t.Fatal(types, err)
		}
		if !structural.Next() {
			t.Fatal("missing CHECK structural row", structural.Err())
		}
		if err := structural.Scan(&tree); err != nil {
			t.Fatal(err)
		}
		if structural.Next() || structural.Err() != nil {
			t.Fatal("unexpected structural rows", structural.Err())
		}
		structural.Close()
		wantTree := fmt.Sprintf("{type 16 typmod -1 collation 0 derivation 0 node NullTest not true {type 16 typmod -1 collation 0 derivation 0 node VisibilityInquiry %d {type 26 typmod -1 collation 0 derivation 0 node Var attnum 1}}}", kind)
		if tree != wantTree {
			t.Fatalf("structural output %q want %q", tree, wantTree)
		}
		for _, prepared := range []bool{false, true} {
			deparseText(t, db, prepared, "SELECT pg_get_expr(conbin,conrelid) FROM pg_catalog.pg_constraint WHERE conname='"+constraint+"'", "("+fn+"(id) IS NOT NULL)")
			deparseText(t, db, prepared, "SELECT pg_get_constraintdef(oid) FROM pg_catalog.pg_constraint WHERE conname='"+constraint+"'", "CHECK (("+fn+"(id) IS NOT NULL))")
		}
		for _, pretty := range []bool{false, true} {
			expression := fn + "(id) IS NOT NULL"
			if !pretty {
				expression = "(" + expression + ")"
			}
			definition := "CHECK (" + expression + ")"
			for _, prepared := range []bool{false, true} {
				deparseText(t, db, prepared, fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), expression)
				deparseText(t, db, prepared, fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), definition)
			}
			var actual string
			if err := db.QueryRow(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint)).Scan(&actual); err != nil {
				t.Fatal(err)
			}
			copyName := fmt.Sprintf("%s_copy_%t", name, pretty)
			execSQL(t, db, "CREATE TABLE "+copyName+"(id OID,CONSTRAINT copied "+actual+")")
			execSQL(t, db, fmt.Sprintf("INSERT INTO %s VALUES(%d)", copyName, valid))
			_, definitionFailure := db.Exec("INSERT INTO " + copyName + " VALUES(0)")
			notNullDiagnostic(t, definitionFailure, stuntgres.Error{Code: "23514", Message: fmt.Sprintf(`new row for relation "%s" violates check constraint "copied"`, copyName), Detail: "Failing row contains (0).", Schema: "public", Table: copyName, Constraint: "copied"})
			_, definitionNull := db.Exec("INSERT INTO " + copyName + " VALUES(NULL)")
			notNullDiagnostic(t, definitionNull, stuntgres.Error{Code: "23514", Message: fmt.Sprintf(`new row for relation "%s" violates check constraint "copied"`, copyName), Detail: "Failing row contains (null).", Schema: "public", Table: copyName, Constraint: "copied"})
			wantTuples(t, db, "SELECT id::bigint FROM "+copyName, [][]any{{valid}})
			if err := db.QueryRow(fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint)).Scan(&actual); err != nil {
				t.Fatal(err)
			}
			expressionCopy := fmt.Sprintf("%s_expr_%t", name, pretty)
			execSQL(t, db, "CREATE TABLE "+expressionCopy+"(id OID,CONSTRAINT copied CHECK ("+actual+"))")
			execSQL(t, db, fmt.Sprintf("INSERT INTO %s VALUES(%d)", expressionCopy, valid))
			_, failure := db.Exec("INSERT INTO " + expressionCopy + " VALUES(0)")
			notNullDiagnostic(t, failure, stuntgres.Error{Code: "23514", Message: fmt.Sprintf(`new row for relation "%s" violates check constraint "copied"`, expressionCopy), Detail: "Failing row contains (0).", Schema: "public", Table: expressionCopy, Constraint: "copied"})
			_, nullFailure := db.Exec("INSERT INTO " + expressionCopy + " VALUES(NULL)")
			notNullDiagnostic(t, nullFailure, stuntgres.Error{Code: "23514", Message: fmt.Sprintf(`new row for relation "%s" violates check constraint "copied"`, expressionCopy), Detail: "Failing row contains (null).", Schema: "public", Table: expressionCopy, Constraint: "copied"})
			wantTuples(t, db, "SELECT id::bigint FROM "+expressionCopy, [][]any{{valid}})
		}
		if kind == 0 {
			execSQL(t, db, "SET search_path=pg_catalog")
			wantTuples(t, db, fmt.Sprintf("SELECT pg_table_is_visible(%d::oid)", valid), [][]any{{false}})
			execSQL(t, db, fmt.Sprintf("INSERT INTO public.%s VALUES(%d)", name, valid))
			execSQL(t, db, "SET search_path=public,pg_catalog")
		}
	}
}

func TestVisibilityInquiryAlterCheckCapture(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	for kind, fn := range []string{"pg_table_is_visible", "pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"} {
		for _, prepared := range []bool{false, true} {
			name := fmt.Sprintf("visibility_alter_%d_%t", kind, prepared)
			constraint := name + "_check"
			execSQL(t, db, "CREATE TABLE "+name+"(id OID,CONSTRAINT "+constraint+" CHECK("+fn+"(id) IS NOT NULL))")
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
			execSQL(t, db, fmt.Sprintf("INSERT INTO %s VALUES(%d),(%d)", name, valid, valid))
			alter := func(q string) error {
				if !prepared {
					_, err := db.Exec(q)
					return err
				}
				stmt, err := db.Prepare(q)
				if err != nil {
					return err
				}
				defer stmt.Close()
				_, err = stmt.Exec()
				return err
			}
			if err := alter("ALTER TABLE " + name + " ALTER COLUMN id TYPE BIGINT USING id::bigint"); err != nil {
				t.Fatal(err)
			}
			baseline := [][]any{{valid}, {valid}}
			wantTuples(t, db, "SELECT id FROM "+name+" ORDER BY id", baseline)
			var before string
			if err := db.QueryRow("SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='" + constraint + "'").Scan(&before); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(before, fmt.Sprintf("VisibilityInquiry %d", kind)) {
				t.Fatal(before)
			}
			err := alter("ALTER TABLE " + name + " ALTER COLUMN id TYPE INTEGER USING 0")
			notNullDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: fmt.Sprintf(`check constraint "%s" of relation "%s" is violated by some row`, constraint, name), Schema: "public", Table: name, Constraint: constraint})
			wantTuples(t, db, "SELECT id FROM "+name+" ORDER BY id", baseline)
			rows, err := db.Query("SELECT id FROM " + name + " ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT8" {
				t.Fatal(types, err)
			}
			rows.Close()
			var after string
			if err := db.QueryRow("SELECT conbin FROM pg_catalog.pg_constraint WHERE conname='" + constraint + "'").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatal("failed ALTER changed CHECK", before, after)
			}
			for _, pretty := range []bool{false, true} {
				expression := fn + "(id::oid) IS NOT NULL"
				if !pretty {
					expression = "(" + fn + "((id)::oid) IS NOT NULL)"
				}
				for _, held := range []bool{false, true} {
					deparseText(t, db, held, fmt.Sprintf("SELECT pg_get_expr(conbin,conrelid,%t) FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), expression)
					deparseText(t, db, held, fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='%s'", pretty, constraint), "CHECK ("+expression+")")
				}
			}
		}
	}
}
