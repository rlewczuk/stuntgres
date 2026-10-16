package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterConstraintPrepared(t *testing.T) {
	domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{
			"CREATE DOMAIN domain_s.d AS integer",
			"CREATE DOMAIN domain_s.child AS domain_s.d",
			"CREATE TYPE domain_s.pair AS(v domain_s.child)",
			"CREATE TABLE domain_s.t(id integer,v domain_s.child)",
			"CREATE TABLE domain_s.defaults(v domain_s.d DEFAULT (-1)::domain_s.d)",
			"CREATE TABLE domain_s.generated(raw integer,v domain_s.d GENERATED ALWAYS AS(raw::domain_s.d) STORED)",
			"CREATE VIEW domain_s.converted AS SELECT (-1)::domain_s.d AS v FROM public.one",
			"PREPARE domain_alter_cast(domain_s.child) AS SELECT $1 AS v",
			"PREPARE domain_alter_insert(integer,domain_s.child) AS INSERT INTO domain_s.t VALUES($1,$2)",
		} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "DEALLOCATE domain_alter_cast")
		defer c.ExecContext(p.Ctx, "DEALLOCATE domain_alter_insert")
		prepare := func(q string) fixture.HeldStatement {
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
		}
		cast := prepare("SELECT $1::domain_s.child AS v FROM public.one")
		insert := prepare("INSERT INTO domain_s.t VALUES($1,$2)")
		selectHeld := prepare("SELECT id,v FROM domain_s.t ORDER BY id")
		domainID := domainAlterOID(t, p, c, "SELECT 'domain_s.child'::regtype::oid")
		wantRows := [][]any{}
		checkInputs := func(t *testing.T, enforced bool, id int) {
			t.Helper()
			for _, value := range []any{int64(2), nil, int64(-2)} {
				s := fixture.Select(cast.SQL, []string{"v"}, []string{"INT4"}, [][]any{{value}}, value)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, b.PG, true, s.Types) }
				if enforced && value == int64(-2) {
					s = domainCheckFailure(cast.SQL, "child", "positive", true, false, value)
					s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
				}
				t.Run(fmt.Sprint("go_cast_", value), func(t *testing.T) { fixture.RunStep(t, p.Ctx, cast, r, s) })
			}
			if enforced {
				t.Run("inferred_insert", func(t *testing.T) {
					s := domainCheckFailure(insert.SQL, "child", "positive", false, false, id, -2)
					s.Diagnostic.Context = "unnamed portal parameter $2 = '...'"
					fixture.RunStep(t, p.Ctx, insert, r, s)
				})
				t.Run("sql_cast", func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure("EXECUTE domain_alter_cast(-2)", "child", "positive", true, false))
				})
				t.Run("sql_insert", func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure(fmt.Sprintf("EXECUTE domain_alter_insert(%d,-2)", id), "child", "positive", false, false))
				})
			} else {
				fixture.RunStep(t, p.Ctx, insert, r, fixture.Exec(insert.SQL, 1, id, -2))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("EXECUTE domain_alter_cast(-2)", []string{"v"}, []string{"INT4"}, [][]any{{int64(-2)}}))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(fmt.Sprintf("EXECUTE domain_alter_insert(%d,-2)", id+1), 1))
				wantRows = append(wantRows, []any{int64(id), int64(-2)}, []any{int64(id + 1), int64(-2)})
			}
			fixture.RunStep(t, p.Ctx, insert, r, fixture.Exec(insert.SQL, 1, id+2, 2))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("EXECUTE domain_alter_cast(2)", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
			wantRows = append(wantRows, []any{int64(id + 2), int64(2)})
			s := fixture.Select(selectHeld.SQL, []string{"id", "v"}, []string{"INT4", "INT4"}, wantRows)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, b.PG, false, s.Types) }
			fixture.RunStep(t, p.Ctx, selectHeld, r, s)
		}
		t.Run("before_first_add", func(t *testing.T) { checkInputs(t, false, 10) })
		var retained *sql.Rows
		if !b.PG {
			var err error
			retained, err = b.DB.QueryContext(p.Ctx, selectHeld.SQL)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			domainReferenceCheckMetadata(t, retained, false, false, []string{"INT4", "INT4"})
		}
		run(fixture.Exec("ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID", 0))
		t.Run("after_not_valid", func(t *testing.T) { checkInputs(t, true, 20) })
		t.Run("failed_validate", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, r, domainAlterOldFailure("ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive", "t", "v"))
		})
		t.Run("after_failed_validate", func(t *testing.T) { checkInputs(t, true, 30) })
		for _, q := range []string{"INSERT INTO domain_s.defaults DEFAULT VALUES", "INSERT INTO domain_s.generated(raw) VALUES(-1)", "SELECT v FROM domain_s.converted"} {
			t.Run(q, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure(q, "d", "positive", q == "SELECT v FROM domain_s.converted", false))
			})
		}
		run(fixture.Exec("UPDATE domain_s.t SET v=1 WHERE v<0", 2))
		wantRows[0] = []any{int64(10), int64(1)}
		wantRows[1] = []any{int64(11), int64(1)}
		run(fixture.Exec("ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive", 0))
		t.Run("after_success", func(t *testing.T) { checkInputs(t, true, 40) })
		run(fixture.Select("SELECT ('{1,NULL}'::domain_s.child[])[1] AS v,('{1,NULL}'::domain_s.child[])[2] AS n,('(2)'::domain_s.pair).v AS c FROM public.one", []string{"v", "n", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), nil, int64(2)}}))
		if got := domainAlterOID(t, p, c, "SELECT 'domain_s.child'::regtype::oid"); got != domainID {
			t.Fatalf("derived identity %d -> %d", domainID, got)
		}
		run(fixture.Exec("ALTER DOMAIN domain_s.d DROP CONSTRAINT positive", 0))
		t.Run("after_last_drop", func(t *testing.T) { checkInputs(t, false, 50) })
		run(fixture.Exec("INSERT INTO domain_s.defaults DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO domain_s.generated(raw) VALUES(-1)", 1))
		run(fixture.Select("SELECT v FROM domain_s.converted", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}}))
		if retained != nil {
			for _, want := range []struct{ id, v int64 }{{10, -2}, {11, -2}, {12, 2}} {
				if !retained.Next() {
					t.Fatal("retained row missing", retained.Err())
				}
				var id, v int64
				if err := retained.Scan(&id, &v); err != nil {
					t.Fatal(err)
				}
				if id != want.id || v != want.v {
					t.Fatalf("retained %d,%d want %+v", id, v, want)
				}
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained trailing/error", retained.Err())
			}
			retained.Close()
		}
		// Native control and logical-lifetime boundaries supplement the portable
		// repeated use above, rather than substituting fresh Prepare for reuse.
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			s := fixture.ErrorDiagnostic(cast.SQL, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change parameter type"), 2)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, cast, r, s)
			fresh := prepare(cast.SQL)
			run(fixture.Exec("DROP SCHEMA domain_s CASCADE", 0))
			run(fixture.Exec("CREATE SCHEMA domain_s", 0))
			run(fixture.Exec("CREATE DOMAIN domain_s.child AS integer", 0))
			s = fixture.ErrorDiagnostic(fresh.SQL, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change parameter type"), 2)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, fresh, r, s)
		}
	})
}
