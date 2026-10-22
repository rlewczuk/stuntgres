package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceViewReplaceAtomicSuffixAndCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW v AS SELECT id FROM t"} {
			fixture.UpdateReturningReferenceExec(t, db, q)
		}
		for _, tc := range []struct{ query, token, code, message string }{
			{"CREATE OR REPLACE VIEW v AS SELECT /* café */ missing FROM t", "missing", "42703", "column \"missing\" does not exist"},
			{"CREATE OR REPLACE VIEW v AS SELECT /* café */ $1 AS id FROM t", "$1", "42P02", "there is no parameter $1"},
		} {
			result, err := db.Exec(tc.query)
			if result != nil {
				t.Fatal(tc.query, result, err)
			}
			position := strconv.Itoa(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.token)]) + 1)
			fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: position})
		}
		for _, q := range []string{"CREATE OR REPLACE VIEW v AS SELECT id+10 AS id FROM t; SELECT FROM", "ALTER VIEW v RENAME TO renamed; SELECT FROM", "DROP VIEW v; SELECT FROM"} {
			if result, err := db.Exec(q); err == nil || result != nil {
				t.Fatal("invalid suffix returned success or result", q, result, err)
			}
		}
		for _, q := range []string{"CREATE OR REPLACE VIEW v AS SELECT id+10 AS id FROM t", "ALTER VIEW v RENAME TO renamed", "DROP VIEW v"} {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if result, err := db.ExecContext(ctx, q); !errors.Is(err, context.Canceled) || result != nil {
				t.Fatal(q, result, err)
			}
		}
		rows, err := db.Query("SELECT id FROM v")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(7)}}, Bag: false})
	}
}
