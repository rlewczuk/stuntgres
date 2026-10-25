package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTypeLifecycleDrop(t *testing.T) {
	for _, tc := range []struct{ name, create, value string }{
		{"enum", "CREATE TYPE tl_src.t AS ENUM ('z','a')", "z"},
		{"domain", "CREATE DOMAIN tl_src.t AS integer", "7"},
		{"composite", "CREATE TYPE tl_src.t AS (n integer,note text)", "(7,x)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				domainIdentityDDL(t, p, c, r, "CREATE SCHEMA tl_src", tc.create, "CREATE TYPE tl_src.free AS ENUM ()", "CREATE TYPE tl_src.peer AS ENUM ('z')", "CREATE TABLE tl_src.holder(keep integer,v tl_src.t)")
				run(fixture.Exec("INSERT INTO tl_src.holder VALUES(11,$1),(12,NULL)", 2, tc.value))
				oid := domainAlterOID(t, p, c, "SELECT 'tl_src.t'::regtype::oid")
				array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='tl_src.t'::regtype::oid")
				free := domainAlterOID(t, p, c, "SELECT 'tl_src.free'::regtype::oid")
				want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type tl_src.t because other objects depend on it")
				want.Detail = "column v of table tl_src.holder depends on type tl_src.t"
				want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				for _, suffix := range []string{"", " RESTRICT"} {
					typeLifecycleFailure(t, p, c, "DROP TYPE tl_src.t"+suffix, want)
				}
				typeLifecycleFailure(t, p, c, "DROP TYPE tl_src.free,tl_src.absent CASCADE", fixture.ControlsReferenceDiagnostic("42704", `type "tl_src.absent" does not exist`))
				batch := want
				batch.Message = "cannot drop desired object(s) because other objects depend on them"
				typeLifecycleFailure(t, p, c, "DROP TYPE tl_src.free,tl_src.t RESTRICT", batch)
				domainIdentitySameOID(t, p, c, "SELECT 'tl_src.free'::regtype::oid", free)
				domainIdentitySameOID(t, p, c, "SELECT 'tl_src.t'::regtype::oid", oid)
				for _, tc := range []struct{ q, message string }{
					{"DROP TYPE IF EXISTS tl_src.absent", `type "tl_src.absent" does not exist, skipping`},
					{"DROP TYPE IF EXISTS tl_missing.t", `schema "tl_missing" does not exist, skipping`},
				} {
					for _, prepared := range []bool{false, true} {
						*notices = nil
						s := fixture.Exec(tc.q, 0)
						s.Prepared = prepared
						run(s)
						domainIdentityNotices(t, notices, typeLifecycleNotice(tc.message))
					}
				}
				*notices = nil
				run(fixture.Prepared(fixture.Exec("DROP TYPE tl_src.t CASCADE", 0)))
				domainIdentityNotices(t, notices, typeLifecycleNotice("drop cascades to column v of table tl_src.holder"))
				run(fixture.Select("SELECT keep FROM tl_src.holder ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(11)}, {int64(12)}}))
				typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_type WHERE oid=$1::oid OR oid=$2::oid", 0, oid, array)
				domainIdentityDDL(t, p, c, r, "DROP TYPE tl_src.free,tl_src.free RESTRICT", "CREATE TYPE tl_src.parent AS ENUM ('z')", "CREATE DOMAIN tl_src.child AS tl_src.parent", "DROP TYPE tl_src.parent,tl_src.child,tl_src.parent RESTRICT")
				run(fixture.Select("SELECT 'z'::tl_src.peer::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"z"}}))
			})
		})
	}
	t.Run("recursive_expression_and_column_closure", func(t *testing.T) {
		typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			domainIdentityDDL(t, p, c, r, "CREATE SCHEMA tl_src", "CREATE SCHEMA tl_dst",
				"CREATE DOMAIN tl_src.t AS integer", "CREATE DOMAIN tl_src.child AS tl_src.t", "CREATE TYPE tl_src.box AS(v tl_src.t,keep integer)",
				"CREATE DOMAIN tl_src.check_only AS integer CONSTRAINT mentions CHECK(VALUE>0::tl_src.t)",
				"CREATE TABLE tl_src.holder(keep integer,v tl_src.child,a tl_src.t[],calc integer DEFAULT 9::tl_src.t,CONSTRAINT mentions CHECK(keep>0::tl_src.t))",
				"CREATE INDEX tl_index ON tl_src.holder(v)", "CREATE VIEW tl_src.live AS SELECT v FROM tl_src.holder", "CREATE VIEW tl_src.live2 AS SELECT v FROM tl_src.live", "CREATE MATERIALIZED VIEW tl_src.mv AS SELECT v FROM tl_src.holder",
				"CREATE TABLE tl_src.zero(v tl_src.t)", "CREATE FUNCTION tl_src.formal(tl_src.t) RETURNS integer LANGUAGE SQL AS 'SELECT $1::integer'", "CREATE FUNCTION tl_src.result(integer) RETURNS tl_src.t LANGUAGE SQL AS 'SELECT $1'", "CREATE FUNCTION tl_src.text_only(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1::tl_src.t::integer'",
				"CREATE TABLE tl_src.generated(keep integer,calc integer GENERATED ALWAYS AS (keep::tl_src.t) STORED)")
			run(fixture.Exec("INSERT INTO tl_src.holder(keep,v,a) VALUES(1,7,'{8,NULL}'),(2,NULL,NULL)", 2))
			run(fixture.Exec("INSERT INTO tl_src.zero VALUES(7),(NULL)", 2))
			run(fixture.Exec("INSERT INTO tl_src.generated(keep) VALUES(5),(6)", 2))
			domainIdentityDDL(t, p, c, r, "ALTER TYPE tl_src.t RENAME TO moved", "ALTER TYPE tl_src.moved SET SCHEMA tl_dst")
			*notices = nil
			run(fixture.Exec("DROP TYPE tl_dst.moved CASCADE", 0))
			fixture.DomainReferenceNotice(t, notices, "drop cascades to 14 other objects", "drop cascades to column a of table tl_src.holder\ndrop cascades to type tl_src.child\ndrop cascades to column v of table tl_src.holder\ndrop cascades to view tl_src.live\ndrop cascades to materialized view tl_src.mv\ndrop cascades to view tl_src.live2\ndrop cascades to column v of composite type tl_src.box\ndrop cascades to constraint mentions\ndrop cascades to default value for column calc of table tl_src.holder\ndrop cascades to constraint mentions on table tl_src.holder\ndrop cascades to column v of table tl_src.zero\ndrop cascades to function tl_src.formal(tl_dst.moved)\ndrop cascades to function tl_src.result(integer)\ndrop cascades to column calc of table tl_src.generated")
			run(fixture.Select("SELECT keep,calc FROM tl_src.holder ORDER BY keep", []string{"keep", "calc"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(9)}, {int64(2), int64(9)}}))
			run(fixture.Exec("INSERT INTO tl_src.holder(keep) VALUES(-1)", 1))
			run(fixture.Select("SELECT calc FROM tl_src.holder WHERE keep=-1", []string{"calc"}, []string{"INT4"}, [][]any{{nil}}))
			typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM tl_src.zero", 2)
			run(fixture.Select("SELECT keep FROM tl_src.generated ORDER BY keep", []string{"keep"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(6)}}))
			run(fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='tl_src.box'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"keep"}}))
			run(fixture.Select("SELECT 7::tl_src.check_only AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_constraint WHERE contypid='tl_src.check_only'::regtype::oid", 0)
			run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='tl_src'::regnamespace::oid ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{{"text_only"}}))
			typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_class WHERE relnamespace='tl_src'::regnamespace::oid AND relname IN ('live','live2','mv','tl_index')", 0)
		})
	})
	t.Run("typed_table_source", func(t *testing.T) {
		typeLifecycleScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
			domainIdentityDDL(t, p, c, r, "CREATE TYPE public.tl_pair AS(n integer,note text)", "CREATE TABLE public.tl_typed OF public.tl_pair", "CREATE TABLE public.tl_unrelated(keep integer)")
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO public.tl_unrelated VALUES(11)", 1))
			want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type tl_pair because other objects depend on it")
			want.Detail = "table tl_typed depends on type tl_pair"
			want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			typeLifecycleFailure(t, p, c, "DROP TYPE public.tl_pair RESTRICT", want)
			*notices = nil
			domainIdentityDDL(t, p, c, r, "DROP TYPE public.tl_pair CASCADE")
			domainIdentityNotices(t, notices, typeLifecycleNotice("drop cascades to table tl_typed"))
			typeLifecycleCount(t, p, c, r, "SELECT count(*) AS n FROM pg_class WHERE relnamespace='public'::regnamespace::oid AND relname='tl_typed'", 0)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT keep FROM public.tl_unrelated", []string{"keep"}, []string{"INT4"}, [][]any{{int64(11)}}))
		})
	})
}
