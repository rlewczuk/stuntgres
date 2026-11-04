package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Active expectations follow REL_18_6 relation.c/schema.c and the dependency
// owners; the ordinary phase is the real inactive pinned PostgreSQL oracle.
func TestReferenceSepgsqlSimulationRetirement(t *testing.T) {
	sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("CREATE TABLE retirement_parent(id serial PRIMARY KEY,v text)", 0))
		run(fixture.Exec("INSERT INTO retirement_parent(v) VALUES('kept')", 1))
		run(fixture.Exec("CREATE ROLE retirement_guest", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO retirement_guest", 0))
		run(fixture.Exec("SET ROLE retirement_guest", 0))
		run(fixture.ImplicitError("DROP TABLE retirement_parent", fixture.Diagnostic{Code: "42501", Message: "must be owner of table retirement_parent"}))
		run(fixture.Prepared(fixture.ImplicitError("DROP TABLE retirement_parent", fixture.Diagnostic{Code: "42501", Message: "must be owner of table retirement_parent"})))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("CREATE VIEW retirement_view AS SELECT v FROM retirement_parent", 0))
		run(fixture.ImplicitError("DROP TABLE retirement_parent", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table retirement_parent because other objects depend on it", Detail: "view retirement_view depends on table retirement_parent", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
		run(sepgsqlText("SELECT v FROM retirement_parent", "v", "kept"))
		run(fixture.Exec("DROP TABLE retirement_parent CASCADE", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('retirement_parent','retirement_parent_id_seq','retirement_parent_pkey','retirement_view')", 0))
		run(fixture.Exec("CREATE TABLE retirement_root(k integer PRIMARY KEY) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE retirement_leaf PARTITION OF retirement_root DEFAULT", 0))
		run(fixture.Exec("INSERT INTO retirement_root VALUES(1)", 1))
		run(fixture.ImplicitError("ALTER TABLE retirement_leaf DROP CONSTRAINT retirement_leaf_pkey", fixture.Diagnostic{Code: "42P16", Message: "cannot drop inherited constraint \"retirement_leaf_pkey\" of relation \"retirement_leaf\""}))
		run(fixture.Exec("ALTER TABLE retirement_root DROP CONSTRAINT IF EXISTS retirement_missing", 0))
		run(fixture.Exec("ALTER TABLE retirement_root DROP CONSTRAINT retirement_root_pkey", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_constraint WHERE conrelid IN ('retirement_root'::regclass,'retirement_leaf'::regclass) AND contype='p'", 0))
		run(fixture.Select("SELECT k FROM retirement_root", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(sepgsqlCount("SELECT count(*) FROM pg_attribute WHERE attrelid IN ('retirement_root'::regclass,'retirement_leaf'::regclass) AND attname='marker'", 0))
		run(fixture.Exec("DROP TABLE retirement_root,retirement_leaf CASCADE", 0))
		run(fixture.Exec("CREATE TABLE retirement_atomic(k integer PRIMARY KEY,blocked text)", 0))
		run(fixture.Exec("INSERT INTO retirement_atomic VALUES(1,'kept')", 1))
		run(fixture.Exec("ALTER TABLE retirement_atomic DROP CONSTRAINT retirement_atomic_pkey, ADD COLUMN marker integer, DROP COLUMN blocked", 0))
		sepgsqlRetirementCompositeResult(run)
		run(fixture.Exec("DROP TABLE retirement_atomic", 0))
		run(fixture.Exec("DROP TABLE IF EXISTS retirement_absent", 0))
		run(fixture.Exec("CREATE ROLE retirement_owner", 0))
		run(fixture.Exec("CREATE TABLE retirement_owned(id serial)", 0))
		run(fixture.Exec("ALTER TABLE retirement_owned OWNER TO retirement_owner", 0))
		run(fixture.Exec("DROP OWNED BY retirement_owner", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('retirement_owned','retirement_owned_id_seq')", 0))
	}, func(config *stuntgres.SepgsqlSimulationConfig) {
		config.Policy.Rules = append(config.Policy.Rules,
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_database", Allow: []string{"getattr"}},
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_schema", Allow: []string{"remove_name", "add_name"}},
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_table", Allow: []string{"drop", "setattr", "create"}},
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_column", Allow: []string{"drop", "setattr", "create"}},
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_sequence", Allow: []string{"drop", "setattr", "get_value"}},
			stuntgres.SepgsqlRule{Source: sepgsqlR, Target: sepgsqlP, Class: "db_view", Allow: []string{"drop"}},
		)
	}, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, c *sql.Conn, run sepgsqlRun) {
		run(fixture.Exec("CREATE TABLE retirement_parent(id serial PRIMARY KEY,v text)", 0))
		run(fixture.Exec("INSERT INTO retirement_parent(v) VALUES('kept')", 1))
		run(fixture.Exec("SECURITY LABEL ON SEQUENCE retirement_parent_id_seq IS '"+sepgsqlS+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(fixture.Exec("CREATE ROLE retirement_guest", 0))
		run(fixture.Exec("SET ROLE retirement_guest", 0))
		run(fixture.ImplicitError("DROP TABLE retirement_parent", fixture.Diagnostic{Code: "42501", Message: "must be owner of table retirement_parent"}))
		run(fixture.Prepared(fixture.ImplicitError("DROP TABLE retirement_parent", fixture.Diagnostic{Code: "42501", Message: "must be owner of table retirement_parent"})))
		run(fixture.Exec("RESET ROLE", 0))
		run(sepgsqlDenied("DROP TABLE retirement_parent"))
		run(fixture.Prepared(sepgsqlDenied("DROP TABLE retirement_parent")))
		run(sepgsqlText("SELECT v FROM retirement_parent", "v", "kept"))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('retirement_parent','retirement_parent_id_seq','retirement_parent_pkey')", 3))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='retirement_parent_id_seq'::regclass AND label='"+sepgsqlS+"'", 1))
		run(fixture.Exec("SECURITY LABEL ON SEQUENCE retirement_parent_id_seq IS '"+sepgsqlP+"'", 0))
		run(fixture.Exec("SECURITY LABEL ON COLUMN retirement_parent.v IS '"+sepgsqlS+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlDenied("DROP TABLE retirement_parent"))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("SECURITY LABEL ON COLUMN retirement_parent.v IS '"+sepgsqlP+"'", 0))
		run(fixture.Exec("CREATE VIEW retirement_view AS SELECT v FROM retirement_parent", 0))
		run(fixture.Exec("SECURITY LABEL ON TABLE retirement_parent IS '"+sepgsqlS+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		// Ordinary RESTRICT is complete before the otherwise-denied MAC cohort.
		run(fixture.ImplicitError("DROP TABLE retirement_parent", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop table retirement_parent because other objects depend on it", Detail: "view retirement_view depends on table retirement_parent", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(fixture.Exec("SECURITY LABEL ON TABLE retirement_parent IS '"+sepgsqlP+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(fixture.Exec("DROP TABLE retirement_parent CASCADE", 0))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('retirement_parent','retirement_parent_id_seq','retirement_parent_pkey','retirement_view')", 0))
		run(fixture.Exec("CREATE TABLE retirement_root(k integer PRIMARY KEY) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE retirement_leaf PARTITION OF retirement_root DEFAULT", 0))
		run(fixture.Exec("INSERT INTO retirement_root VALUES(1)", 1))
		run(fixture.Exec("SECURITY LABEL ON TABLE retirement_leaf IS '"+sepgsqlS+"'", 0))
		// Capture genuine identities and all labels before the descendant index
		// projects its late DROP check onto the secret leaf table.
		hierarchyUnchanged := sepgsqlRetirementUnchanged(t, p, c,
			"SELECT * FROM pg_class WHERE relname IN ('retirement_root','retirement_leaf','retirement_root_pkey','retirement_leaf_pkey') ORDER BY relname",
			"SELECT * FROM pg_constraint WHERE conrelid IN ('retirement_root'::regclass,'retirement_leaf'::regclass) ORDER BY oid",
			"SELECT * FROM pg_index WHERE indrelid IN ('retirement_root'::regclass,'retirement_leaf'::regclass) ORDER BY indexrelid",
			"SELECT * FROM pg_attribute WHERE attrelid IN ('retirement_root'::regclass,'retirement_leaf'::regclass) ORDER BY attrelid,attnum",
			"SELECT * FROM pg_seclabel WHERE objoid IN ('retirement_root'::regclass,'retirement_leaf'::regclass,'retirement_root_pkey'::regclass,'retirement_leaf_pkey'::regclass) ORDER BY objoid,classoid,objsubid,provider",
		)
		run(sepgsqlText("SELECT label FROM pg_seclabel WHERE objoid='retirement_leaf'::regclass AND objsubid=0 AND provider='selinux'", "label", sepgsqlS))
		run(sepgsqlText("SELECT label FROM pg_seclabel WHERE objoid='retirement_root'::regclass AND objsubid=0 AND provider='selinux'", "label", sepgsqlP))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid IN ('retirement_root_pkey'::regclass,'retirement_leaf_pkey'::regclass)", 0))
		run(sepgsqlSet(sepgsqlR))
		run(fixture.ImplicitError("ALTER TABLE retirement_leaf DROP CONSTRAINT retirement_leaf_pkey", fixture.Diagnostic{Code: "42P16", Message: "cannot drop inherited constraint \"retirement_leaf_pkey\" of relation \"retirement_leaf\""}))
		run(fixture.Exec("ALTER TABLE retirement_root DROP CONSTRAINT IF EXISTS retirement_missing", 0))
		run(fixture.Exec("DROP TABLE IF EXISTS retirement_absent", 0))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlDenied("ALTER TABLE retirement_root DROP CONSTRAINT retirement_root_pkey"))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		hierarchyUnchanged()
		run(fixture.Select("SELECT k FROM retirement_root", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Select("SELECT k FROM retirement_leaf", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(sepgsqlCount("SELECT count(*) FROM pg_constraint WHERE conrelid IN ('retirement_root'::regclass,'retirement_leaf'::regclass) AND contype='p'", 2))
		run(sepgsqlCount("SELECT count(*) FROM pg_attribute WHERE attrelid IN ('retirement_root'::regclass,'retirement_leaf'::regclass) AND attname='marker'", 0))
		run(fixture.Exec("SECURITY LABEL ON TABLE retirement_leaf IS '"+sepgsqlP+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(fixture.Exec("ALTER TABLE retirement_root DROP CONSTRAINT retirement_root_pkey", 0))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(sepgsqlCount("SELECT count(*) FROM pg_constraint WHERE conrelid IN ('retirement_root'::regclass,'retirement_leaf'::regclass) AND contype='p'", 0))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('retirement_root_pkey','retirement_leaf_pkey')", 0))
		run(fixture.Select("SELECT k FROM retirement_root", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Exec("SECURITY LABEL ON TABLE retirement_leaf IS '"+sepgsqlS+"'", 0))
		multiRootUnchanged := sepgsqlRetirementUnchanged(t, p, c,
			"SELECT * FROM pg_class WHERE relname IN ('retirement_root','retirement_leaf') ORDER BY relname",
			"SELECT * FROM pg_attribute WHERE attrelid IN ('retirement_root'::regclass,'retirement_leaf'::regclass) ORDER BY attrelid,attnum",
			"SELECT * FROM pg_seclabel WHERE objoid IN ('retirement_root'::regclass,'retirement_leaf'::regclass) ORDER BY objoid,classoid,objsubid,provider",
		)
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlDenied("DROP TABLE retirement_root,retirement_leaf CASCADE"))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		multiRootUnchanged()
		run(fixture.Select("SELECT k FROM retirement_root", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Select("SELECT k FROM retirement_leaf", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('retirement_root','retirement_leaf')", 2))
		run(fixture.Exec("SECURITY LABEL ON TABLE retirement_leaf IS '"+sepgsqlP+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(fixture.Exec("DROP TABLE retirement_root,retirement_leaf CASCADE", 0))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('retirement_root','retirement_leaf')", 0))
		// The supported ordinary-table path separately proves a late column
		// denial rolls back earlier constraint retirement and column creation.
		run(fixture.Exec("CREATE TABLE retirement_atomic(k integer PRIMARY KEY,blocked text)", 0))
		run(fixture.Exec("INSERT INTO retirement_atomic VALUES(1,'kept')", 1))
		run(fixture.Exec("SECURITY LABEL ON COLUMN retirement_atomic.blocked IS '"+sepgsqlS+"'", 0))
		atomicUnchanged := sepgsqlRetirementUnchanged(t, p, c,
			"SELECT * FROM pg_class WHERE relname IN ('retirement_atomic','retirement_atomic_pkey') ORDER BY relname",
			"SELECT * FROM pg_constraint WHERE conrelid='retirement_atomic'::regclass ORDER BY oid",
			"SELECT * FROM pg_index WHERE indrelid='retirement_atomic'::regclass ORDER BY indexrelid",
			"SELECT * FROM pg_attribute WHERE attrelid='retirement_atomic'::regclass ORDER BY attnum",
			"SELECT * FROM pg_seclabel WHERE objoid IN ('retirement_atomic'::regclass,'retirement_atomic_pkey'::regclass) ORDER BY objoid,classoid,objsubid,provider",
		)
		run(sepgsqlText("SELECT label FROM pg_seclabel WHERE objoid='retirement_atomic'::regclass AND objsubid=2 AND provider='selinux'", "label", sepgsqlS))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='retirement_atomic_pkey'::regclass", 0))
		const composite = "ALTER TABLE retirement_atomic DROP CONSTRAINT retirement_atomic_pkey, ADD COLUMN marker integer, DROP COLUMN blocked"
		for _, prepared := range []bool{false, true} {
			run(sepgsqlSet(sepgsqlR))
			denied := sepgsqlDenied(composite)
			if prepared {
				denied = fixture.Prepared(denied)
			}
			run(denied)
			run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
			atomicUnchanged()
			run(sepgsqlCount("SELECT count(*) FROM pg_constraint WHERE conrelid='retirement_atomic'::regclass AND contype='p'", 1))
			run(sepgsqlCount("SELECT count(*) FROM pg_attribute WHERE attrelid='retirement_atomic'::regclass AND attname='marker'", 0))
			run(sepgsqlCount("SELECT count(*) FROM pg_attribute WHERE attrelid='retirement_atomic'::regclass AND attname='blocked' AND NOT attisdropped", 1))
			row := fixture.Select("SELECT k,blocked FROM retirement_atomic", []string{"k", "blocked"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "kept"}})
			run(row)
			run(fixture.Prepared(row))
		}
		run(fixture.Exec("SECURITY LABEL ON COLUMN retirement_atomic.blocked IS '"+sepgsqlP+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(fixture.Exec(composite, 0))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		sepgsqlRetirementCompositeResult(run)
		run(sepgsqlText("SELECT label FROM pg_seclabel WHERE objoid='retirement_atomic'::regclass AND objsubid=3 AND provider='selinux'", "label", sepgsqlP))
		run(sepgsqlCount("SELECT count(*) FROM pg_seclabel WHERE objoid='retirement_atomic'::regclass AND objsubid=2", 0))
		run(fixture.Exec("DROP TABLE retirement_atomic", 0))
		run(fixture.Exec("CREATE ROLE retirement_owner", 0))
		run(fixture.Exec("CREATE TABLE retirement_owned(id serial)", 0))
		run(fixture.Exec("ALTER TABLE retirement_owned OWNER TO retirement_owner", 0))
		run(fixture.Exec("SECURITY LABEL ON SEQUENCE retirement_owned_id_seq IS '"+sepgsqlS+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(sepgsqlDenied("DROP OWNED BY retirement_owner"))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('retirement_owned','retirement_owned_id_seq')", 2))
		run(fixture.Exec("SECURITY LABEL ON SEQUENCE retirement_owned_id_seq IS '"+sepgsqlP+"'", 0))
		run(sepgsqlSet(sepgsqlR))
		run(fixture.Exec("DROP OWNED BY retirement_owner", 0))
		run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true))
		run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname IN ('retirement_owned','retirement_owned_id_seq')", 0))
	})
}

// Generated identities are compared with their own genuine predecessors, not
// another backend's allocator; independent expected rows/counts remain explicit.
func sepgsqlRetirementUnchanged(t *testing.T, p *fixture.Pair, c *sql.Conn, queries ...string) func() {
	t.Helper()
	before := make([][]map[string]any, len(queries))
	for i, query := range queries {
		before[i] = fixture.DefinitionIdentityRows(t, p.Ctx, c, query)
	}
	return func() {
		t.Helper()
		for i, query := range queries {
			fixture.DefinitionIdentityEqual(t, fixture.DefinitionIdentityRows(t, p.Ctx, c, query), before[i])
		}
	}
}

func sepgsqlRetirementCompositeResult(run sepgsqlRun) {
	run(sepgsqlCount("SELECT count(*) FROM pg_constraint WHERE conrelid='retirement_atomic'::regclass AND contype='p'", 0))
	run(sepgsqlCount("SELECT count(*) FROM pg_class WHERE relname='retirement_atomic_pkey'", 0))
	run(sepgsqlCount("SELECT count(*) FROM pg_attribute WHERE attrelid='retirement_atomic'::regclass AND attname='marker' AND NOT attisdropped", 1))
	run(sepgsqlCount("SELECT count(*) FROM pg_attribute WHERE attrelid='retirement_atomic'::regclass AND attname='blocked' AND NOT attisdropped", 0))
	attributes := fixture.Select("SELECT attname,attnum,atttypid::bigint,attnotnull FROM pg_attribute WHERE attrelid='retirement_atomic'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum", "atttypid", "attnotnull"}, []string{"NAME", "INT2", "INT8", "BOOL"}, [][]any{{"k", int64(1), int64(23), true}, {"marker", int64(3), int64(23), false}})
	run(attributes)
	run(fixture.Prepared(attributes))
	row := fixture.Select("SELECT k,marker FROM retirement_atomic", []string{"k", "marker"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}})
	run(row)
	run(fixture.Prepared(row))
}
