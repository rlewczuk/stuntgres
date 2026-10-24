package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceTransactionalTypesOwnKeyConflict(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE TYPE tt_round AS(v numeric(4,1))", "CREATE TABLE tt_keys OF tt_round(PRIMARY KEY(v))"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO tt_keys VALUES(1.1)", 1))
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("INSERT INTO tt_keys VALUES(1.2)", 1))
			run(fixture.Exec("SAVEPOINT before_round", 0))
			d := fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "tt_keys_pkey"`)
			d.Detail = "Key (v)=(1) is duplicated."
			d.Schema, d.Table, d.Constraint = "public", "tt_keys", "tt_keys_pkey"
			run(fixture.ErrorDiagnostic("ALTER TYPE tt_round ADD ATTRIBUTE extra boolean CASCADE, ALTER ATTRIBUTE v TYPE numeric(3,0) CASCADE", d))
			run(fixture.Exec("ROLLBACK TO before_round", 0))
			run(fixture.Select("SELECT v FROM tt_keys ORDER BY v", []string{"v"}, []string{"NUMERIC"}, [][]any{{"1.1"}, {"1.2"}}))
			run(fixture.Select("SELECT count(*) FROM pg_attribute WHERE attrelid='tt_keys'::regclass AND attname='extra' AND NOT attisdropped", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Exec("DELETE FROM tt_keys WHERE v=1.2", 1))
			run(fixture.Exec("ALTER TYPE tt_round ALTER ATTRIBUTE v TYPE numeric(3,0) CASCADE", 0))
			run(fixture.Select("SELECT v FROM tt_keys", []string{"v"}, []string{"NUMERIC"}, [][]any{{"1"}}))
			run(fixture.Exec("ROLLBACK", 0))
		})
	}
}

func TestReferenceTransactionalTypesNoticeAndPosition(t *testing.T) {
	domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec(`CREATE DOMAIN domain_s."Δ Domain" AS integer`, 0))
		run(fixture.Exec(`CREATE TABLE domain_s.cascade_holder(v domain_s."Δ Domain",keep integer)`, 0))
		run(fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		q := `ALTER DOMAIN domain_s."Δ Domain" DROP CONSTRAINT IF EXISTS absent CASCADE`
		*notices = nil
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		if len(*notices) != 0 {
			t.Fatal("Prepare emitted notice", *notices)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, fixture.Exec(q, 0))
		want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `constraint "absent" of domain "domain_s.Δ Domain" does not exist, skipping`}
		if len(*notices) != 1 || (*notices)[0] != want {
			t.Fatalf("notices=%+v want=%+v", *notices, want)
		}
		run(fixture.Select(`SELECT count(*) FROM pg_constraint WHERE contypid='domain_s."Δ Domain"'::regtype`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("SAVEPOINT syntax", 0))
		q = `ALTER DOMAIN domain_s."Δ Domain" ADD CHECK(VALUE>0), SET NOT NULL`
		d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near ","`)
		d.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, ",")]) + 1)
		s := fixture.Prepared(fixture.ErrorDiagnostic(q, d))
		s.ErrorStage = "prepare"
		run(s)
		run(fixture.Exec("ROLLBACK TO syntax", 0))
		run(fixture.Select(`SELECT count(*) FROM pg_constraint WHERE contypid='domain_s."Δ Domain"'::regtype`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("INSERT INTO domain_s.cascade_holder VALUES(7,11)", 1))
		q = `DROP DOMAIN domain_s."Δ Domain" CASCADE`
		*notices = nil
		drop, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer drop.Close()
		if len(*notices) != 0 {
			t.Fatal("Prepare emitted CASCADE notice", *notices)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: drop, SQL: q}, r, fixture.Exec(q, 0))
		fixture.DomainReferenceNotice(t, notices, "drop cascades to column v of table domain_s.cascade_holder", "")
		run(fixture.Select("SELECT keep FROM domain_s.cascade_holder", []string{"keep"}, []string{"INT4"}, [][]any{{int64(11)}}))
		run(fixture.Exec("ROLLBACK", 0))
	})
}
