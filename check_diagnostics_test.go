package stuntgres_test

import (
	"stuntgres"
	"testing"
)

func TestCheckInvalidDefinitions(t *testing.T) {
	for _, tc := range []struct{ q, code, message, position string }{
		{"CREATE TABLE t(a INT CHECK(a))", "42804", "argument of CHECK must be type boolean, not type integer", "28"},
		{"CREATE TABLE t(a TEXT CHECK(a))", "42804", "argument of CHECK must be type boolean, not type text", "29"},
		{"CREATE TABLE t(a INT CHECK(missing=1))", "42703", "column \"missing\" does not exist", "28"},
		{"CREATE TABLE t(a INT CHECK(other.a=1))", "42P01", "missing FROM-clause entry for table \"other\"", "28"},
		{"CREATE TABLE t(a INT CHECK(ctid IS NULL))", "42P10", "system column \"ctid\" reference in check constraint is invalid", "28"},
		{"CREATE TABLE t(a INT CHECK(count(*)>0))", "42803", "aggregate functions are not allowed in check constraints", "28"},
		{"CREATE TABLE t(a INT CHECK($1=1))", "42P02", "there is no parameter $1", "28"},
		{"CREATE TABLE t(a INT CHECK(FALSE AND $1=1))", "42P02", "there is no parameter $1", "38"},
		{"CREATE TABLE t(a INT CONSTRAINT c CHECK(TRUE),CONSTRAINT c CHECK(TRUE))", "42710", "check constraint \"c\" already exists", ""},
		{"CREATE TABLE t(a INT CHECK(TRUE),CONSTRAINT t_check CHECK(TRUE))", "42710", "check constraint \"t_check\" already exists", ""},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(tc.q+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				checkExecError(t, db, prepared, tc.q, stuntgres.Error{Code: tc.code, Message: tc.message, Position: tc.position})
				checkExecError(t, db, false, "SELECT * FROM t", stuntgres.Error{Code: "42P01", Message: `relation "t" does not exist`, Position: "15"})
				tableCoreExec(t, db, false, "CREATE TABLE t(a INT)", 0)
			})
		}
	}
}
func TestCheckNamesAndPrecedence(t *testing.T) {
	for _, tc := range []struct{ ddl, name, detail string }{
		{"CREATE TABLE t(a INT CONSTRAINT z CHECK(FALSE),CONSTRAINT a CHECK(FALSE))", "a", "1"},
		{"CREATE TABLE t(a INT,b INT CHECK(a+a>0))", "t_a_check", "0, 1"},
		{"CREATE TABLE t(a INT,b INT,CHECK(a<b))", "t_check", "2, 1"},
		{"CREATE TABLE t(a INT CONSTRAINT t_a_check CHECK(a>0),CHECK(a>1))", "t_a_check1", "1"},
		{"CREATE TABLE t(a INT CHECK(a>0),CHECK(a>1),CHECK(a>2))", "t_a_check2", "2"},
	} {
		t.Run(tc.ddl, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, tc.ddl)
			checkExecError(t, db, false, "INSERT INTO t VALUES("+tc.detail+")", checkViolation("t", tc.name, tc.detail))
		})
	}
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT NOT NULL,CHECK(FALSE))")
	checkExecError(t, db, false, "INSERT INTO t VALUES(NULL)", stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"})
}
