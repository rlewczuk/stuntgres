package reference_test

import (
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Ordinary phases are real pinned PostgreSQL-compatible results. Active phases
// specify native simulation callback behavior, not an active SELinux oracle.
func TestReferenceSepgsqlSimulationCallbacks(t *testing.T) {
	const hash = "hstore_hash_extended(hstore,bigint)"
	const conversion = "pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean)"
	grouped := func(array bool) fixture.Step {
		key, values := "v", "('a=>1'::hstore),('a=>1'::hstore),('b=>2'::hstore)"
		if array {
			key, values = "ARRAY[v]", "('a'::ltree),('a'::ltree),('b'::ltree)"
		}
		return sepgsqlCount("SELECT count(*) AS count FROM (SELECT DISTINCT "+key+" FROM (VALUES "+values+") AS t(v) WHERE v IS NOT NULL) AS distinct_values", 2)
	}
	converted := fixture.Select("SELECT convert_to('é','LATIN1') AS value", []string{"value"}, []string{"BYTEA"}, [][]any{{"\xe9"}})
	empty := fixture.Select("SELECT convert_to('','LATIN1') AS value", []string{"value"}, []string{"BYTEA"}, [][]any{{""}})
	identity := fixture.Select("SELECT convert_to('é','UTF8') AS value", []string{"value"}, []string{"BYTEA"}, [][]any{{"é"}})
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("CREATE EXTENSION hstore", 0))
		run(fixture.Exec("CREATE EXTENSION ltree", 0))
		for _, step := range []fixture.Step{grouped(false), fixture.Prepared(grouped(false)), grouped(true), converted, fixture.Prepared(converted), empty, identity} {
			run(step)
		}
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.Policy.Rules = append(config.Policy.Rules, stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlS, Class: "db_procedure", Deny: []string{"execute"}})
		config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("CREATE EXTENSION hstore", 0))
		run(fixture.Exec("CREATE EXTENSION ltree", 0))
		for _, signature := range []string{hash, "hash_ltree_extended(ltree,bigint)", conversion} {
			run(fixture.Exec("SECURITY LABEL ON FUNCTION "+signature+" IS '"+sepgsqlS+"'", 0))
		}
		run(sepgsqlSet(sepgsqlR))
		// Direct callbacks do not inherit fictional SQL EXECUTE checks. The hash
		// procedure and converter deny execute but permit real trusted entry.
		for _, step := range []fixture.Step{grouped(false), fixture.Prepared(grouped(false)), grouped(true), converted, fixture.Prepared(converted)} {
			run(step)
		}
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		for _, signature := range []string{hash, "hash_ltree_extended(ltree,bigint)", conversion} {
			run(fixture.Exec("SECURITY LABEL ON FUNCTION "+signature+" IS '"+sepgsqlU+"'", 0))
		}
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlDenied(grouped(false).Query))
		run(sepgsqlDenied(grouped(true).Query))
		run(sepgsqlDenied(converted.Query))
		// No bytes, identical encoding and all-NULL hash keys invoke no converter
		// or scalar hash callback. Retained equality initialization remains ordinary.
		run(empty)
		run(identity)
		run(sepgsqlCount("SELECT count(*) AS count FROM (SELECT DISTINCT v FROM (VALUES (NULL::hstore),(NULL::hstore)) AS t(v) WHERE v IS NOT NULL) AS distinct_values", 0))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	})
}

// Support kernels are exercised through genuine installed objects and public
// SQL. The active branch specifies simulation outcomes, not stock SELinux parity.
func TestReferenceSepgsqlSimulationSupport(t *testing.T) {
	setup := func(run sepgsqlRun) {
		for _, query := range []string{
			"CREATE EXTENSION hstore",
			"CREATE EXTENSION ltree",
			"CREATE EXTENSION btree_gin",
			"CREATE EXTENSION citext",
			"CREATE TYPE cb_range AS RANGE (subtype=ltree)",
			"CREATE TABLE cb_rows(id integer,v hstore,a ltree[],r cb_range)",
			"CREATE INDEX cb_btree ON cb_rows(v)",
			"CREATE INDEX cb_hash ON cb_rows USING hash(v)",
			"CREATE INDEX cb_array ON cb_rows USING gin(a)",
			"CREATE TABLE cb_scalar(v integer)",
			"CREATE INDEX cb_scalar_i ON cb_scalar USING gin(v)",
		} {
			run(fixture.Exec(query, 0))
		}
		run(fixture.Exec("INSERT INTO cb_rows VALUES(1,'a=>1',ARRAY['a'::ltree,NULL],cb_range('a','z','[]')),(2,'a=>1',ARRAY['a'::ltree,NULL],cb_range('a','z','[]')),(3,'b=>2',ARRAY['b'::ltree],cb_range('b','z','[]')),(4,NULL,NULL,NULL)", 4))
	}
	ordered := fixture.Select("SELECT id FROM cb_rows ORDER BY v,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}})
	emptyOrdered := fixture.Select("SELECT id FROM cb_rows WHERE false ORDER BY v", []string{"id"}, []string{"INT4"}, nil)
	nullOrdered := fixture.Select("SELECT id FROM (VALUES (1,NULL::hstore),(2,NULL::hstore)) AS t(id,v) ORDER BY v,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
	member := sepgsqlCount("SELECT count(*) AS count FROM cb_rows WHERE v IN (SELECT v FROM cb_rows WHERE id<=2)", 2)
	grouped := sepgsqlCount("SELECT count(*) AS count FROM (SELECT DISTINCT a FROM cb_rows WHERE a IS NOT NULL) AS distinct_arrays", 2)
	ranged := sepgsqlCount("SELECT count(*) AS count FROM cb_rows WHERE r @> 'c'::ltree", 3)
	unbounded := sepgsqlBool("SELECT isempty(cb_range(NULL,NULL)) AS value", "value", false)
	constructor := fixture.Select("SELECT prosrc AS source,proisstrict AS strict,prorettype='cb_range'::regtype AS result,pronamespace='public'::regnamespace AS namespace FROM pg_proc WHERE oid='cb_range(ltree,ltree)'::regprocedure", []string{"source", "strict", "result", "namespace"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"range_constructor2", false, true, true}})
	constructorLabel := sepgsqlText("SELECT label FROM pg_seclabel WHERE classoid='pg_proc'::regclass AND objoid='cb_range(ltree,ltree)'::regprocedure AND objsubid=0 AND provider='selinux'", "label", sepgsqlP)
	family := sepgsqlBool("SELECT rngmultitypid='cb_multirange'::regtype AS companion FROM pg_range WHERE rngtypid='cb_range'::regtype", "companion", true)
	constructors := []fixture.Step{constructor}
	constructorLabels := []fixture.Step{constructorLabel}
	for _, declaration := range []struct {
		signature, source, result string
		strict                    bool
	}{
		{"cb_range(ltree,ltree,text)", "range_constructor3", "cb_range", false},
		{"cb_multirange()", "multirange_constructor0", "cb_multirange", true},
		{"cb_multirange(cb_range)", "multirange_constructor1", "cb_multirange", true},
		{"cb_multirange(cb_range[])", "multirange_constructor2", "cb_multirange", true},
	} {
		// Resolve each real declaration once per query by its exact signature.
		// Broad catalog joins obscure this small cohort's identity assertions.
		constructors = append(constructors, fixture.Select("SELECT prosrc AS source,proisstrict AS strict,prorettype='"+declaration.result+"'::regtype AS result,pronamespace='public'::regnamespace AS namespace FROM pg_proc WHERE oid='"+declaration.signature+"'::regprocedure", []string{"source", "strict", "result", "namespace"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{declaration.source, declaration.strict, true, true}}))
		constructorLabels = append(constructorLabels, sepgsqlText("SELECT label FROM pg_seclabel WHERE classoid='pg_proc'::regclass AND objoid='"+declaration.signature+"'::regprocedure AND objsubid=0 AND provider='selinux'", "label", sepgsqlP))
	}
	cycle := sepgsqlCount("WITH RECURSIVE walk(k,n) AS (SELECT v,1 FROM cb_rows WHERE id=1 UNION ALL SELECT k,n+1 FROM walk WHERE n<3) SEARCH DEPTH FIRST BY k SET ordering CYCLE k SET is_cycle USING path SELECT count(*) AS count FROM walk", 2)
	original := sepgsqlText("SELECT pg_catalog.text(true) AS value", "value", "true")
	alias := sepgsqlText("SELECT public.citext(true)::text AS value", "value", "true")
	originalNull := sepgsqlBool("SELECT pg_catalog.text(NULL::boolean) IS NULL AS skipped", "skipped", true)
	aliasNull := sepgsqlBool("SELECT public.citext(NULL::boolean)::text IS NULL AS skipped", "skipped", true)
	ordinary := func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		for _, step := range constructors {
			run(step)
			run(fixture.Prepared(step))
		}
		for _, step := range []fixture.Step{family, ordered, emptyOrdered, nullOrdered, unbounded, member, grouped, ranged, cycle} {
			run(step)
			run(fixture.Prepared(step))
		}
		run(fixture.Exec("INSERT INTO cb_scalar VALUES(7),(7),(NULL)", 3))
		run(sepgsqlCount("SELECT count(*) AS count FROM cb_scalar WHERE v=7", 2))
		run(fixture.Exec("ALTER FUNCTION hstore_cmp(hstore,hstore) RENAME TO cb_hstore_cmp", 0))
		run(ordered)
		run(fixture.Prepared(ordered))
		for _, step := range []fixture.Step{original, alias, originalNull, aliasNull} {
			run(step)
			run(fixture.Prepared(step))
		}
	}
	sepgsqlCase(t, ordinary, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.Policy.Rules = append(config.Policy.Rules,
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlS, Class: "db_procedure", Deny: []string{"execute"}},
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlU, Class: "db_procedure", Allow: []string{"execute"}},
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_table", Allow: []string{"insert"}},
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_column", Allow: []string{"insert"}})
		config.Policy.TrustedTransitions = append(config.Policy.TrustedTransitions, stuntgres.SepgsqlTrustedTransition{Source: sepgsqlR, Procedure: sepgsqlU, Result: sepgsqlT})
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		setup(run)
		admin := func() { run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true)) }
		label := func(signature, value string) {
			run(fixture.Exec("SECURITY LABEL ON FUNCTION "+signature+" IS '"+value+"'", 0))
		}
		// Each exact generated declaration retains its own inherited P label.
		// The range constructors are nonstrict; all three multirange forms are strict.
		run(family)
		run(fixture.Prepared(family))
		for _, steps := range [][]fixture.Step{constructors, constructorLabels} {
			for _, step := range steps {
				run(step)
				run(fixture.Prepared(step))
			}
		}
		for _, signature := range []string{"hstore_cmp(hstore,hstore)", "hstore_hash(hstore)", "hstore_hash_extended(hstore,bigint)", "ltree_cmp(ltree,ltree)", "hash_ltree(ltree)", "hash_ltree_extended(ltree,bigint)", "gin_extract_value_int4(integer,internal)"} {
			label(signature, sepgsqlS)
		}
		run(sepgsqlSet(sepgsqlR))
		for _, step := range []fixture.Step{ordered, emptyOrdered, nullOrdered, member, grouped, ranged, cycle} {
			run(step)
			run(fixture.Prepared(step))
		}
		run(fixture.Exec("INSERT INTO cb_scalar VALUES(7),(7),(NULL)", 3))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		admin()
		run(fixture.Exec("ALTER FUNCTION hstore_cmp(hstore,hstore) RENAME TO cb_hstore_cmp", 0))
		run(sepgsqlSet(sepgsqlR))
		run(ordered)
		run(fixture.Prepared(ordered))
		run(fixture.Prepared(ranged))
		admin()
		label("cb_hstore_cmp(hstore,hstore)", sepgsqlU)
		label("ltree_cmp(ltree,ltree)", sepgsqlU)
		run(sepgsqlSet(sepgsqlR))
		// The actual serial SortNode compares non-NULL hstore keys through the
		// renamed installed helper, without acquiring SQL EXECUTE permission.
		for _, query := range []string{ordered.Query, ranged.Query} {
			denied := sepgsqlDenied(query)
			denied.SelectRows, denied.ErrorStage = true, "execute"
			run(denied)
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
			run(fixture.Prepared(denied))
			run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		}
		admin()
		run(constructorLabel)
		label("cb_range(ltree,ltree)", sepgsqlU)
		run(sepgsqlSet(sepgsqlR))
		// NULL bounds skip subtype comparison, not the nonstrict constructor's
		// own genuine entry. Isolate it, then restore its asserted original P.
		constructorDenied := sepgsqlDenied(unbounded.Query)
		constructorDenied.SelectRows, constructorDenied.ErrorStage = true, "execute"
		run(constructorDenied)
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		run(fixture.Prepared(constructorDenied))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		admin()
		label("cb_range(ltree,ltree)", sepgsqlP)
		run(constructorLabel)
		run(sepgsqlSet(sepgsqlR))
		// Selected support initialization is not entry: empty input and NULL
		// sort keys need no hstore comparison; an unbounded range needs no leaf.
		for _, step := range []fixture.Step{emptyOrdered, nullOrdered, unbounded} {
			run(step)
			run(fixture.Prepared(step))
		}
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		admin()
		label("cb_hstore_cmp(hstore,hstore)", sepgsqlS)
		label("ltree_cmp(ltree,ltree)", sepgsqlS)
		run(sepgsqlSet(sepgsqlR))
		for _, step := range []fixture.Step{ordered, ranged} {
			run(step)
			run(fixture.Prepared(step))
		}
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		admin()
		label("hash_ltree_extended(ltree,bigint)", sepgsqlU)
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlDenied(grouped.Query))
		run(sepgsqlCount("SELECT count(*) AS count FROM (SELECT DISTINCT a FROM (VALUES(NULL::ltree[]),(NULL::ltree[])) AS t(a) WHERE a IS NOT NULL) AS distinct_arrays", 0))
		admin()
		label("hash_ltree_extended(ltree,bigint)", sepgsqlS)
		label("hstore_eq(hstore,hstore)", sepgsqlU)
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlDenied(cycle.Query))
		admin()
		label("hstore_eq(hstore,hstore)", sepgsqlP)
		// REL_18_6 citext--1.4.sql installs citext(boolean) AS 'booltext'.
		// Only the original pg_catalog.text(boolean) has the fmgrtab exemption;
		// the genuine installed INTERNAL alias must still enter through FHET.
		label("pg_catalog.text(boolean)", sepgsqlU)
		label("public.citext(boolean)", sepgsqlU)
		run(sepgsqlSet(sepgsqlR))
		for _, step := range []fixture.Step{original, originalNull, aliasNull} {
			run(step)
			run(fixture.Prepared(step))
		}
		run(sepgsqlDenied(alias.Query))
		run(fixture.Prepared(sepgsqlDenied(alias.Query)))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	})
}
