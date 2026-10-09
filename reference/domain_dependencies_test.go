package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDomainDependencies(t *testing.T) {
	t.Run("restrict_and_exact_cascade", func(t *testing.T) {
		domainReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
			run(referenceExec("CREATE DOMAIN domain_s.di AS integer", 0))
			run(referenceExec("CREATE TABLE domain_s.t (id integer,i domain_s.di,keep integer)", 0))
			run(referenceExec("INSERT INTO domain_s.t VALUES (1,7,11),(2,NULL,12)", 2))
			want := controlsReferenceDiagnostic("2BP01", "cannot drop type domain_s.di because other objects depend on it")
			want.Detail = "column i of table domain_s.t depends on type domain_s.di"
			want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic("DROP DOMAIN domain_s.di", want)
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
			}
			run(referenceSelect("SELECT id,i,keep FROM domain_s.t ORDER BY id", []string{"id", "i", "keep"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(7), int64(11)}, {int64(2), nil, int64(12)}}))
			run(referenceExec("CREATE TABLE domain_s.deps (keep integer,d domain_s.di)", 0))
			run(referenceExec("INSERT INTO domain_s.deps VALUES (1,7),(2,NULL)", 2))
			run(referenceExec("CREATE VIEW domain_s.depview AS SELECT d FROM domain_s.deps", 0))
			want.Detail += "\ncolumn d of table domain_s.deps depends on type domain_s.di\nview domain_s.depview depends on column d of table domain_s.deps"
			run(referenceErrorDiagnostic("DROP DOMAIN domain_s.di RESTRICT", want))
			*notices = nil
			run(referenceExec("DROP DOMAIN domain_s.di CASCADE", 0))
			domainReferenceNotice(t, notices, "drop cascades to 3 other objects", "drop cascades to column i of table domain_s.t\ndrop cascades to column d of table domain_s.deps\ndrop cascades to view domain_s.depview")
			run(referenceSelect("SELECT id,keep FROM domain_s.t ORDER BY id", []string{"id", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), int64(12)}}))
			run(referenceSelect("SELECT keep FROM domain_s.deps ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			want = controlsReferenceDiagnostic("42703", `column "d" does not exist`)
			want.Position = "8"
			s := referenceErrorDiagnostic("SELECT d FROM domain_s.deps", want)
			s.selectRows = true
			s.errorStage = "execute"
			run(s)
			want = controlsReferenceDiagnostic("42P01", `relation "domain_s.depview" does not exist`)
			want.Position = "15"
			s = referenceErrorDiagnostic("SELECT * FROM domain_s.depview", want)
			s.selectRows = true
			s.prepared = true
			s.errorStage = "prepare"
			run(s)
			run(referenceErrorDiagnostic("DROP DOMAIN domain_s.di", controlsReferenceDiagnostic("42704", `type "domain_s.di" does not exist`)))
			run(referenceExec("DROP DOMAIN IF EXISTS domain_s.di", 0))
			domainReferenceNotice(t, notices, `type "domain_s.di" does not exist, skipping`, "")
			run(referenceExec("CREATE DOMAIN domain_s.di integer", 0))
			run(referenceExec("CREATE TABLE domain_s.recreated (v domain_s.di)", 0))
			run(referenceExec("DROP TABLE domain_s.recreated", 0))
			run(referenceExec("DROP DOMAIN domain_s.di", 0))
		})
	})
	t.Run("chain_columns_and_expression_closure", func(t *testing.T) {
		domainReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
			for _, q := range []string{
				"CREATE DOMAIN domain_s.di AS integer", "CREATE DOMAIN domain_s.chain AS domain_s.di",
				"CREATE TABLE domain_s.chain_table (keep integer,d1 domain_s.chain,d2 domain_s.di,calc integer DEFAULT 9::domain_s.di,CHECK (keep>0::domain_s.di))",
				"CREATE VIEW domain_s.cast_view AS SELECT keep::domain_s.di AS v FROM domain_s.chain_table",
				"CREATE TABLE domain_s.only_domain (v domain_s.chain)",
			} {
				run(referenceExec(q, 0))
			}
			run(referenceExec("INSERT INTO domain_s.chain_table (keep,d1,d2) VALUES (1,7,8),(2,NULL,NULL)", 2))
			run(referenceExec("INSERT INTO domain_s.only_domain VALUES (7),(NULL)", 2))
			want := controlsReferenceDiagnostic("2BP01", "cannot drop type domain_s.di because other objects depend on it")
			want.Detail = "type domain_s.chain depends on type domain_s.di\ncolumn d1 of table domain_s.chain_table depends on type domain_s.chain\ncolumn v of table domain_s.only_domain depends on type domain_s.chain\ncolumn d2 of table domain_s.chain_table depends on type domain_s.di\ndefault value for column calc of table domain_s.chain_table depends on type domain_s.di\nconstraint chain_table_keep_check on table domain_s.chain_table depends on type domain_s.di\nview domain_s.cast_view depends on type domain_s.di"
			want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			run(referenceErrorDiagnostic("DROP DOMAIN domain_s.di", want))
			*notices = nil
			run(referenceExec("DROP DOMAIN domain_s.di CASCADE", 0))
			domainReferenceNotice(t, notices, "drop cascades to 7 other objects", "drop cascades to type domain_s.chain\ndrop cascades to column d1 of table domain_s.chain_table\ndrop cascades to column v of table domain_s.only_domain\ndrop cascades to column d2 of table domain_s.chain_table\ndrop cascades to default value for column calc of table domain_s.chain_table\ndrop cascades to constraint chain_table_keep_check on table domain_s.chain_table\ndrop cascades to view domain_s.cast_view")
			run(referenceSelect("SELECT keep,calc FROM domain_s.chain_table ORDER BY keep", []string{"keep", "calc"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {int64(2), int64(9)}}))
			// Removed domain-dependent CHECK and DEFAULT, not the surviving columns.
			run(referenceExec("INSERT INTO domain_s.chain_table (keep) VALUES (-1)", 1))
			run(referenceSelect("SELECT keep,calc FROM domain_s.chain_table WHERE keep=-1", []string{"keep", "calc"}, []string{"INT4", "INT4"}, [][]any{{int64(-1), nil}}))
			run(referenceSelect("SELECT count(*) AS n FROM domain_s.only_domain", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
			want = controlsReferenceDiagnostic("42P01", `relation "domain_s.cast_view" does not exist`)
			want.Position = "15"
			s := referenceErrorDiagnostic("SELECT * FROM domain_s.cast_view", want)
			s.selectRows = true
			s.errorStage = "execute"
			run(s)
			want = controlsReferenceDiagnostic("42704", `type "domain_s.chain" does not exist`)
			want.Position = "32"
			run(referenceErrorDiagnostic("CREATE TABLE domain_s.probe (v domain_s.chain)", want))
		})
	})
	t.Run("schema_and_search_path", func(t *testing.T) {
		domainReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
			for _, q := range []string{"CREATE SCHEMA domain_other", "CREATE SCHEMA domain_external", "CREATE DOMAIN domain_s.di AS integer", "CREATE DOMAIN domain_other.di AS text"} {
				run(referenceExec(q, 0))
			}
			run(referenceExec("SET search_path TO domain_s,domain_other,public", 0))
			run(referenceSelect("SELECT '7'::di AS v FROM public.one", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(referenceExec("SET search_path TO domain_other,domain_s,public", 0))
			run(referencePrepared(referenceSelect("SELECT 'word'::di AS v FROM public.one", []string{"v"}, []string{"TEXT"}, [][]any{{"word"}})))
			run(referenceExec("CREATE TABLE domain_external.survivor (keep integer,d domain_s.di)", 0))
			run(referenceExec("INSERT INTO domain_external.survivor VALUES (1,7),(2,NULL)", 2))
			want := controlsReferenceDiagnostic("2BP01", "cannot drop schema domain_s because other objects depend on it")
			want.Detail = "type domain_s.di depends on schema domain_s\ncolumn d of table domain_external.survivor depends on type domain_s.di"
			want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			run(referenceErrorDiagnostic("DROP SCHEMA domain_s RESTRICT", want))
			*notices = nil
			run(referenceExec("DROP SCHEMA domain_s CASCADE", 0))
			domainReferenceNotice(t, notices, "drop cascades to 2 other objects", "drop cascades to type domain_s.di\ndrop cascades to column d of table domain_external.survivor")
			run(referenceSelect("SELECT keep FROM domain_external.survivor ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(referenceSelect("SELECT 'still here'::domain_other.di AS v FROM public.one", []string{"v"}, []string{"TEXT"}, [][]any{{"still here"}}))
		})
	})
	t.Run("ownership", func(t *testing.T) {
		roleReferenceCleanup(t, []string{"domain_creator", "domain_reader"})
		domainReferenceScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, recorder *referenceRecorder, notices *[]referenceDiagnostic) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
			if backend.pg {
				// Existing role fixture authority; native public bootstrap has these rights.
				if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC"); err != nil {
					t.Fatal(err)
				}
			}
			t.Cleanup(func() {
				if _, err := backend.db.ExecContext(p.ctx, "DROP DOMAIN IF EXISTS public.domain_owned CASCADE"); err != nil {
					t.Error(err)
				}
			})
			for _, q := range []string{"CREATE ROLE domain_creator", "CREATE ROLE domain_reader", "SET ROLE domain_reader"} {
				run(referenceExec(q, 0))
			}
			run(referenceErrorDiagnostic("CREATE DOMAIN domain_s.denied AS integer", controlsReferenceDiagnostic("42501", "permission denied for schema domain_s")))
			run(referenceExec("RESET ROLE", 0))
			run(referenceExec("SET ROLE domain_creator", 0))
			run(referenceExec("CREATE DOMAIN public.domain_owned AS integer", 0))
			run(referenceExec("RESET ROLE", 0))
			run(referenceExec("SET ROLE domain_reader", 0))
			run(referenceErrorDiagnostic("DROP DOMAIN public.domain_owned", controlsReferenceDiagnostic("42501", "must be owner of type domain_owned")))
			run(referenceExec("RESET ROLE", 0))
			if backend.pg {
				// External ACL-management control. Native type ACL syntax is excluded;
				// internal authorization units exercise its catalog authority instead.
				run(referenceExec("REVOKE USAGE ON DOMAIN public.domain_owned FROM PUBLIC", 0))
				run(referenceExec("SET ROLE domain_reader", 0))
				s := referenceErrorDiagnostic("CREATE DOMAIN public.domain_denied_base AS public.domain_owned", controlsReferenceDiagnostic("42501", "permission denied for type domain_owned"))
				s.errorStage = "execute"
				run(s)
				run(referenceExec("RESET ROLE", 0))
			}
			run(referenceExec("SET ROLE domain_creator", 0))
			run(referenceExec("DROP DOMAIN public.domain_owned", 0))
			run(referenceExec("RESET ROLE", 0))
		})
	})
}

func domainReferenceNotice(t *testing.T, records *[]referenceDiagnostic, message, detail string) {
	t.Helper()
	want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail}
	if len(*records) != 1 || (*records)[0] != want {
		t.Fatalf("domain notice got %#v want %#v", *records, want)
	}
	*records = nil
}
