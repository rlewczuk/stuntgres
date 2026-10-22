package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainIdentityDropAtomicity(t *testing.T) {
	t.Run("moved_identity_batch_and_notices", func(t *testing.T) {
		domainIdentityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			domainIdentityDDL(t, p, c, r, "CREATE SCHEMA di_src", "CREATE SCHEMA di_dst", "CREATE DOMAIN di_src.d AS integer", "CREATE DOMAIN di_src.free AS integer", "CREATE TABLE di_src.t(v di_src.d,keep integer)")
			run(fixture.Exec("INSERT INTO di_src.t VALUES(7,11),(NULL,12)", 2))
			domainIdentityDDL(t, p, c, r, "ALTER DOMAIN di_src.d RENAME TO renamed", "ALTER DOMAIN di_src.renamed SET SCHEMA di_dst")
			oid := domainAlterOID(t, p, c, "SELECT 'di_dst.renamed'::regtype::oid")
			want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type di_dst.renamed because other objects depend on it")
			want.Detail = "column v of table di_src.t depends on type di_dst.renamed"
			want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			for _, prepared := range []bool{false, true} {
				for _, suffix := range []string{"", " RESTRICT"} {
					s := fixture.ErrorDiagnostic("DROP DOMAIN di_dst.renamed"+suffix, want)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
				}
				missing := fixture.ControlsReferenceDiagnostic("42704", `type "di_src.absent" does not exist`)
				s := fixture.ErrorDiagnostic("DROP DOMAIN di_src.free,di_src.absent CASCADE", missing)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				batch := want
				batch.Message = "cannot drop desired object(s) because other objects depend on them"
				s = fixture.ErrorDiagnostic("DROP DOMAIN di_src.free,di_dst.renamed RESTRICT", batch)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				domainIdentitySameOID(t, p, c, "SELECT 'di_dst.renamed'::regtype::oid", oid)
				run(fixture.Select("SELECT v,keep FROM di_src.t ORDER BY keep", []string{"v", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(11)}, {nil, int64(12)}}))
				for _, q := range []string{"DROP DOMAIN IF EXISTS di_src.absent", "DROP DOMAIN IF EXISTS di_missing.d"} {
					*notices = nil
					s := fixture.Exec(q, 0)
					s.Prepared = prepared
					run(s)
					message := `type "di_src.absent" does not exist, skipping`
					if q == "DROP DOMAIN IF EXISTS di_missing.d" {
						message = `schema "di_missing" does not exist, skipping`
					}
					n := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message}
					domainIdentityNotices(t, notices, n)
				}
			}
			*notices = nil
			run(fixture.Prepared(fixture.Exec("DROP DOMAIN di_dst.renamed CASCADE", 0)))
			domainIdentityNotices(t, notices, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to column v of table di_src.t"})
			run(fixture.Select("SELECT keep FROM di_src.t ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}}))
			run(fixture.Select("SELECT count(*) AS n FROM pg_type WHERE oid=$1::oid OR typelem=$1::oid", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}, oid))
			domainIdentityDDL(t, p, c, r, "CREATE DOMAIN di_src.parent AS integer", "CREATE DOMAIN di_src.child AS di_src.parent", "DROP DOMAIN di_src.parent,di_src.child,di_src.parent RESTRICT", "DROP DOMAIN di_src.free RESTRICT")
		})
	})
	t.Run("renamed_recursive_closure", func(t *testing.T) {
		domainIdentityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			domainIdentityDDL(t, p, c, r, "CREATE SCHEMA di_src", "CREATE SCHEMA di_dst",
				"CREATE DOMAIN di_src.d AS integer", "CREATE DOMAIN di_src.child AS di_src.d",
				"CREATE TYPE di_src.box AS(v di_src.d,keep integer)",
				"CREATE DOMAIN di_src.check_only AS integer CONSTRAINT mentions CHECK(VALUE>0::di_src.d)",
				"CREATE TABLE di_src.t(keep integer,v di_src.child,a di_src.d[],calc integer DEFAULT 9::di_src.d,CONSTRAINT mentions CHECK(keep>0::di_src.d))",
				"CREATE INDEX di_index ON di_src.t(v)", "CREATE VIEW di_src.v AS SELECT v FROM di_src.t", "CREATE VIEW di_src.v2 AS SELECT v FROM di_src.v",
				"CREATE MATERIALIZED VIEW di_src.mv AS SELECT v FROM di_src.t",
				"CREATE TABLE di_src.zero(v di_src.d)",
				"CREATE FUNCTION di_src.formal(di_src.d) RETURNS integer LANGUAGE SQL AS 'SELECT $1::integer'",
				"CREATE FUNCTION di_src.result(integer) RETURNS di_src.d LANGUAGE SQL AS 'SELECT $1'",
				"CREATE FUNCTION di_src.text_only(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1::di_src.d::integer'")
			run(fixture.Exec("INSERT INTO di_src.t(keep,v,a) VALUES(1,7,'{8,NULL}'),(2,NULL,NULL)", 2))
			run(fixture.Exec("INSERT INTO di_src.zero VALUES(7),(NULL)", 2))
			domainIdentityDDL(t, p, c, r, "CREATE TABLE di_src.generated(keep integer,calc integer GENERATED ALWAYS AS (keep::di_src.d) STORED)")
			run(fixture.Exec("INSERT INTO di_src.generated(keep) VALUES(5),(6)", 2))
			domainIdentityDDL(t, p, c, r, "ALTER DOMAIN di_src.d RENAME TO renamed", "ALTER DOMAIN di_src.renamed SET SCHEMA di_dst")
			*notices = nil
			run(fixture.Exec("DROP DOMAIN di_dst.renamed CASCADE", 0))
			fixture.DomainReferenceNotice(t, notices, "drop cascades to 14 other objects", "drop cascades to column a of table di_src.t\ndrop cascades to type di_src.child\ndrop cascades to column v of table di_src.t\ndrop cascades to view di_src.v\ndrop cascades to materialized view di_src.mv\ndrop cascades to view di_src.v2\ndrop cascades to column v of composite type di_src.box\ndrop cascades to constraint mentions\ndrop cascades to default value for column calc of table di_src.t\ndrop cascades to constraint mentions on table di_src.t\ndrop cascades to column v of table di_src.zero\ndrop cascades to function di_src.formal(di_dst.renamed)\ndrop cascades to function di_src.result(integer)\ndrop cascades to column calc of table di_src.generated")
			run(fixture.Select("SELECT keep,calc FROM di_src.t ORDER BY keep", []string{"keep", "calc"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {int64(2), int64(9)}}))
			run(fixture.Exec("INSERT INTO di_src.t(keep) VALUES(-1)", 1))
			run(fixture.Select("SELECT calc FROM di_src.t WHERE keep=-1", []string{"calc"}, []string{"INT4"}, [][]any{{nil}}))
			run(fixture.Select("SELECT count(*) AS n FROM di_src.zero", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(fixture.Select("SELECT keep FROM di_src.generated ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(6)}}))
			run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='di_src.generated'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"keep"}}))
			run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='di_src.box'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"keep"}}))
			run(fixture.Select("SELECT 7::di_src.check_only AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE contypid='di_src.check_only'::regtype::oid", []string{"conname"}, []string{"NAME"}, [][]any{}))
			run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='di_src'::regnamespace::oid ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{{"text_only"}}))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='di_src'::regnamespace::oid AND relname IN ('v','v2','mv','di_index') ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{}))
		})
	})
	t.Run("late_permission_failure", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"di_batch_owner", "di_batch_other"})
		domainIdentityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			domainIdentityDDL(t, p, c, r, "CREATE ROLE di_batch_owner", "CREATE ROLE di_batch_other", "CREATE SCHEMA di_src", "GRANT USAGE,CREATE ON SCHEMA di_src TO di_batch_owner,di_batch_other", "SET ROLE di_batch_owner", "CREATE DOMAIN di_src.first AS integer", "RESET ROLE", "SET ROLE di_batch_other", "CREATE DOMAIN di_src.last AS integer", "RESET ROLE", "CREATE TABLE di_src.t(v di_src.first,keep integer)")
			run(fixture.Exec("INSERT INTO di_src.t VALUES(7,11)", 1))
			domainIdentityDDL(t, p, c, r, "SET ROLE di_batch_owner")
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("DROP DOMAIN IF EXISTS di_src.first,di_src.last CASCADE", fixture.ControlsReferenceDiagnostic("42501", "must be owner of type di_src.last"))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
			}
			domainIdentityDDL(t, p, c, r, "RESET ROLE")
			run(fixture.Select("SELECT v,keep FROM di_src.t", []string{"v", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(11)}}))
			run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='di_src'::regnamespace::oid AND typtype='d' ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{{"first"}, {"last"}}))
		})
	})
}
