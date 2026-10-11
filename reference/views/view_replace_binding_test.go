package reference_test

import (
	"context"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceViewReplaceBinding(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT)", 0), fixture.Exec("INSERT INTO t VALUES(7)", 1), fixture.Exec("CREATE VIEW v AS SELECT id FROM t", 0)} {
		p.Step(t, s)
	}
	for _, tc := range []struct{ query, token, code, message string }{
		{"CREATE OR REPLACE VIEW v AS SELECT /* café */ missing FROM t", "missing", "42703", "column \"missing\" does not exist"},
		{"CREATE OR REPLACE VIEW v AS SELECT /* café */ $1 AS id FROM t", "$1", "42P02", "there is no parameter $1"},
		{"CREATE OR REPLACE VIEW v(x,y) AS SELECT id FROM t", "", "42601", "CREATE VIEW specifies more column names than columns"},
		{"CREATE OR REPLACE VIEW v AS SELECT id,id FROM t", "", "42701", "column \"id\" of relation \"v\" already exists"},
	} {
		position := ""
		if tc.token != "" {
			position = strconv.Itoa(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.token)]) + 1)
		}
		s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: position})
		t.Run(tc.query, func(t *testing.T) {

			t.Run("paired", func(t *testing.T) { p.Step(t, s) })
		})
	}
	p.Step(t, fixture.Select("SELECT id FROM v", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

func TestReferenceViewReplaceCollateKeyword(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(7)", 1))
	// PostgreSQL accepts COLLATE as a bare output label when no collation name
	// follows. Both implicit and explicit labels must work.
	s := fixture.Select("SELECT id collate FROM t", []string{"collate"}, []string{"INT4"}, [][]any{{int64(7)}})
	p.Step(t, s)
	p.Step(t, fixture.Select("SELECT id AS collate FROM t", []string{"collate"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

func TestReferenceViewReplaceOptions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT)", 0), fixture.Exec("CREATE VIEW v WITH(security_barrier=true,security_invoker=true) AS SELECT id FROM t", 0), fixture.Exec("ALTER VIEW v SET(security_invoker=false)", 0), fixture.Exec("ALTER VIEW v RESET(security_barrier)", 0), fixture.Exec("ALTER VIEW v RESET(unknown_option,unknown_option,check_option)", 0), fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT id FROM t", 0)} {
		p.Step(t, s)
	}
	// PostgreSQL permits check_option on this automatically updatable view;
	// TestReferenceViewReplaceCapabilityBoundaries asserts native exclusion.
	if p.Postgres != nil {
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("ALTER VIEW v SET(check_option=local)", 0))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("ALTER VIEW v RESET(check_option)", 0))
	}
}

func TestReferenceViewReplaceReadOnly(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, tc := range []struct{ query, tag string }{{"CREATE OR REPLACE VIEW absent AS SELECT id FROM missing", "CREATE VIEW"}, {"ALTER VIEW absent RENAME TO renamed", "ALTER VIEW"}, {"DROP VIEW absent", "DROP VIEW"}} {
		for _, backend := range p.Backends() {
			func() {
				c, err := backend.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := c.Close(); err != nil {
						t.Error(err)
					}
				}()
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.Exec("BEGIN READ ONLY", 0))
				defer fixture.RunStep(t, context.Background(), c, backend.Recorder, fixture.Exec("ROLLBACK", 0))
				fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + tc.tag + " in a read-only transaction"}))
			}()
		}
	}
}
