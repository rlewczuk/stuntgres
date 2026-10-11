package reference_test

import (
	"context"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceMaterializedAlterPrepared(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "alter_prepared")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			for _, suffix := range []string{"RENAME COLUMN i TO key", "RENAME TO saved", "OWNER TO CURRENT_USER", "OWNER TO CURRENT_ROLE", "OWNER TO SESSION_USER", "SET SCHEMA alter_prepared", "DEPENDS ON EXTENSION unaccent", "NO DEPENDS ON EXTENSION unaccent"} {
				t.Run(suffix, func(t *testing.T) {
					q := "ALTER MATERIALIZED VIEW later " + suffix
					held, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal("Describe performed lookup", err)
					}
					defer held.Close()
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42P01", `relation "later" does not exist`)))
					run(fixture.Exec("CREATE MATERIALIZED VIEW later AS SELECT 7 AS i", 1))
					if suffix == "SET SCHEMA alter_prepared" {
						run(fixture.Exec("CREATE SCHEMA alter_prepared", 0))
					}
					if suffix == "DEPENDS ON EXTENSION unaccent" {
						run(fixture.Exec("CREATE EXTENSION unaccent", 0))
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.Exec(q, 0))
					name := "later"
					if suffix == "RENAME TO saved" {
						name = "saved"
					}
					if suffix == "SET SCHEMA alter_prepared" {
						name = "alter_prepared.later"
					}
					run(fixture.Exec("DROP MATERIALIZED VIEW "+name, 0))
				})
			}
		})
	}
}
func TestReferenceMaterializedAlterGrammar(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			alterSeed(run)
			cases := []struct{ q, token string }{
				{"ALTER MATERIALIZED VIEW IF EXISTS alter_mv DEPENDS ON EXTENSION unaccent", "DEPENDS"},
				{"ALTER MATERIALIZED VIEW IF EXISTS alter_mv NO DEPENDS ON EXTENSION unaccent", "DEPENDS"},
				{"ALTER MATERIALIZED VIEW ONLY alter_mv RENAME TO saved", "ONLY"},
				{"ALTER MATERIALIZED VIEW alter_mv RENAME TO saved trailing", "trailing"},
				{"ALTER MATERIALIZED VIEW alter_mv RENAME COLUMN i TO key, RENAME COLUMN s TO value", ","},
				{"ALTER MATERIALIZED VIEW alter_mv RENAME TO $1", "$1"},
				{"PREPARE alter_sql AS ALTER MATERIALIZED VIEW alter_mv RENAME TO saved", "ALTER"},
				{"ALTER MATERIALIZED VIEW alter_mv RENAME TO \"é\" trailing", "trailing"},
			}
			for _, tc := range cases {
				want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "`+tc.token+`"`)
				want.Position = strconv.Itoa(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1)
				alterError(t, p, c, tc.q, want, "prepare")
			}
			run(materializedViewBag("alter_mv"))
			run(fixture.Exec(`CREATE MATERIALIZED VIEW "é" AS SELECT 1 AS depends,2 AS extension,3 AS materialized`, 1))
			run(fixture.Prepared(fixture.Exec(`ALTER MATERIALIZED VIEW "é" RENAME depends TO "新"`, 0)))
			run(fixture.Select(`SELECT "新",extension,materialized FROM "é"`, []string{"新", "extension", "materialized"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(3)}}))
		})
	}
}
func TestReferenceMaterializedAlterMessages(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			alterSeed(run)
			q := "ALTER MATERIALIZED VIEW alter_mv RENAME COLUMN i TO key; ALTER MATERIALIZED VIEW alter_mv RENAME TO saved"
			run(fixture.Exec(q, 0))
			run(alterBag("saved", alterThree))
			malformed := "ALTER MATERIALIZED VIEW saved RENAME TO changed; SELECT FROM"
			wantEOF := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
			wantEOF.Position = "61"
			alterError(t, p, c, malformed, wantEOF, "prepare")
			run(alterBag("saved", alterThree))
			if _, err := c.PrepareContext(p.Ctx, "ALTER MATERIALIZED VIEW saved RENAME TO changed; SELECT 1"); err == nil {
				t.Fatal("prepared message accepted two commands")
			}
			run(alterBag("saved", alterThree))
			run(fixture.Select("ALTER MATERIALIZED VIEW saved OWNER TO CURRENT_USER", []string{}, []string{}, nil))
			run(fixture.Prepared(fixture.Select("ALTER MATERIALIZED VIEW saved OWNER TO SESSION_USER", []string{}, []string{}, nil)))
		})
	}
}
func TestReferenceMaterializedAlterBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, run := fixture.QueryTableCreateSession(t, p, b)
			alterSeed(run)
			run(fixture.Exec("SET default_transaction_read_only=on", 0))
			defer c.ExecContext(context.Background(), "SET default_transaction_read_only=off")
			for _, q := range alterForms("missing_mv") {
				alterError(t, p, c, q, fixture.ControlsReferenceDiagnostic("25006", "cannot execute ALTER MATERIALIZED VIEW in a read-only transaction"), "execute")
			}
			run(fixture.Exec("SET default_transaction_read_only=off", 0))
			tx := fixture.QueryLockBegin(t, p.Ctx, c)
			q := "ALTER MATERIALIZED VIEW alter_mv RENAME COLUMN i TO key"
			if b.PG {
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec(q, 0))
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, alterBag("alter_mv", alterThree))
			} else {
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")))
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(materializedViewBag("alter_mv"))
		})
	}
}
