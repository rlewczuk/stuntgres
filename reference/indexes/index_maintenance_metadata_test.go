package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIndexMaintenanceMetadataOracle(t *testing.T) {
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(unused INT,n INT,s TEXT)", 0))
		run(fixture.Exec("CREATE INDEX ni ON t(n ASC NULLS LAST,n ASC NULLS FIRST,n DESC NULLS LAST,n DESC NULLS FIRST)", 0))
		run(fixture.Exec("CREATE INDEX ei ON t(lower(s),(n+1),n DESC NULLS LAST)", 0))
		run(fixture.Exec("CREATE INDEX ci ON t((s COLLATE \"C\"),(s COLLATE \"POSIX\"),s)", 0))
		run(fixture.Exec("CREATE TABLE q(n INT CONSTRAINT u UNIQUE NULLS NOT DISTINCT)", 0))
		run(fixture.Exec("CREATE TABLE d(n INT CONSTRAINT distinct_u UNIQUE)", 0))
		run(fixture.Exec("ALTER TABLE t DROP COLUMN unused", 0))
		if b.PG {
			q := `SELECT c.relname,i.indnatts,i.indnkeyatts,i.indisunique,i.indnullsnotdistinct,i.indisprimary,i.indisexclusion,i.indimmediate,i.indisclustered,i.indisvalid,i.indcheckxmin,i.indisready,i.indislive,i.indisreplident,i.indkey::text,i.indoption::text,i.indexprs IS NULL AS no_expr,i.indpred IS NULL AS no_pred,i.indexrelid=c.oid AS same_index,i.indrelid=p.oid AS same_parent FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_class p ON p.oid=i.indrelid WHERE c.relname IN ('ni','ei','ci','u','distinct_u') ORDER BY c.relname`
			cols := []string{"relname", "indnatts", "indnkeyatts", "indisunique", "indnullsnotdistinct", "indisprimary", "indisexclusion", "indimmediate", "indisclustered", "indisvalid", "indcheckxmin", "indisready", "indislive", "indisreplident", "indkey", "indoption", "no_expr", "no_pred", "same_index", "same_parent"}
			types := []string{"NAME", "INT2", "INT2", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL"}
			expected := [][]any{
				{"ci", int64(3), int64(3), false, false, false, false, true, false, true, false, true, true, false, "3 3 3", "0 0 0", true, true, true, true},
				{"distinct_u", int64(1), int64(1), true, false, false, false, true, false, true, false, true, true, false, "1", "0", true, true, true, true},
				{"ei", int64(3), int64(3), false, false, false, false, true, false, true, false, true, true, false, "0 0 2", "0 0 1", false, true, true, true},
				{"ni", int64(4), int64(4), false, false, false, false, true, false, true, false, true, true, false, "2 2 2 2", "0 2 1 3", true, true, true, true},
				{"u", int64(1), int64(1), true, true, false, false, true, false, true, false, true, true, false, "1", "0", true, true, true, true},
			}
			run(fixture.Select(q, cols, types, expected))
			run(fixture.Select(`SELECT c.relname,i.indclass::text,i.indcollation::text,pg_get_expr(i.indexprs,i.indrelid) AS expressions FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE c.relname IN ('ni','ei','ci','u','distinct_u') ORDER BY c.relname`, []string{"relname", "indclass", "indcollation", "expressions"}, []string{"NAME", "TEXT", "TEXT", "TEXT"}, [][]any{
				{"ci", "3126 3126 3126", "950 951 100", nil}, {"distinct_u", "1978", "0", nil}, {"ei", "3126 1978 1978", "100 0 0", "lower(s), (n + 1)"}, {"ni", "1978 1978 1978 1978", "0 0 0 0", nil}, {"u", "1978", "0", nil},
			}))
		}
		run(fixture.Select(`SELECT schemaname,tablename,indexname,tablespace,indexdef FROM pg_catalog.pg_indexes WHERE schemaname='public' AND (tablename='t' OR tablename='q' OR tablename='d') ORDER BY indexname`, []string{"schemaname", "tablename", "indexname", "tablespace", "indexdef"}, []string{"NAME", "NAME", "NAME", "NAME", "TEXT"}, [][]any{
			{"public", "t", "ci", nil, `CREATE INDEX ci ON public.t USING btree (s COLLATE "C", s COLLATE "POSIX", s)`},
			{"public", "d", "distinct_u", nil, "CREATE UNIQUE INDEX distinct_u ON public.d USING btree (n)"},
			{"public", "t", "ei", nil, "CREATE INDEX ei ON public.t USING btree (lower(s), ((n + 1)), n DESC NULLS LAST)"},
			{"public", "t", "ni", nil, "CREATE INDEX ni ON public.t USING btree (n, n NULLS FIRST, n DESC NULLS LAST, n DESC)"},
			{"public", "q", "u", nil, "CREATE UNIQUE INDEX u ON public.q USING btree (n) NULLS NOT DISTINCT"},
		}))
		run(fixture.Exec("ALTER INDEX ni RENAME TO renamed_ni", 0))
		run(fixture.Exec("REINDEX TABLE t", 0))
		if b.PG {
			run(fixture.Select(`SELECT c.relname,i.indkey::text,i.indclass::text,i.indcollation::text,i.indoption::text FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE c.relname IN ('renamed_ni','ei','ci') ORDER BY c.relname`, []string{"relname", "indkey", "indclass", "indcollation", "indoption"}, []string{"NAME", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
				{"ci", "3 3 3", "3126 3126 3126", "950 951 100", "0 0 0"}, {"ei", "0 0 2", "3126 1978 1978", "100 0 0", "0 0 1"}, {"renamed_ni", "2 2 2 2", "1978 1978 1978 1978", "0 0 0 0", "0 2 1 3"},
			}))
		}
		run(fixture.Exec("ALTER TABLE t RENAME COLUMN s TO renamed_s", 0))
		if b.PG {
			run(fixture.Select(`SELECT i.indkey::text,i.indclass::text,i.indcollation::text,i.indexprs IS NULL AS no_expr,pg_get_indexdef(c.oid) AS definition FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE c.relname='ci'`, []string{"indkey", "indclass", "indcollation", "no_expr", "definition"}, []string{"TEXT", "TEXT", "TEXT", "BOOL", "TEXT"}, [][]any{{"3 3 3", "3126 3126 3126", "950 951 100", true, `CREATE INDEX ci ON public.t USING btree (renamed_s COLLATE "C", renamed_s COLLATE "POSIX", renamed_s)`}}))
		}
		run(fixture.Exec("DROP INDEX renamed_ni", 0))
		run(fixture.Select("SELECT indexname FROM pg_catalog.pg_indexes WHERE schemaname='public' AND indexname='renamed_ni'", []string{"indexname"}, []string{"NAME"}, nil))
	})
}

func TestReferenceIndexMaintenanceOrderingProviderOracle(t *testing.T) {
	// Independently pinned builtin class identities from tests-1 PG18. Domain
	// declarations retain their own type identities but inherit base providers.
	providers := []struct {
		name, typ, declared, className, input, coll string
		oid                                         int64
	}{
		{"bool", "BOOLEAN", "boolean", "bool_ops", "boolean", "0", 10003},
		{"bytea", "BYTEA", "bytea", "bytea_ops", "bytea", "0", 10006},
		{"char", `"char"`, `"char"`, "char_ops", `"char"`, "0", 10007},
		{"name", "NAME", "name", "name_ops", "name", "950", 10028},
		{"int2", "SMALLINT", "smallint", "int2_ops", "smallint", "0", 1979},
		{"int4", "INTEGER", "integer", "int4_ops", "integer", "0", 1978},
		{"int8", "BIGINT", "bigint", "int8_ops", "bigint", "0", 3124},
		{"text", "TEXT", "text", "text_ops", "text", "100", 3126},
		{"oid", "OID", "oid", "oid_ops", "oid", "0", 1981},
		{"bpchar", "CHARACTER(4)", "character", "bpchar_ops", "character", "100", 10004},
		{"varchar", "VARCHAR(4)", "character varying", "text_ops", "text", "100", 3126},
		{"float4", "REAL", "real", "float4_ops", "real", "0", 10012},
		{"float8", "DOUBLE PRECISION", "double precision", "float8_ops", "double precision", "0", 3123},
		{"date", "DATE", "date", "date_ops", "date", "0", 3122},
		{"time", "TIME", "time without time zone", "time_ops", "time without time zone", "0", 10038},
		{"timestamp", "TIMESTAMP", "timestamp without time zone", "timestamp_ops", "timestamp without time zone", "0", 3128},
		{"timestamptz", "TIMESTAMPTZ", "timestamp with time zone", "timestamptz_ops", "timestamp with time zone", "0", 3127},
		{"timetz", "TIMETZ", "time with time zone", "timetz_ops", "time with time zone", "0", 10041},
		{"interval", "INTERVAL", "interval", "interval_ops", "interval", "0", 10022},
		{"numeric", "NUMERIC", "numeric", "numeric_ops", "numeric", "0", 3125},
		{"uuid", "UUID", "uuid", "uuid_ops", "uuid", "0", 10065},
		{"regclass", "REGCLASS", "regclass", "oid_ops", "oid", "0", 1981},
		{"domain_int4", "im_int_domain", "im_int_domain", "int4_ops", "integer", "0", 1978},
		{"domain_varchar", "im_text_domain", "im_text_domain", "text_ops", "text", "100", 3126},
		{"enum", "im_enum", "im_enum", "enum_ops", "anyenum", "0", 10069},
		{"domain_enum", "im_enum_domain", "im_enum_domain", "enum_ops", "anyenum", "0", 10069},
	}
	indexMaintenanceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, q := range []string{"CREATE TABLE shared_provider(v INT)", "INSERT INTO shared_provider VALUES(2),(1),(NULL)", "CREATE INDEX shared_provider_idx ON shared_provider(v)", "REINDEX INDEX shared_provider_idx", "ALTER INDEX shared_provider_idx RENAME TO shared_provider_renamed", "REINDEX TABLE shared_provider", "DROP INDEX shared_provider_renamed"} {
			count := int64(0)
			if q == "INSERT INTO shared_provider VALUES(2),(1),(NULL)" {
				count = 3
			}
			run(fixture.Exec(q, count))
		}
		run(fixture.Select("SELECT v FROM shared_provider ORDER BY v NULLS LAST", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {nil}}))
		// This is an intentional PG catalog/provider control, not a discovery
		// filter: BindOrdering admits char and regclass, but native CREATE TABLE
		// excludes their stored declarations. All provider identities, including
		// these two and domain wrappers, have native central-provider units.
		if !b.PG {
			return
		}
		for _, q := range []string{"CREATE DOMAIN im_int_domain AS INT", "CREATE DOMAIN im_text_domain AS VARCHAR(4)", "CREATE TYPE im_enum AS ENUM('low','high')", "CREATE DOMAIN im_enum_domain AS im_enum"} {
			run(fixture.Exec(q, 0))
		}
		var indexRows, classRows [][]any
		seen := map[string]bool{}
		for _, provider := range providers {
			run(fixture.Exec(fmt.Sprintf("CREATE TABLE provider_%s(v %s)", provider.name, provider.typ), 0))
			run(fixture.Exec(fmt.Sprintf("CREATE INDEX provider_%s_idx ON provider_%s(v)", provider.name, provider.name), 0))
			run(fixture.Exec(fmt.Sprintf("CREATE DOMAIN im_domain_%s AS %s", provider.name, provider.typ), 0))
			run(fixture.Exec(fmt.Sprintf("CREATE TABLE provider_wrapped_%s(v im_domain_%s)", provider.name, provider.name), 0))
			run(fixture.Exec(fmt.Sprintf("CREATE INDEX provider_wrapped_%s_idx ON provider_wrapped_%s(v)", provider.name, provider.name), 0))
			class := fmt.Sprint(provider.oid)
			indexRows = append(indexRows, []any{"provider_" + provider.name + "_idx", provider.declared, class, provider.coll, "1", "0", provider.oid, "pg_catalog", provider.className, provider.input, true})
			indexRows = append(indexRows, []any{"provider_wrapped_" + provider.name + "_idx", "im_domain_" + provider.name, class, provider.coll, "1", "0", provider.oid, "pg_catalog", provider.className, provider.input, true})
			if !seen[provider.className] {
				classRows = append(classRows, []any{"pg_catalog", provider.className, provider.oid, provider.input, true})
				seen[provider.className] = true
			}
		}
		if b.PG {
			s := fixture.Select(`SELECT c.relname,a.atttypid::regtype::text AS declared_type,i.indclass::text,i.indcollation::text,i.indkey::text,i.indoption::text,o.oid::bigint AS class_oid,n.nspname AS class_schema,o.opcname,o.opcintype::regtype::text AS provider_type,o.opcdefault FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_attribute a ON a.attrelid=i.indrelid AND a.attnum=1 JOIN pg_opclass o ON o.oid=i.indclass[0] JOIN pg_namespace n ON n.oid=o.opcnamespace JOIN pg_am am ON am.oid=o.opcmethod WHERE c.relname LIKE 'provider_%_idx' AND am.amname='btree'`, []string{"relname", "declared_type", "indclass", "indcollation", "indkey", "indoption", "class_oid", "class_schema", "opcname", "provider_type", "opcdefault"}, []string{"NAME", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT8", "NAME", "NAME", "TEXT", "BOOL"}, indexRows)
			s.Bag = true
			run(s)
			s = fixture.Select(`SELECT n.nspname,o.opcname,o.oid::bigint,o.opcintype::regtype::text,o.opcdefault FROM pg_opclass o JOIN pg_namespace n ON n.oid=o.opcnamespace JOIN pg_am am ON am.oid=o.opcmethod WHERE am.amname='btree' AND o.oid IN (SELECT i.indclass[0] FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE c.relname LIKE 'provider_%_idx')`, []string{"nspname", "opcname", "oid", "opcintype", "opcdefault"}, []string{"NAME", "NAME", "INT8", "TEXT", "BOOL"}, classRows)
			s.Bag = true
			run(s)
		}
	})
}
