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

func TestReferenceViewReplaceCapabilityBoundariesConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW v AS SELECT id FROM t"} {
			fixture.UpdateReturningReferenceExec(t, db, q)
		}
		for _, q := range []string{"CREATE VIEW excluded WITH(check_option=local) AS SELECT id FROM t", "CREATE OR REPLACE VIEW v WITH(check_option=local) AS SELECT id FROM t", "ALTER VIEW v SET(check_option=local)"} {
			r, err := db.Exec(q)
			if err != nil {
				t.Fatal(q, err)
			}
			fixture.DeleteReferenceCount(t, r, err, 0)
		}
		for _, q := range []string{"ALTER VIEW v ALTER COLUMN id SET DEFAULT 7", "ALTER VIEW v ALTER COLUMN id DROP DEFAULT"} {
			r, err := db.Exec(q)
			if backend.PG {
				if err != nil {
					t.Fatal(q, err)
				}
				fixture.DeleteReferenceCount(t, r, err, 0)
			} else {
				fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "0A000", Message: "ALTER VIEW column defaults are unsupported", Position: "14"})
			}
		}
		fixture.UpdateReturningReferenceExec(t, db, "ALTER VIEW v RESET(unknown_option,unknown_option,check_option)")
		rows, err := db.Query("SELECT id FROM v")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(7)}}, Bag: false})
	}
}

func TestReferenceViewReplaceBindingAndAtomicSuffixConsumerRoutes(t *testing.T) {
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
			_, err := db.Exec(tc.query)
			position := strconv.Itoa(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.token)]) + 1)
			fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: position})
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
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(7)}}, Bag: false})
	}
}
