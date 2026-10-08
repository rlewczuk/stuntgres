package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestDeparseConstraintsNeverEvaluatesStoredTree(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE SEQUENCE s START 1")
	execSQL(t, db, "CREATE TABLE t(a INT,CONSTRAINT c CHECK(nextval('s')>0))")
	// Source-derived ruleutils spelling; the reference witness independently
	// checks it. CREATE and both deparsers must leave nextval untouched.
	for _, prepared := range []bool{false, true} {
		deparseText(t, db, prepared, "SELECT pg_get_expr(conbin,conrelid,false) FROM pg_catalog.pg_constraint WHERE conname='c'", "(nextval('s'::regclass) > 0)")
		deparseText(t, db, prepared, "SELECT pg_get_expr(conbin,conrelid,true) FROM pg_catalog.pg_constraint WHERE conname='c'", "nextval('s'::regclass) > 0")
		deparseText(t, db, prepared, "SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='c'", "CHECK (nextval('s'::regclass) > 0)")
	}
	wantTuples(t, db, "SELECT nextval('s')", [][]any{{int64(1)}})
	execSQL(t, db, "ALTER SEQUENCE s RENAME TO renamed_sequence")
	deparseText(t, db, true, "SELECT pg_get_expr(conbin,conrelid,true) FROM pg_catalog.pg_constraint WHERE conname='c'", "nextval('renamed_sequence'::regclass) > 0")
	wantTuples(t, db, "SELECT nextval('renamed_sequence')", [][]any{{int64(2)}})
}
