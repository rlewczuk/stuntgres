package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDomainDependencies(t *testing.T) {
	t.Run("restrict_and_exact_cascade", func(t *testing.T) {
		domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			run(fixture.Exec("CREATE DOMAIN domain_s.di AS integer", 0))
			run(fixture.Exec("CREATE TABLE domain_s.t (id integer,i domain_s.di,keep integer)", 0))
			run(fixture.Exec("INSERT INTO domain_s.t VALUES (1,7,11),(2,NULL,12)", 2))
			want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type domain_s.di because other objects depend on it")
			want.Detail = "column i of table domain_s.t depends on type domain_s.di"
			want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("DROP DOMAIN domain_s.di", want)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
			}
			run(fixture.Select("SELECT id,i,keep FROM domain_s.t ORDER BY id", []string{"id", "i", "keep"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(11)}, {int64(2), nil, int64(12)}}))
			run(fixture.Exec("CREATE TABLE domain_s.deps (keep integer,d domain_s.di)", 0))
			run(fixture.Exec("INSERT INTO domain_s.deps VALUES (1,7),(2,NULL)", 2))
			run(fixture.Exec("CREATE VIEW domain_s.depview AS SELECT d FROM domain_s.deps", 0))
			want.Detail += "\ncolumn d of table domain_s.deps depends on type domain_s.di\nview domain_s.depview depends on column d of table domain_s.deps"
			run(fixture.ErrorDiagnostic("DROP DOMAIN domain_s.di RESTRICT", want))
			*notices = nil
			run(fixture.Exec("DROP DOMAIN domain_s.di CASCADE", 0))
			fixture.DomainReferenceNotice(t, notices, "drop cascades to 3 other objects", "drop cascades to column i of table domain_s.t\ndrop cascades to column d of table domain_s.deps\ndrop cascades to view domain_s.depview")
			run(fixture.Select("SELECT id,keep FROM domain_s.t ORDER BY id", []string{"id", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(12)}}))
			run(fixture.Select("SELECT keep FROM domain_s.deps ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			want = fixture.ControlsReferenceDiagnostic("42703", `column "d" does not exist`)
			want.Position = "8"
			s := fixture.ErrorDiagnostic("SELECT d FROM domain_s.deps", want)
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			want = fixture.ControlsReferenceDiagnostic("42P01", `relation "domain_s.depview" does not exist`)
			want.Position = "15"
			s = fixture.ErrorDiagnostic("SELECT * FROM domain_s.depview", want)
			s.SelectRows = true
			s.Prepared = true
			s.ErrorStage = "prepare"
			run(s)
			run(fixture.ErrorDiagnostic("DROP DOMAIN domain_s.di", fixture.ControlsReferenceDiagnostic("42704", `type "domain_s.di" does not exist`)))
			run(fixture.Exec("DROP DOMAIN IF EXISTS domain_s.di", 0))
			fixture.DomainReferenceNotice(t, notices, `type "domain_s.di" does not exist, skipping`, "")
			run(fixture.Exec("CREATE DOMAIN domain_s.di integer", 0))
			run(fixture.Exec("CREATE TABLE domain_s.recreated (v domain_s.di)", 0))
			run(fixture.Exec("DROP TABLE domain_s.recreated", 0))
			run(fixture.Exec("DROP DOMAIN domain_s.di", 0))
		})
	})
	t.Run("chain_columns_and_expression_closure", func(t *testing.T) {
		domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			for _, q := range []string{
				"CREATE DOMAIN domain_s.di AS integer", "CREATE DOMAIN domain_s.chain AS domain_s.di",
				"CREATE TABLE domain_s.chain_table (keep integer,d1 domain_s.chain,d2 domain_s.di,calc integer DEFAULT 9::domain_s.di,CHECK (keep>0::domain_s.di))",
				"CREATE VIEW domain_s.cast_view AS SELECT keep::domain_s.di AS v FROM domain_s.chain_table",
				"CREATE TABLE domain_s.only_domain (v domain_s.chain)",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO domain_s.chain_table (keep,d1,d2) VALUES (1,7,8),(2,NULL,NULL)", 2))
			run(fixture.Exec("INSERT INTO domain_s.only_domain VALUES (7),(NULL)", 2))
			want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type domain_s.di because other objects depend on it")
			want.Detail = "type domain_s.chain depends on type domain_s.di\ncolumn d1 of table domain_s.chain_table depends on type domain_s.chain\ncolumn v of table domain_s.only_domain depends on type domain_s.chain\ncolumn d2 of table domain_s.chain_table depends on type domain_s.di\ndefault value for column calc of table domain_s.chain_table depends on type domain_s.di\nconstraint chain_table_keep_check on table domain_s.chain_table depends on type domain_s.di\nview domain_s.cast_view depends on type domain_s.di"
			want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			run(fixture.ErrorDiagnostic("DROP DOMAIN domain_s.di", want))
			*notices = nil
			run(fixture.Exec("DROP DOMAIN domain_s.di CASCADE", 0))
			fixture.DomainReferenceNotice(t, notices, "drop cascades to 7 other objects", "drop cascades to type domain_s.chain\ndrop cascades to column d1 of table domain_s.chain_table\ndrop cascades to column v of table domain_s.only_domain\ndrop cascades to column d2 of table domain_s.chain_table\ndrop cascades to default value for column calc of table domain_s.chain_table\ndrop cascades to constraint chain_table_keep_check on table domain_s.chain_table\ndrop cascades to view domain_s.cast_view")
			run(fixture.Select("SELECT keep,calc FROM domain_s.chain_table ORDER BY keep", []string{"keep", "calc"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {int64(2), int64(9)}}))
			// Removed domain-dependent CHECK and DEFAULT, not the surviving columns.
			run(fixture.Exec("INSERT INTO domain_s.chain_table (keep) VALUES (-1)", 1))
			run(fixture.Select("SELECT keep,calc FROM domain_s.chain_table WHERE keep=-1", []string{"keep", "calc"}, []string{"INT4", "INT4"}, [][]any{{int64(-1), nil}}))
			run(fixture.Select("SELECT count(*) AS n FROM domain_s.only_domain", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
			want = fixture.ControlsReferenceDiagnostic("42P01", `relation "domain_s.cast_view" does not exist`)
			want.Position = "15"
			s := fixture.ErrorDiagnostic("SELECT * FROM domain_s.cast_view", want)
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			want = fixture.ControlsReferenceDiagnostic("42704", `type "domain_s.chain" does not exist`)
			want.Position = "32"
			run(fixture.ErrorDiagnostic("CREATE TABLE domain_s.probe (v domain_s.chain)", want))
		})
	})
	t.Run("schema_and_search_path", func(t *testing.T) {
		domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			for _, q := range []string{"CREATE SCHEMA domain_other", "CREATE SCHEMA domain_external", "CREATE DOMAIN domain_s.di AS integer", "CREATE DOMAIN domain_other.di AS text"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("SET search_path TO domain_s,domain_other,public", 0))
			run(fixture.Select("SELECT '7'::di AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Exec("SET search_path TO domain_other,domain_s,public", 0))
			run(fixture.Prepared(fixture.Select("SELECT 'word'::di AS v FROM public.one", []string{"v"}, []string{"TEXT"}, [][]any{{"word"}})))
			run(fixture.Exec("CREATE TABLE domain_external.survivor (keep integer,d domain_s.di)", 0))
			run(fixture.Exec("INSERT INTO domain_external.survivor VALUES (1,7),(2,NULL)", 2))
			want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop schema domain_s because other objects depend on it")
			want.Detail = "type domain_s.di depends on schema domain_s\ncolumn d of table domain_external.survivor depends on type domain_s.di"
			want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			run(fixture.ErrorDiagnostic("DROP SCHEMA domain_s RESTRICT", want))
			*notices = nil
			run(fixture.Exec("DROP SCHEMA domain_s CASCADE", 0))
			fixture.DomainReferenceNotice(t, notices, "drop cascades to 2 other objects", "drop cascades to type domain_s.di\ndrop cascades to column d of table domain_external.survivor")
			run(fixture.Select("SELECT keep FROM domain_external.survivor ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(fixture.Select("SELECT 'still here'::domain_other.di AS v FROM public.one", []string{"v"}, []string{"TEXT"}, [][]any{{"still here"}}))
		})
	})
	t.Run("ownership", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"domain_creator", "domain_reader"})
		domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, recorder *fixture.Recorder, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			t.Cleanup(func() {
				if _, err := backend.DB.ExecContext(p.Ctx, "DROP DOMAIN IF EXISTS public.domain_owned CASCADE"); err != nil {
					t.Error(err)
				}
			})
			for _, q := range []string{"CREATE ROLE domain_creator", "CREATE ROLE domain_reader", "SET ROLE domain_reader"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.ErrorDiagnostic("CREATE DOMAIN domain_s.denied AS integer", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema domain_s")))
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET ROLE domain_creator", 0))
			fixture.WithPublicSchemaOwner(t, p, backend, "domain_creator", func() { run(fixture.Exec("CREATE DOMAIN public.domain_owned AS integer", 0)) })
			run(fixture.Exec("RESET ROLE", 0))
			run(fixture.Exec("SET ROLE domain_reader", 0))
			run(fixture.ErrorDiagnostic("DROP DOMAIN public.domain_owned", fixture.ControlsReferenceDiagnostic("42501", "must be owner of type domain_owned")))
			run(fixture.Exec("RESET ROLE", 0))
			if backend.PG {
				// External ACL-management control. Native type ACL syntax is excluded;
				// internal authorization units exercise its catalog authority instead.
				run(fixture.Exec("REVOKE USAGE ON DOMAIN public.domain_owned FROM PUBLIC", 0))
				run(fixture.Exec("SET ROLE domain_reader", 0))
				s := fixture.ErrorDiagnostic("CREATE DOMAIN public.domain_denied_base AS public.domain_owned", fixture.ControlsReferenceDiagnostic("42501", "permission denied for type domain_owned"))
				s.ErrorStage = "execute"
				fixture.WithPublicSchemaOwner(t, p, backend, "domain_reader", func() { run(s) })
				run(fixture.Exec("RESET ROLE", 0))
			}
			run(fixture.Exec("SET ROLE domain_creator", 0))
			run(fixture.Exec("DROP DOMAIN public.domain_owned", 0))
			run(fixture.Exec("RESET ROLE", 0))
		})
	})
}
