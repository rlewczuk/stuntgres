package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Enum-element arrays are ordinary array descriptors whose element is a
// user-defined enum. Every expected value below is from the pinned PostgreSQL
// 18.6 capture in .cswd/tmp/GRIND-TYPE-ENUM-ARRAY/oracle-enum-array.txt.
// Scalar enum results use fixture.EnumReferenceRunStep; array results use the
// array-codec family with enumFunctionsTypeName's native/PG catalog names.

func enumArrayExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, count int64) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(query, count))
}

// enumArrayQuery runs an ordered select whose raw codec families are declared
// independently of the catalog type names.
func enumArrayQuery(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, names, types, raw []string, rows [][]any) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ArrayReferenceSelect(query, names, types, raw, rows, b.PG))
}

// enumArrayEnumQuery runs a select containing scalar enum columns; a "" type
// name marks an enum column for the enum raw transport check.
func enumArrayEnumQuery(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, names, types []string, rows [][]any) {
	t.Helper()
	fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, fixture.Select(query, names, types, rows))
}

func enumArrayText(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, rows [][]any) {
	t.Helper()
	enumArrayQuery(t, p, b, c, query, []string{"v"}, []string{"TEXT"}, nil, rows)
}

func enumArrayInt(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, rows [][]any) {
	t.Helper()
	enumArrayQuery(t, p, b, c, query, []string{"v"}, []string{"INT4"}, nil, rows)
}

func enumArrayBool(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, want bool) {
	t.Helper()
	enumArrayQuery(t, p, b, c, query, []string{"v"}, []string{"BOOL"}, nil, [][]any{{want}})
}

func enumArrayResult(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, rows [][]any) {
	t.Helper()
	enumArrayQuery(t, p, b, c, query, []string{"v"}, []string{enumFunctionsTypeName(b.PG, "_E")}, []string{"_ENUM"}, rows)
}

func enumArrayLabels(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, rows [][]any) {
	t.Helper()
	enumArrayEnumQuery(t, p, b, c, query, []string{"v"}, []string{""}, rows)
}

func TestReferenceEnumArrayAdmission(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		enumArrayExec(t, p, b, c, "CREATE TYPE e AS ENUM ('a','b')", 0)
		enumArrayExec(t, p, b, c, "CREATE TABLE t(id integer, x e[])", 0)
		enumArrayExec(t, p, b, c, "INSERT INTO t VALUES (1,'{a,b}'),(2,'{}'),(3,NULL),(4,'{b}')", 4)
		enumArrayQuery(t, p, b, c, "SELECT x::text AS v FROM t ORDER BY id", []string{"v"}, []string{"TEXT"}, nil,
			[][]any{{"{a,b}"}, {"{}"}, {nil}, {"{b}"}})
		enumArrayResult(t, p, b, c, "SELECT '{a,b}'::e[] AS v", [][]any{{"{a,b}"}})
		enumArrayResult(t, p, b, c, "SELECT ARRAY['a'::e,'b'::e] AS v", [][]any{{"{a,b}"}})
		enumArrayBool(t, p, b, c, "SELECT ARRAY['a'::e,'b'::e] = '{a,b}'::e[] AS v", true)
		enumArrayQuery(t, p, b, c, "SELECT ('{a,b}'::e[])::text[] AS v", []string{"v"}, []string{"_TEXT"}, []string{"_TEXT"},
			[][]any{{"{a,b}"}})
		enumArrayResult(t, p, b, c, "SELECT ('{a,b}'::text[])::e[] AS v", [][]any{{"{a,b}"}})
		enumArrayResult(t, p, b, c, "SELECT '[0:1]={a,b}'::e[] AS v", [][]any{{"[0:1]={a,b}"}})
		enumArrayText(t, p, b, c, "SELECT '{a,NULL}'::e[]::text AS v", [][]any{{"{a,NULL}"}})
		// The direct literal pins position 8; the parameter form reports the
		// same state at execute with the portal context.
		fixture.ArrayReferenceInputError(t, p, b, c, "{a,c}", "e[]", fixture.ControlsReferenceDiagnostic("22P02", `invalid input value for enum e: "c"`))
		// The cast routine reports the same state with no position at all.
		cast := fixture.ImplicitError("SELECT ('{a,c}'::text[])::e[] AS v", fixture.Diagnostic{Code: "22P02", Message: `invalid input value for enum e: "c"`})
		cast.SelectRows = true
		fixture.RunStep(t, p.Ctx, c, b.Recorder, cast)
	})
}

func TestReferenceEnumArrayOperations(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		enumArrayExec(t, p, b, c, "CREATE TYPE e AS ENUM ('a','b')", 0)
		enumArrayBool(t, p, b, c, "SELECT 'a'::e = ANY (ARRAY['b'::e,'a'::e]) AS v", true)
		enumArrayBool(t, p, b, c, "SELECT 'a'::e = ALL (ARRAY['a'::e,'a'::e]) AS v", true)
		enumArrayBool(t, p, b, c, "SELECT '{a,b}'::e[] = '{a,b}'::e[] AS v", true)
		enumArrayBool(t, p, b, c, "SELECT '{a}'::e[] < '{b}'::e[] AS v", true)
		enumArrayText(t, p, b, c, "SELECT array_to_string(ARRAY['a'::e,'b'::e], '|') AS v", [][]any{{"a|b"}})
		enumArrayInt(t, p, b, c, "SELECT cardinality('{a,b}'::e[]) AS v", [][]any{{int64(2)}})
		enumArrayInt(t, p, b, c, "SELECT array_length('{a,b}'::e[],1) AS v", [][]any{{int64(2)}})
		enumArrayText(t, p, b, c, "SELECT array_dims('{a,b}'::e[]) AS v", [][]any{{"[1:2]"}})
		enumArrayText(t, p, b, c, "SELECT array_dims('{{a,b},{b,a}}'::e[]) AS v", [][]any{{"[1:2][1:2]"}})
		enumArrayLabels(t, p, b, c, "SELECT ('{a,b}'::e[])[2] AS v", [][]any{{"b"}})
		enumArrayText(t, p, b, c, "SELECT pg_typeof(('{a}'::e[])[1])::text AS v", [][]any{{"e"}})
		enumArrayResult(t, p, b, c, "SELECT ('{a,b}'::e[])[1:1] AS v", [][]any{{"{a}"}})
		enumArrayResult(t, p, b, c, "SELECT ('{a,b}'::e[])[1:] AS v", [][]any{{"{a,b}"}})
		enumArrayResult(t, p, b, c, "SELECT ('[0:1]={a,b}'::e[])[0:0] AS v", [][]any{{"{a}"}})
		enumArrayResult(t, p, b, c, "SELECT '{a,b}'::e[] || '{b}'::e[] AS v", [][]any{{"{a,b,b}"}})
		enumArrayInt(t, p, b, c, "SELECT array_position('{a,b}'::e[], 'b'::e) AS v", [][]any{{int64(2)}})
		enumArrayText(t, p, b, c, "SELECT array_remove('{a,b,a}'::e[], 'a'::e)::text AS v", [][]any{{"{b}"}})
		enumArrayText(t, p, b, c, "SELECT array_replace('{a,b}'::e[], 'a'::e, 'b'::e)::text AS v", [][]any{{"{b,b}"}})
		enumArrayText(t, p, b, c, "SELECT array_prepend('b'::e, '{a}'::e[])::text AS v", [][]any{{"{b,a}"}})
		enumArrayText(t, p, b, c, "SELECT array_append('{a}'::e[], 'b'::e)::text AS v", [][]any{{"{a,b}"}})
		enumArrayInt(t, p, b, c, "SELECT generate_subscripts('{a,b}'::e[],1) AS v ORDER BY v", [][]any{{int64(1)}, {int64(2)}})
		enumArrayEnumQuery(t, p, b, c, "SELECT v, o FROM unnest('{b,a}'::e[]) WITH ORDINALITY AS u(v,o) ORDER BY o",
			[]string{"v", "o"}, []string{"", "INT8"}, [][]any{{"b", int64(1)}, {"a", int64(2)}})
		enumArrayExec(t, p, b, c, "CREATE TABLE agg_src(v e)", 0)
		enumArrayExec(t, p, b, c, "INSERT INTO agg_src VALUES ('a'),('b')", 2)
		enumArrayResult(t, p, b, c, "SELECT array_agg(v ORDER BY v) AS v FROM agg_src", [][]any{{"{a,b}"}})
		enumArrayText(t, p, b, c, "SELECT pg_typeof(array_agg(v ORDER BY v))::text AS v FROM agg_src", [][]any{{"e[]"}})
		enumArrayExec(t, p, b, c, "CREATE TABLE agg_ctas AS SELECT array_agg(v ORDER BY v) AS v FROM agg_src", 1)
		enumArrayText(t, p, b, c, "SELECT v::text AS v FROM agg_ctas", [][]any{{"{a,b}"}})
		enumArrayText(t, p, b, c, "SELECT format_type(atttypid,atttypmod) AS v FROM pg_attribute WHERE attrelid='agg_ctas'::regclass AND attname='v'", [][]any{{"e[]"}})
	})
}

func TestReferenceEnumArrayDDL(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		enumArrayExec(t, p, b, c, "CREATE TYPE e AS ENUM ('a','b')", 0)
		enumArrayExec(t, p, b, c, "CREATE TABLE alt(id int)", 0)
		enumArrayExec(t, p, b, c, "ALTER TABLE alt ADD COLUMN x e[]", 0)
		enumArrayExec(t, p, b, c, "ALTER TABLE alt ALTER COLUMN x TYPE e[]", 0)
		enumArrayExec(t, p, b, c, "ALTER TABLE alt ALTER COLUMN x TYPE text[]", 0)
		enumArrayText(t, p, b, c, "SELECT format_type(atttypid,atttypmod) AS v FROM pg_attribute WHERE attrelid='alt'::regclass AND attname='x'", [][]any{{"text[]"}})
		enumArrayExec(t, p, b, c, "CREATE DOMAIN ed AS e", 0)
		enumArrayExec(t, p, b, c, "CREATE DOMAIN ea AS e[]", 0)
		enumArrayExec(t, p, b, c, "CREATE TABLE domt(a ed[], b ea)", 0)
		enumArrayExec(t, p, b, c, "INSERT INTO domt VALUES ('{a,b}','{a,b}')", 1)
		enumArrayQuery(t, p, b, c, "SELECT a::text AS a,b::text AS b FROM domt", []string{"a", "b"}, []string{"TEXT", "TEXT"}, nil,
			[][]any{{"{a,b}", "{a,b}"}})
		enumArrayQuery(t, p, b, c, "SELECT attname,format_type(atttypid,atttypmod) AS format_type FROM pg_attribute WHERE attrelid='domt'::regclass AND attnum>0 ORDER BY attnum",
			[]string{"attname", "format_type"}, []string{"NAME", "TEXT"}, nil,
			[][]any{{"a", "ed[]"}, {"b", "ea"}})
		enumArrayExec(t, p, b, c, "CREATE TABLE multi(x e[][])", 0)
		enumArrayQuery(t, p, b, c, "SELECT attndims,format_type(atttypid,atttypmod) AS format_type FROM pg_attribute WHERE attrelid='multi'::regclass AND attname='x'",
			[]string{"attndims", "format_type"}, []string{"INT2", "TEXT"}, nil,
			[][]any{{int64(2), "e[]"}})
		enumArrayResult(t, p, b, c, "SELECT '{{a,b},{b,a}}'::e[] AS v", [][]any{{"{{a,b},{b,a}}"}})
		enumArrayResult(t, p, b, c, "SELECT ARRAY[ARRAY['a'::e],ARRAY['b'::e]] AS v", [][]any{{"{{a},{b}}"}})
		enumArrayText(t, p, b, c, "SELECT '{a}'::_e::text AS v", [][]any{{"{a}"}})
	})
}

func TestReferenceEnumArrayMetadata(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		enumArrayExec(t, p, b, c, "CREATE TYPE e AS ENUM ('a','b')", 0)
		enumArrayExec(t, p, b, c, "CREATE TABLE t(x e[])", 0)
		enumArrayQuery(t, p, b, c, "SELECT attname,atttypid::regtype::text AS atttypid,attnum,atttypmod,attndims,format_type(atttypid,atttypmod) AS format_type FROM pg_attribute WHERE attrelid='t'::regclass AND attname='x'",
			[]string{"attname", "atttypid", "attnum", "atttypmod", "attndims", "format_type"},
			[]string{"NAME", "TEXT", "INT2", "INT4", "INT2", "TEXT"}, nil,
			[][]any{{"x", "e[]", int64(1), int64(-1), int64(1), "e[]"}})
		enumArrayQuery(t, p, b, c, "SELECT typname,typtype,typcategory,CASE WHEN typelem=0 THEN '-' ELSE typelem::regtype::text END AS typelem,CASE WHEN typarray=0 THEN '-' ELSE typarray::regtype::text END AS typarray,typlen,typalign,typstorage,typdelim FROM pg_type WHERE typname IN ('e','_e') ORDER BY typname",
			[]string{"typname", "typtype", "typcategory", "typelem", "typarray", "typlen", "typalign", "typstorage", "typdelim"},
			[]string{"NAME", "CHAR", "CHAR", "TEXT", "TEXT", "INT2", "CHAR", "CHAR", "CHAR"}, nil,
			[][]any{
				{"_e", "b", "A", "e", "-", int64(-1), "i", "x", ","},
				{"e", "e", "E", "-", "e[]", int64(4), "i", "p", ","},
			})
	})
}

func TestReferenceEnumArrayPrepared(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		enumArrayExec(t, p, b, c, "CREATE TYPE e AS ENUM ('a','b')", 0)
		enumArrayExec(t, p, b, c, "CREATE TABLE arrsrc(x e[])", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO arrsrc VALUES($1)", 1, "{a,b}")))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO arrsrc VALUES($1)", 1, nil)))
		enumArrayQuery(t, p, b, c, "SELECT x::text AS x FROM arrsrc ORDER BY x", []string{"x"}, []string{"TEXT"}, nil,
			[][]any{{"{a,b}"}, {nil}})
		s := fixture.ArrayReferenceSelect("SELECT $1::e[] AS v", []string{"v"}, []string{enumFunctionsTypeName(b.PG, "_E")}, []string{"_ENUM"}, [][]any{{"{a,b}"}}, b.PG, "{a,b}")
		s.Prepared = true
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		equal := fixture.Select("SELECT $1::e[] = '{a,b}'::e[] AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, "{a,b}")
		equal.Prepared = true
		fixture.RunStep(t, p.Ctx, c, b.Recorder, equal)
	})
}

func TestReferenceEnumArrayQuoting(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		enumArrayExec(t, p, b, c, `CREATE TYPE quote_e AS ENUM ('a b','a,b','a"b','NULL','a\b')`, 0)
		enumArrayText(t, p, b, c, `SELECT ARRAY['a b'::quote_e,'a,b'::quote_e,'a"b'::quote_e,'NULL'::quote_e,'a\b'::quote_e]::text AS v`,
			[][]any{{`{"a b","a,b","a\"b","NULL","a\\b"}`}})
		enumArrayText(t, p, b, c, `SELECT (ARRAY['a b'::quote_e,'a,b'::quote_e]::text)::quote_e[]::text AS v`,
			[][]any{{`{"a b","a,b"}`}})
		enumArrayText(t, p, b, c, `SELECT ARRAY['a b'::quote_e, NULL::quote_e]::text AS v`,
			[][]any{{`{"a b",NULL}`}})
		enumArrayText(t, p, b, c, `SELECT ARRAY['NULL'::quote_e,'a\b'::quote_e]::text AS v`,
			[][]any{{`{"NULL","a\\b"}`}})
		enumArrayBool(t, p, b, c, `SELECT (ARRAY['NULL'::quote_e,'a b'::quote_e]::text)::quote_e[] = ARRAY['NULL'::quote_e,'a b'::quote_e] AS v`, true)
	})
}

func TestReferenceEnumArrayDependencies(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		enumArrayExec(t, p, b, c, "CREATE TYPE drop_e AS ENUM ('a','b')", 0)
		enumArrayExec(t, p, b, c, "CREATE TABLE drop_t(x drop_e[])", 0)
		drop := fixture.ImplicitError("DROP TYPE drop_e", fixture.Diagnostic{
			Code:    "2BP01",
			Message: "cannot drop type drop_e because other objects depend on it",
			Detail:  "column x of table drop_t depends on type drop_e[]",
			Hint:    "Use DROP ... CASCADE to drop the dependent objects too.",
		})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, drop)
		enumArrayExec(t, p, b, c, "DROP TYPE drop_e CASCADE", 0)
		enumArrayQuery(t, p, b, c, "SELECT count(*) AS v FROM pg_attribute WHERE attrelid='drop_t'::regclass AND attname='x'",
			[]string{"v"}, []string{"INT8"}, nil, [][]any{{int64(0)}})
		for _, q := range []string{
			"CREATE TYPE drop_e2 AS ENUM ('a','b')",
			"CREATE DOMAIN drop_d AS drop_e2",
			"CREATE DOMAIN drop_da AS drop_e2[]",
			"CREATE TABLE drop_dt(a drop_d, b drop_da)",
		} {
			enumArrayExec(t, p, b, c, q, 0)
		}
		// Multi-dependent ordering is not asserted; the single-column case owns
		// the exact detail record above.
		enumArrayExec(t, p, b, c, "DROP TYPE drop_e2 CASCADE", 0)
		enumArrayQuery(t, p, b, c, "SELECT count(*) AS v FROM pg_type WHERE typname IN ('drop_e2','_drop_e2','drop_d','_drop_d','drop_da','_drop_da')",
			[]string{"v"}, []string{"INT8"}, nil, [][]any{{int64(0)}})
		enumArrayQuery(t, p, b, c, "SELECT count(*) AS v FROM pg_attribute WHERE attrelid='drop_dt'::regclass AND attnum>0 AND NOT attisdropped",
			[]string{"v"}, []string{"INT8"}, nil, [][]any{{int64(0)}})
	})
}

func TestReferenceEnumArrayMigrations(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE TYPE provisioner_type AS ENUM ('echo','terraform')",
			"CREATE TABLE provisioner_daemons_t(id integer, provisioners provisioner_type[] NOT NULL)",
			"CREATE TYPE display_app AS ENUM ('true','false')",
			"CREATE TABLE agent_default_apps(display_apps display_app[])",
			"CREATE TYPE workspace_agent_subsystem AS ENUM ('envbox','envbuilder')",
			"CREATE TABLE agent_subsystems(workspace_agent_subsystem workspace_agent_subsystem[])",
			"CREATE TYPE api_key_scope AS ENUM ('read','write')",
			"CREATE TABLE api_keys(id integer, scopes api_key_scope[])",
			"ALTER TABLE api_keys ADD COLUMN allow_list api_key_scope[]",
			"ALTER TABLE api_keys ALTER COLUMN scopes TYPE api_key_scope[]",
		} {
			enumArrayExec(t, p, b, c, q, 0)
		}
		enumArrayExec(t, p, b, c, "INSERT INTO provisioner_daemons_t VALUES (1,'{echo}')", 1)
		enumArrayText(t, p, b, c, "SELECT provisioners::text AS v FROM provisioner_daemons_t", [][]any{{"{echo}"}})
		enumArrayQuery(t, p, b, c, "SELECT '{echo}'::provisioner_type[] AS v", []string{"v"},
			[]string{enumFunctionsTypeName(b.PG, "_PROVISIONER_TYPE")}, []string{"_ENUM"}, [][]any{{"{echo}"}})
		enumArrayQuery(t, p, b, c, "SELECT ARRAY['echo'::provisioner_type] AS v", []string{"v"},
			[]string{enumFunctionsTypeName(b.PG, "_PROVISIONER_TYPE")}, []string{"_ENUM"}, [][]any{{"{echo}"}})
	})
}

func TestReferenceEnumArrayRoutines(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		enumArrayExec(t, p, b, c, "CREATE TYPE e AS ENUM ('a','b')", 0)
		enumArrayExec(t, p, b, c, "CREATE FUNCTION enum_first_of(e[]) RETURNS e LANGUAGE SQL AS $$SELECT $1[1]$$", 0)
		enumArrayLabels(t, p, b, c, "SELECT enum_first_of('{a,b}') AS v", [][]any{{"a"}})
		enumArrayLabels(t, p, b, c, "SELECT enum_first_of(ARRAY['b'::e]) AS v", [][]any{{"b"}})
		enumArrayExec(t, p, b, c, "CREATE FUNCTION enum_pair() RETURNS e[] LANGUAGE SQL AS $$SELECT ARRAY['a'::e,'b'::e]$$", 0)
		enumArrayResult(t, p, b, c, "SELECT enum_pair() AS v", [][]any{{"{a,b}"}})
		enumArrayQuery(t, p, b, c, "SELECT enum_pair()::text[] AS v", []string{"v"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{"{a,b}"}})
		enumArrayExec(t, p, b, c, "CREATE FUNCTION enum_first_of_plpgsql(e[]) RETURNS e LANGUAGE plpgsql AS $$BEGIN RETURN $1[1]; END$$", 0)
		enumArrayLabels(t, p, b, c, "SELECT enum_first_of_plpgsql('{b,a}') AS v", [][]any{{"b"}})
		enumArrayBool(t, p, b, c, "SELECT 'a'::e = ANY(enum_pair()) AS v", true)
		enumArrayExec(t, p, b, c, "CREATE FUNCTION enum_label_count(e[]) RETURNS int LANGUAGE SQL AS $$SELECT cardinality($1)$$", 0)
		enumArrayInt(t, p, b, c, "SELECT enum_label_count('{a,b}') AS v", [][]any{{int64(2)}})
		enumArrayText(t, p, b, c, "SELECT pg_get_function_arguments('enum_first_of(e[])'::regprocedure) AS v", [][]any{{"e[]"}})
		enumArrayText(t, p, b, c, "SELECT pg_get_function_result('enum_first_of(e[])'::regprocedure) AS v", [][]any{{"e"}})
	})
}
