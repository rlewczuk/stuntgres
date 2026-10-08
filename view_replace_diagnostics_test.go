package stuntgres_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestViewReplaceDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		setup, query string
		want         stuntgres.Error
	}{
		{"", "CREATE OR REPLACE VIEW v AS SELECT id FROM t", stuntgres.Error{Code: "42P16", Message: "cannot drop columns from view"}},
		{"", "CREATE OR REPLACE VIEW v AS SELECT id AS renamed,txt FROM t", stuntgres.Error{Code: "42P16", Message: "cannot change name of view column \"id\" to \"renamed\"", Hint: "Use ALTER VIEW ... RENAME COLUMN ... to change name of view column instead."}},
		{"", "CREATE OR REPLACE VIEW v AS SELECT id::BIGINT AS id,txt FROM t", stuntgres.Error{Code: "42P16", Message: "cannot change data type of view column \"id\" from integer to bigint"}},
		{"CREATE VIEW cv AS SELECT txt::VARCHAR(3) AS x FROM t", "CREATE OR REPLACE VIEW cv AS SELECT txt::VARCHAR(4) AS x FROM t", stuntgres.Error{Code: "42P16", Message: "cannot change data type of view column \"x\" from character varying(3) to character varying(4)"}},
		{"CREATE VIEW cv AS SELECT id::NUMERIC(10,1) AS x FROM t", "CREATE OR REPLACE VIEW cv AS SELECT id::NUMERIC(10,2) AS x FROM t", stuntgres.Error{Code: "42P16", Message: "cannot change data type of view column \"x\" from numeric(10,1) to numeric(10,2)"}},
		{"CREATE VIEW cv AS SELECT txt COLLATE \"C\" AS x FROM t", "CREATE OR REPLACE VIEW cv AS SELECT txt COLLATE \"POSIX\" AS x FROM t", stuntgres.Error{Code: "42P16", Message: "cannot change collation of view column \"x\" from \"C\" to \"POSIX\""}},
		{"", "DROP VIEW t", stuntgres.Error{Code: "42809", Message: "\"t\" is not a view", Hint: "Use DROP TABLE to remove a table."}},
		{"", "DROP TABLE v", stuntgres.Error{Code: "42809", Message: "\"v\" is not a table", Hint: "Use DROP VIEW to remove a view."}},
		{"", "ALTER VIEW v SET(unknown_option=true)", stuntgres.Error{Code: "22023", Message: "unrecognized parameter \"unknown_option\""}},
		{"", "ALTER VIEW v SET(security_invoker=wat)", stuntgres.Error{Code: "22023", Message: "invalid value for boolean option \"security_invoker\": wat"}},
		{"", "ALTER VIEW v SET(security_invoker=true,security_invoker=false)", stuntgres.Error{Code: "22023", Message: "parameter \"security_invoker\" specified more than once"}},
		{"", "ALTER VIEW v RESET(security_invoker=true)", stuntgres.Error{Code: "42601", Message: "RESET must not include values for parameters"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			for _, q := range []string{"CREATE TABLE t(id INT,txt TEXT)", "INSERT INTO t VALUES(1,'a')", "CREATE VIEW v AS SELECT id,txt FROM t"} {
				execSQL(t, db, q)
			}
			if tc.setup != "" {
				execSQL(t, db, tc.setup)
			}
			_, err := db.Exec(tc.query)
			typeCoreDiagnostic(t, err, tc.want)
			rows, err := db.Query("SELECT * FROM v")
			queryCoreRows(t, rows, err, []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}}, false)
		})
	}
}

func TestViewReplaceBindingAndAtomicSuffix(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW v AS SELECT id FROM t"} {
		execSQL(t, db, q)
	}
	for _, tc := range []struct{ query, token, code, message string }{
		{"CREATE OR REPLACE VIEW v AS SELECT /* café */ missing FROM t", "missing", "42703", "column \"missing\" does not exist"},
		{"CREATE OR REPLACE VIEW v AS SELECT /* café */ $1 AS id FROM t", "$1", "42P02", "there is no parameter $1"},
	} {
		_, err := db.Exec(tc.query)
		position := strconv.Itoa(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.token)]) + 1)
		typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Position: position})
	}
	for _, q := range []string{"CREATE OR REPLACE VIEW v AS SELECT id+10 AS id FROM t; SELECT FROM", "ALTER VIEW v RENAME TO renamed; SELECT FROM", "DROP VIEW v; SELECT FROM"} {
		if _, err := db.Exec(q); err == nil {
			t.Fatal("invalid suffix accepted", q)
		}
	}
	for _, q := range []string{"CREATE OR REPLACE VIEW v AS SELECT id+10 AS id FROM t", "ALTER VIEW v RENAME TO renamed", "DROP VIEW v"} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := db.ExecContext(ctx, q); !errors.Is(err, context.Canceled) {
			t.Fatal(q, err)
		}
	}
	rows, err := db.Query("SELECT id FROM v")
	queryCoreRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
}
