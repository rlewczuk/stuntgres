package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFTSConfigurationOwnership(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"config_owner", "config_inherited", "config_recipient", "config_denied"})
	fixture.SchemaReferenceCleanup(t, "config_owner_schema")
	fixture.SchemaReferenceCleanup(t, "config_owner_dest")
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, sql := range []string{"CREATE ROLE config_owner", "CREATE ROLE config_inherited", "CREATE ROLE config_recipient", "CREATE ROLE config_denied", "GRANT config_owner TO config_inherited", "CREATE SCHEMA config_owner_schema AUTHORIZATION config_owner", "CREATE SCHEMA config_owner_dest AUTHORIZATION config_owner", "GRANT USAGE ON SCHEMA config_owner_schema TO PUBLIC", "SET ROLE config_owner", "CREATE TEXT SEARCH CONFIGURATION config_owner_schema.owned (COPY=pg_catalog.simple)"} {
			run(fixture.Exec(sql, 0))
		}
		run(fixture.Select("SELECT cfgowner='config_owner'::regrole::oid AS owned FROM pg_ts_config WHERE oid='config_owner_schema.owned'::regconfig", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET ROLE config_denied", 0))
		for _, prepared := range []bool{false, true} {
			for _, sql := range []string{"ALTER TEXT SEARCH CONFIGURATION config_owner_schema.owned DROP MAPPING FOR asciiword", "DROP TEXT SEARCH CONFIGURATION config_owner_schema.owned"} {
				s := ftsConfigurationError(sql, "42501", "must be owner of text search configuration config_owner_schema.owned")
				s.Prepared = prepared
				run(s)
			}
		}
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET ROLE config_inherited", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION config_owner_schema.owned DROP MAPPING FOR asciiword", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION config_owner_schema.owned ADD MAPPING FOR asciiword WITH simple", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION config_owner_schema.owned RENAME TO renamed", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION config_owner_schema.renamed SET SCHEMA config_owner_dest", 0))
		run(ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION config_owner_dest.renamed OWNER TO config_recipient", "42501", `must be able to SET ROLE "config_recipient"`))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT config_recipient TO config_owner", 0))
		run(fixture.Exec("GRANT CREATE ON SCHEMA config_owner_dest TO config_recipient", 0))
		run(fixture.Exec("SET ROLE config_owner", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION config_owner_dest.renamed OWNER TO config_recipient", 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Select("SELECT cfgowner='config_recipient'::regrole::oid AS owned FROM pg_ts_config WHERE oid='config_owner_dest.renamed'::regconfig", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("REASSIGN OWNED BY config_recipient TO config_owner", 0))
		run(fixture.Select("SELECT cfgowner='config_owner'::regrole::oid AS owned FROM pg_ts_config WHERE oid='config_owner_dest.renamed'::regconfig", []string{"owned"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("DROP OWNED BY config_owner CASCADE", 0))
		run(fixture.Select("SELECT cfgname FROM pg_ts_config WHERE cfgname='renamed'", []string{"cfgname"}, []string{"NAME"}, nil))
	})
}
func TestReferenceFTSConfigurationDependencies(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, sql := range []string{"CREATE TEXT SEARCH DICTIONARY public.config_shared (TEMPLATE=pg_catalog.simple)", "CREATE TEXT SEARCH CONFIGURATION public.config_dep (PARSER=pg_catalog.default)", "ALTER TEXT SEARCH CONFIGURATION public.config_dep ADD MAPPING FOR asciiword,numword WITH public.config_shared,public.config_shared", "CREATE TEXT SEARCH CONFIGURATION public.config_dep_copy (COPY=public.config_dep)"} {
			run(fixture.Exec(sql, 0))
		}
		run(fixture.Select("SELECT count(*) AS namespaces FROM pg_depend WHERE classid=3602 AND objid='public.config_dep'::regconfig AND refclassid=2615 AND refobjid='public'::regnamespace", []string{"namespaces"}, []string{"INT8"}, [][]any{{int64(1)}}))
		// Use identities to assert the exact edge relationships without allocator goldens.
		run(fixture.Select("SELECT count(*) AS dictionaries FROM pg_depend WHERE classid=3602 AND objid='public.config_dep'::regconfig AND refclassid=3600 AND refobjid='public.config_shared'::regdictionary", []string{"dictionaries"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Select("SELECT count(*) AS parsers FROM pg_depend WHERE classid=3602 AND objid='public.config_dep'::regconfig AND refclassid=3601 AND refobjid=3722", []string{"parsers"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("DROP TEXT SEARCH CONFIGURATION public.config_dep", 0))
		query := "SELECT to_tsvector('public.config_dep_copy'::regconfig,$1::text) AS normalized"
		run(ftsConfigurationVector(query, "'rats':1", "Rats"))
		run(fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.config_shared (Accept=false)", 0))
		run(ftsConfigurationVector(query, "", "Rats"))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_dep_copy DROP MAPPING FOR asciiword", 0))
		run(fixture.Select("SELECT count(*) AS dictionaries FROM pg_depend WHERE classid=3602 AND objid='public.config_dep_copy'::regconfig AND refclassid=3600 AND refobjid='public.config_shared'::regdictionary", []string{"dictionaries"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_dep_copy DROP MAPPING FOR numword", 0))
		run(fixture.Exec("DROP TEXT SEARCH DICTIONARY public.config_shared RESTRICT", 0))
		run(fixture.Exec("CREATE TABLE config_consumer(v regconfig DEFAULT 'public.config_dep_copy'::regconfig)", 0))
		run(fixture.Exec("CREATE VIEW config_consumer_view AS SELECT 'public.config_dep_copy'::regconfig AS config", 0))
		run(fixture.Exec("DROP TEXT SEARCH CONFIGURATION public.config_dep_copy CASCADE", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='config_consumer_view'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("INSERT INTO config_consumer DEFAULT VALUES", 1))
		run(fixture.Select("SELECT v FROM config_consumer", []string{"v"}, []string{"REGCONFIG"}, [][]any{{nil}}))
	})
}

func TestReferenceFTSConfigurationFeatureSmoke(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_smoke (COPY=pg_catalog.simple)", 0))
		query := "SELECT to_tsvector('public.config_smoke'::regconfig,$1::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		check := func(want string) {
			s := ftsConfigurationVector(query, want, "The Fat Rats")
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		check("'fat':2 'rats':3 'the':1")
		for _, tc := range []struct{ input, want any }{{nil, nil}, {"", ""}} {
			s := fixture.Select(query, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.want}}, tc.input)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, b.PG) }
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_smoke DROP MAPPING FOR asciiword", 0))
		check("")
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_smoke ADD MAPPING FOR asciiword WITH pg_catalog.simple", 0))
		check("'fat':2 'rats':3 'the':1")
		run(ftsConfigurationError("ALTER TEXT SEARCH CONFIGURATION public.config_smoke ALTER MAPPING FOR asciiword,not_a_token WITH simple", "22023", `token type "not_a_token" does not exist`))
		check("'fat':2 'rats':3 'the':1")
		run(fixture.Exec("CREATE TEXT SEARCH CONFIGURATION public.config_smoke_copy (COPY=public.config_smoke)", 0))
		run(fixture.Exec("ALTER TEXT SEARCH CONFIGURATION public.config_smoke DROP MAPPING FOR asciiword", 0))
		check("")
		run(ftsConfigurationVector("SELECT to_tsvector('public.config_smoke_copy'::regconfig,$1::text) AS normalized", "'fat':2 'rats':3 'the':1", "The Fat Rats"))
		run(fixture.Exec("DROP TEXT SEARCH CONFIGURATION public.config_smoke", 0))
		run(ftsConfigurationMaps("public.config_smoke_copy", ftsConfigurationSimpleMaps()))
		run(fixture.Select("SELECT cfgparser::bigint,cfgowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS owned FROM pg_ts_config WHERE oid='public.config_smoke_copy'::regconfig", []string{"cfgparser", "owned"}, []string{"INT8", "BOOL"}, [][]any{{int64(3722), true}}))
		run(fixture.Exec("CREATE TABLE config_smoke_values(v regconfig)", 0))
		run(fixture.Exec("INSERT INTO config_smoke_values VALUES('public.config_smoke_copy')", 1))
		if !b.PG {
			stmt.Close()
			step := ftsConfigurationVector("SELECT to_tsvector(v,'The Fat Rats') AS normalized FROM config_smoke_values", "'fat':2 'rats':3 'the':1")
			step.CheckMetadata = func(t *testing.T, r *sql.Rows) { configurationVectorMetadata(t, r, false) }
			rangeControlCycle(t, p, b, []fixture.Step{step}, [][]bool{nil}, []rangeStagedFailure{{"CREATE TEXT SEARCH CONFIGURATION public.config_bad_restore (COPY=simple); ALTER TEXT SEARCH CONFIGURATION public.config_bad_restore ADD MAPPING FOR not_a_token WITH simple;", fixture.ControlsReferenceDiagnostic("22023", `token type "not_a_token" does not exist`)}})
		}
	})
}
