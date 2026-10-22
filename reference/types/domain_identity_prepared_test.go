package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainIdentityPreparedMetadata(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"di_prepared_actor"})
	domainIdentityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		domainIdentityDDL(t, p, c, r, "CREATE SCHEMA di_src", "CREATE SCHEMA di_dst", "CREATE DOMAIN di_src.d AS integer CONSTRAINT positive CHECK(VALUE>0)", "CREATE DOMAIN di_src.unchecked AS integer", "CREATE TABLE di_src.t(id integer,v di_src.d)")
		run(fixture.Exec("INSERT INTO di_src.t VALUES(1,7),(2,NULL)", 2))
		insertQ := "INSERT INTO di_src.t VALUES($1,$2)"
		selectQ := "SELECT id,v FROM di_src.t ORDER BY id"
		insert, err := c.PrepareContext(p.Ctx, insertQ)
		if err != nil {
			t.Fatal(err)
		}
		defer insert.Close()
		selectStmt, err := c.PrepareContext(p.Ctx, selectQ)
		if err != nil {
			t.Fatal(err)
		}
		defer selectStmt.Close()
		read := func(want [][]any) {
			s := fixture.Select(selectQ, []string{"id", "v"}, []string{"INT4", "INT4"}, want)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, b.PG, false, s.Types) }
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: selectStmt, SQL: selectQ}, r, s)
		}
		read([][]any{{int64(1), int64(7)}, {int64(2), nil}})
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: insert, SQL: insertQ}, r, fixture.Exec(insertQ, 1, int64(3), int64(9)))
		read([][]any{{int64(1), int64(7)}, {int64(2), nil}, {int64(3), int64(9)}})
		// Pinned18.6 retains explicitly qualified cast identities until an
		// unrelated search_path change requires reanalysis of their old SQL.
		type cast struct {
			q string
			s *sql.Stmt
		}
		var casts []cast
		for _, name := range []string{"d", "unchecked"} {
			q := "SELECT $1::di_src." + name + " AS v"
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			casts = append(casts, cast{q, s})
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, r, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}, int64(7)))
		}
		renameQ := "ALTER DOMAIN di_src.d RENAME TO renamed"
		rename, err := c.PrepareContext(p.Ctx, renameQ)
		if err != nil {
			t.Fatal(err)
		}
		defer rename.Close()
		oid := domainAlterOID(t, p, c, "SELECT 'di_src.d'::regtype::oid")
		// Prepare has no catalog side effect.
		domainIdentitySameOID(t, p, c, "SELECT 'di_src.d'::regtype::oid", oid)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: rename, SQL: renameQ}, r, fixture.Exec(renameQ, 0))
		run(fixture.Prepared(fixture.Exec("ALTER DOMAIN di_src.renamed SET SCHEMA di_dst", 0)))
		domainIdentityDDL(t, p, c, r, "ALTER DOMAIN di_src.unchecked RENAME TO unchecked_new", "ALTER DOMAIN di_src.unchecked_new SET SCHEMA di_dst")
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: insert, SQL: insertQ}, r, fixture.Exec(insertQ, 1, int64(4), int64(11)))
		read([][]any{{int64(1), int64(7)}, {int64(2), nil}, {int64(3), int64(9)}, {int64(4), int64(11)}})
		for _, predicate := range []string{"id=2", "id=99"} {
			s := fixture.Prepared(fixture.Select("SELECT v FROM di_src.t WHERE "+predicate, []string{"v"}, []string{"INT4"}, [][]any{}))
			if predicate == "id=2" {
				s.Rows = [][]any{{nil}}
			}
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, b.PG, false, s.Types) }
			run(s)
		}
		for _, invalidated := range []bool{false, true} {
			if invalidated {
				domainIdentityDDL(t, p, c, r, "SET search_path TO di_dst,public")
			}
			for _, cast := range casts {
				held := fixture.HeldStatement{Executor: c, Statement: cast.s, SQL: cast.q}
				if invalidated {
					name := "di_src.d"
					if cast.q == "SELECT $1::di_src.unchecked AS v" {
						name = "di_src.unchecked"
					}
					want := fixture.ControlsReferenceDiagnostic("42704", `type "`+name+`" does not exist`)
					want.Position = "12"
					s := fixture.ErrorDiagnostic(cast.q, want, int64(8))
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, held, r, s)
				} else {
					s := fixture.Select(cast.q, []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}, int64(8))
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, b.PG, true, s.Types) }
					fixture.RunStep(t, p.Ctx, held, r, s)
				}
			}
		}
		run(fixture.Error("SELECT 7::di_src.d", "42704"))
		run(fixture.Prepared(fixture.Select("SELECT 7::di_dst.renamed AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})))
		read([][]any{{int64(1), int64(7)}, {int64(2), nil}, {int64(3), int64(9)}, {int64(4), int64(11)}})
		// Effective authority is checked when a held utility executes, not
		// captured from the superuser at Prepare.
		domainIdentityDDL(t, p, c, r, "CREATE ROLE di_prepared_actor", "GRANT USAGE ON SCHEMA di_dst TO di_prepared_actor")
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
				t.Error(err)
			}
		}()
		q := "ALTER DOMAIN di_dst.renamed RENAME TO forbidden"
		utility, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer utility.Close()
		domainIdentityDDL(t, p, c, r, "SET ROLE di_prepared_actor")
		result, err := utility.ExecContext(p.Ctx)
		fixture.State(t, err, "42501")
		if result != nil {
			t.Fatal("denied held utility published result")
		}
		want := fixture.ControlsReferenceDiagnostic("42501", "must be owner of type renamed")
		fixture.AssertDiagnostic(t, err, &want)
		domainIdentityDDL(t, p, c, r, "RESET ROLE")
		domainIdentitySameOID(t, p, c, "SELECT 'di_dst.renamed'::regtype::oid", oid)
	})
}
