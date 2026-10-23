package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainConstraintsDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			dcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				dcSeed(run)
				where := "domain_schema='dc_scope'"
				held := dcHold(t, p, c, dcProjection(b.PG, where, nil).Query)
				want := dcSeedRows(database)
				check := func() { dcCheck(t, p, c, r, held, dcProjection(b.PG, where, want)) }
				check()
				dcMissing(func(s fixture.Step) { run(s); check() })
				fail := func(q string, d fixture.Diagnostic) {
					s := fixture.ErrorDiagnostic(q, d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					run(s)
					check()
				}
				fail("ALTER DOMAIN dc_scope.a ADD CONSTRAINT same CHECK(VALUE>0)", fixture.ControlsReferenceDiagnostic("42710", `constraint "same" for domain "a" already exists`))
				fail("ALTER DOMAIN dc_scope.a DROP CONSTRAINT absent", fixture.ControlsReferenceDiagnostic("42704", `constraint "absent" of domain "dc_scope.a" does not exist`))
				run(fixture.Exec("CREATE ROLE dc_reader", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA dc_scope TO dc_reader", 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION dc_reader", 0))
				fail("ALTER DOMAIN dc_scope.a ADD CONSTRAINT denied CHECK(VALUE<100)", fixture.ControlsReferenceDiagnostic("42501", "must be owner of type dc_scope.a"))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("CREATE DOMAIN dc_scope.bad AS integer", 0))
				run(fixture.Exec("CREATE TABLE dc_scope.bad_values(v dc_scope.bad)", 0))
				run(fixture.Exec("INSERT INTO dc_scope.bad_values VALUES(-1)", 1))
				data := func(value int64) {
					s := fixture.Select("SELECT v FROM dc_scope.bad_values", []string{"v"}, []string{"INT4"}, [][]any{{value}})
					run(s)
					run(fixture.Prepared(s))
				}
				d := fixture.ControlsReferenceDiagnostic("23514", `column "v" of table "bad_values" contains values that violate the new constraint`)
				d.Schema, d.Table, d.Column = "dc_scope", "bad_values", "v"
				fail("ALTER DOMAIN dc_scope.bad ADD CONSTRAINT positive CHECK(VALUE>0)", d)
				data(-1)
				run(fixture.Exec("ALTER DOMAIN dc_scope.bad ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", 0))
				want = append(want, dcRow(database, "dc_scope", "positive", "bad"))
				check()
				run(dcFlags("bad", "positive", false))
				fail("ALTER DOMAIN dc_scope.bad VALIDATE CONSTRAINT positive", d)
				data(-1)
				run(dcFlags("bad", "positive", false))
				run(fixture.Exec("UPDATE dc_scope.bad_values SET v=1", 1))
				run(fixture.Exec("ALTER DOMAIN dc_scope.bad VALIDATE CONSTRAINT positive", 0))
				check()
				data(1)
				run(dcFlags("bad", "positive", true))
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				q := "ALTER DOMAIN dc_scope.a ADD CONSTRAINT transactional CHECK(VALUE<100)"
				if b.PG {
					s := fixture.Exec(q, 0)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, tx, r, s)
					scope := append(append([][]any(nil), want...), dcRow(database, "dc_scope", "transactional", "a"))
					fixture.RunStep(t, p.Ctx, tx, r, dcProjection(b.PG, where, scope))
				} else {
					s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction"))
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, tx, r, s)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				check()
			})
		})
	}
}
