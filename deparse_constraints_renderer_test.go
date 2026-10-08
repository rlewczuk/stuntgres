package stuntgres_test

import (
	"context"
	"errors"
	"fmt"
	"stuntgres"
	"testing"
)

func TestDeparseConstraintsRendererGoldens(t *testing.T) {
	for _, tc := range []struct{ expr, compact, pretty string }{
		{"a>0 AND (b<5 OR flag)", "((a > 0) AND ((b < 5) OR flag))", "a > 0 AND (b < 5 OR flag)"},
		{"NULLIF(a,1)>0", "(NULLIF(a, 1) > 0)", "NULLIF(a, 1) > 0"},
		{"GREATEST(a,1)>0", "(GREATEST(a, 1) > 0)", "GREATEST(a, 1) > 0"},
		{"LEAST(a,3)>0", "(LEAST(a, 3) > 0)", "LEAST(a, 3) > 0"},
		{"COALESCE(a,1)>0", "(COALESCE(a, 1) > 0)", "COALESCE(a, 1) > 0"},
		{"flag IS NOT TRUE", "(flag IS NOT TRUE)", "flag IS NOT TRUE"},
		{"NOT flag", "(NOT flag)", "NOT flag"},
		{"a BETWEEN 1 AND 5", "((a >= 1) AND (a <= 5))", "a >= 1 AND a <= 5"},
		{"a IS DISTINCT FROM 1", "(a IS DISTINCT FROM 1)", "a IS DISTINCT FROM 1"},
		{"a IS NOT NULL", "(a IS NOT NULL)", "a IS NOT NULL"},
		{"CASE WHEN flag THEN a ELSE 2 END>0", "(\nCASE\n    WHEN flag THEN a\n    ELSE 2\nEND > 0)", "\nCASE\n    WHEN flag THEN a\n    ELSE 2\nEND > 0"},
		{"CASE a WHEN 1 THEN true ELSE false END", "\nCASE a\n    WHEN 1 THEN true\n    ELSE false\nEND", "\nCASE a\n    WHEN 1 THEN true\n    ELSE false\nEND"},
		{"abs(a)>0", "(abs(a) > 0)", "abs(a) > 0"},
		{"(a+1)*2>0", "(((a + 1) * 2) > 0)", "((a + 1) * 2) > 0"},
		{"(a & 3)=1", "((a & 3) = 1)", "(a & 3) = 1"},
		{"round(n,1)>0", "(round(n, 1) > (0)::numeric)", "round(n, 1) > 0::numeric"},
		{"random()>0", "(random() > (0)::double precision)", "random() > 0::double precision"},
		{"random_normal() IS NOT NULL", "(random_normal() IS NOT NULL)", "random_normal() IS NOT NULL"},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE render(a INT,b BIGINT,flag BOOL,n NUMERIC,CONSTRAINT c CHECK("+tc.expr+"))")
			for _, prepared := range []bool{false, true} {
				deparseText(t, db, prepared, "SELECT pg_get_expr(conbin,conrelid,false) FROM pg_catalog.pg_constraint WHERE conname='c'", tc.compact)
				deparseText(t, db, prepared, "SELECT pg_get_expr(conbin,conrelid,true) FROM pg_catalog.pg_constraint WHERE conname='c'", tc.pretty)
			}
		})
	}
}

func TestDeparseConstraintsStableAttributesAndSnapshots(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(prefix TEXT,a INT,b INT DEFAULT 7,CONSTRAINT c CHECK(b>0))")
	cid := deparseID(t, db, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='c'")
	did := deparseID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef")
	saved, err := db.Query("SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='c'")
	if err != nil {
		t.Fatal(err)
	}
	defer saved.Close()
	execSQL(t, db, "ALTER TABLE t DROP prefix")
	wantTuples(t, db, "SELECT adnum FROM pg_catalog.pg_attrdef", [][]any{{int64(3)}})
	if deparseID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef") != did || deparseID(t, db, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='c'") != cid {
		t.Fatal("remap changed definition identity")
	}
	execSQL(t, db, "CREATE TABLE supplied(x TEXT,y TEXT,z TEXT)")
	deparseText(t, db, true, "SELECT pg_get_expr(conbin,'supplied'::regclass) FROM pg_catalog.pg_constraint WHERE conname='c'", "(z > 0)")
	execSQL(t, db, "CREATE TABLE wrongshape(q TEXT)")
	_, err = db.Query("SELECT pg_get_expr(conbin,'wrongshape'::regclass) FROM pg_catalog.pg_constraint WHERE conname='c'")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "XX000", Message: `invalid attnum 3 for relation "wrongshape"`})
	execSQL(t, db, "ALTER TABLE t RENAME b TO renamed")
	deparseText(t, db, true, "SELECT pg_get_constraintdef($1,true)", "CHECK (renamed > 0)", cid)
	execSQL(t, db, "DROP TABLE t")
	deparseText(t, db, false, "SELECT pg_get_constraintdef($1)", nil, cid)
	if !saved.Next() {
		t.Fatal("eager snapshot missing", saved.Err())
	}
	var old string
	if err = saved.Scan(&old); err != nil || old != "CHECK (b > 0)" {
		t.Fatal(old, err)
	}
	if saved.Next() || saved.Err() != nil {
		t.Fatal(saved.Err())
	}
}

func TestDeparseConstraintsCancellationAndRollback(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE TABLE t(a INT DEFAULT 7,CONSTRAINT c CHECK(a>0))")
	id := deparseID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef")
	execSQL(t, db, "BEGIN")
	// ALTER DEFAULT is outside the native explicit-transaction boundary.
	_, err := db.Exec("ALTER TABLE t ALTER a SET DEFAULT 3")
	notNullDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
	execSQL(t, db, "ROLLBACK")
	if deparseID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef") != id {
		t.Fatal("rollback lost original identity")
	}
	deparseText(t, db, false, "SELECT pg_get_expr(adbin,0) FROM pg_catalog.pg_attrdef", "7")
	execSQL(t, db, "BEGIN")
	execSQL(t, db, "INSERT INTO t VALUES(1)")
	execSQL(t, db, "ROLLBACK")
	wantTuples(t, db, "SELECT a FROM t", [][]any{})
	execSQL(t, db, "ALTER TABLE t ALTER a SET DEFAULT 3")
	replacement := deparseID(t, db, "SELECT oid FROM pg_catalog.pg_attrdef")
	if replacement == id {
		t.Fatal("supported default replacement retained old identity")
	}
	deparseText(t, db, false, "SELECT pg_get_expr(adbin,0) FROM pg_catalog.pg_attrdef", "3")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := db.QueryContext(ctx, "SELECT pg_get_constraintdef(oid) FROM pg_catalog.pg_constraint")
	if r != nil {
		r.Close()
		t.Fatal("canceled query exposed cursor")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	deparseText(t, db, true, "SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='c'", "CHECK (a > 0)")
}

func TestDeparseConstraintsAssignmentGoldens(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE assigned(a DOUBLE PRECISION DEFAULT 0,b REAL DEFAULT 1,c DOUBLE PRECISION DEFAULT 0.5,n NUMERIC DEFAULT 0.0,f BOOL DEFAULT false)")
	for _, prepared := range []bool{false, true} {
		for _, pretty := range []string{"false", "true"} {
			for i, want := range []string{"0", "1", "0.5", "0.0", "false"} {
				deparseText(t, db, prepared, fmt.Sprintf("SELECT pg_get_expr(adbin,0,%s) FROM pg_catalog.pg_attrdef WHERE adnum=%d", pretty, i+1), want)
			}
		}
	}
}
