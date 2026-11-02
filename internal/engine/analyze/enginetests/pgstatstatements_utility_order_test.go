package enginetests

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
)

func TestPgStatStatementsFingerprintUtilityOptionOrder(t *testing.T) {
	// CreateFunctionStmt/CreateRoleStmt options are selected ordered DefElem
	// lists, not a bag of effective settings. TableLikeClause is deliberately
	// different: its selected options field is the released accumulated bitmask.
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"CREATE FUNCTION f() RETURNS integer LANGUAGE SQL IMMUTABLE AS 'SELECT 1'", "CREATE FUNCTION f() RETURNS integer AS 'SELECT 1' IMMUTABLE LANGUAGE SQL", false},
		{"CREATE FUNCTION f() RETURNS integer LANGUAGE SQL IMMUTABLE AS 'SELECT 1'", "CREATE FUNCTION f() RETURNS integer LANGUAGE SQL /* 雪 */ IMMUTABLE AS 'SELECT 1'", true},
		{"CREATE FUNCTION f() RETURNS integer LANGUAGE SQL STRICT AS 'SELECT 1'", "CREATE FUNCTION f() RETURNS integer LANGUAGE SQL RETURNS NULL ON NULL INPUT AS 'SELECT 1'", true},
		{"CREATE FUNCTION f() RETURNS integer LANGUAGE SQL SECURITY DEFINER AS 'SELECT 1'", "CREATE FUNCTION f() RETURNS integer LANGUAGE SQL EXTERNAL SECURITY DEFINER AS 'SELECT 1'", true},
		{"CREATE PROCEDURE p() LANGUAGE SQL SECURITY INVOKER AS 'SELECT 1'", "CREATE PROCEDURE p() AS 'SELECT 1' SECURITY INVOKER LANGUAGE SQL", false},
		{"CREATE ROLE r LOGIN INHERIT", "CREATE ROLE r INHERIT LOGIN", false},
		{"CREATE ROLE r", "CREATE GROUP r", false},
		{"CREATE ROLE r", "CREATE USER r", false},
		{"CREATE GROUP r", "CREATE USER r", false},
		{"ALTER ROLE r LOGIN INHERIT", "ALTER ROLE r INHERIT LOGIN", false},
		{"CREATE ROLE r LOGIN INHERIT", "CREATE ROLE r /* 雪 */ LOGIN INHERIT", true},
		{"CREATE ROLE r PASSWORD 'value' LOGIN", "CREATE ROLE r ENCRYPTED PASSWORD 'value' LOGIN", true},
		{"CREATE ROLE r IN ROLE other LOGIN", "CREATE ROLE r LOGIN IN ROLE other", false},
		{"CREATE TABLE t(LIKE other INCLUDING DEFAULTS)", "CREATE TABLE t(LIKE other INCLUDING DEFAULTS INCLUDING DEFAULTS)", true},
		{"CREATE TABLE t(LIKE other)", "CREATE TABLE t(LIKE other INCLUDING DEFAULTS EXCLUDING DEFAULTS)", true},
		{"CREATE TABLE t(LIKE other INCLUDING DEFAULTS)", "CREATE TABLE t(LIKE other EXCLUDING DEFAULTS INCLUDING DEFAULTS)", true},
		{"CREATE TABLE t(LIKE other INCLUDING DEFAULTS)", "CREATE TABLE t(LIKE other INCLUDING STORAGE)", false},
	} {
		t.Run(tc.left+" / "+tc.right, func(t *testing.T) {
			left, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.left))
			right, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.right))
			if (left.QueryID() == right.QueryID()) != tc.same {
				t.Fatalf("released option selection: same=%v, want %v", left.QueryID() == right.QueryID(), tc.same)
			}
		})
	}
}
