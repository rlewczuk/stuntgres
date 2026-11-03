package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type pgTrgmDeclaration struct {
	name, result, volatility, inputs, parallel string
	arity                                      int
}

var pgTrgmDeclarations = []pgTrgmDeclaration{
	{"show_trgm", "text[]", "i", "text", "s", 1},
	{"similarity", "real", "i", "text,text", "s", 2},
	{"word_similarity", "real", "i", "text,text", "s", 2},
	{"strict_word_similarity", "real", "i", "text,text", "s", 2},
	{"similarity_op", "boolean", "s", "text,text", "s", 2},
	{"word_similarity_op", "boolean", "s", "text,text", "s", 2},
	{"word_similarity_commutator_op", "boolean", "s", "text,text", "s", 2},
	{"strict_word_similarity_op", "boolean", "s", "text,text", "s", 2},
	{"strict_word_similarity_commutator_op", "boolean", "s", "text,text", "s", 2},
	{"similarity_dist", "real", "i", "text,text", "s", 2},
	{"word_similarity_dist_op", "real", "i", "text,text", "s", 2},
	{"word_similarity_dist_commutator_op", "real", "i", "text,text", "s", 2},
	{"strict_word_similarity_dist_op", "real", "i", "text,text", "s", 2},
	{"strict_word_similarity_dist_commutator_op", "real", "i", "text,text", "s", 2},
	{"set_limit", "real", "v", "real", "u", 1},
	{"show_limit", "real", "s", "", "s", 0},
}

var pgTrgmOperators = []struct {
	token, routine, commutator, result string
	reverse                            bool
	kernel                             int
}{
	{"%", "similarity_op", "%", "BOOL", false, 0},
	{"<%", "word_similarity_op", "%>", "BOOL", false, 1},
	{"%>", "word_similarity_commutator_op", "<%", "BOOL", true, 1},
	{"<<%", "strict_word_similarity_op", "%>>", "BOOL", false, 2},
	{"%>>", "strict_word_similarity_commutator_op", "<<%", "BOOL", true, 2},
	{"<->", "similarity_dist", "<->", "FLOAT4", false, 0},
	{"<<->", "word_similarity_dist_op", "<->>", "FLOAT4", false, 1},
	{"<->>", "word_similarity_dist_commutator_op", "<<->", "FLOAT4", true, 1},
	{"<<<->", "strict_word_similarity_dist_op", "<->>>", "FLOAT4", false, 2},
	{"<->>>", "strict_word_similarity_dist_commutator_op", "<<<->", "FLOAT4", true, 2},
}

func pgTrgmInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, schema string) {
	t.Helper()
	run := pgTrgmRun(t, p, b, c)
	run(fixture.Select(
		"SELECT e.extversion,e.extrelocatable,n.nspname,e.extowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS owner FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='pg_trgm'",
		[]string{"extversion", "extrelocatable", "nspname", "owner"},
		[]string{"TEXT", "BOOL", "NAME", "BOOL"}, [][]any{{"1.6", true, schema, true}},
	))
	run(fixture.Select(
		"SELECT version,superuser,trusted,relocatable,schema FROM pg_available_extension_versions WHERE name='pg_trgm' AND version='1.6'",
		[]string{"version", "superuser", "trusted", "relocatable", "schema"},
		[]string{"TEXT", "BOOL", "BOOL", "BOOL", "NAME"}, [][]any{{"1.6", true, true, true, nil}},
	))
	for _, d := range pgTrgmDeclarations {
		args := d.inputs
		q := "SELECT p.proname,p.pronargs,p.prorettype=$2::regtype::oid AS result,p.provolatile,p.proisstrict,p.proparallel,p.prokind,p.proretset,p.proargnames,p.pronargdefaults,p.provariadic=0 AS nonvariadic,l.lanname,p.probin,p.prosrc,p.pronamespace=e.extnamespace AS namespace,p.proowner=e.extowner AS owner,(SELECT count(*) FROM pg_depend d WHERE d.classid=1255 AND d.objid=p.oid AND d.refclassid=3079 AND d.refobjid=e.oid AND d.deptype='e') AS membership FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang JOIN pg_extension e ON e.extname='pg_trgm' WHERE p.oid=$1::regprocedure::oid"
		run(fixture.Select(q,
			[]string{"proname", "pronargs", "result", "provolatile", "proisstrict", "proparallel", "prokind", "proretset", "proargnames", "pronargdefaults", "nonvariadic", "lanname", "probin", "prosrc", "namespace", "owner", "membership"},
			[]string{"NAME", "INT2", "BOOL", "CHAR", "BOOL", "CHAR", "CHAR", "BOOL", "_TEXT", "INT2", "BOOL", "NAME", "TEXT", "TEXT", "BOOL", "BOOL", "INT8"},
			[][]any{{d.name, int64(d.arity), true, d.volatility, true, d.parallel, "f", false, nil, int64(0), true, "c", "$libdir/pg_trgm", d.name, true, true, int64(1)}},
			schema+"."+d.name+"("+args+")", d.result,
		))
		run(fixture.Select(
			"SELECT prosecdef,proleakproof,proconfig,procost,prorows FROM pg_proc WHERE oid=$1::regprocedure::oid",
			[]string{"prosecdef", "proleakproof", "proconfig", "procost", "prorows"},
			[]string{"BOOL", "BOOL", "_TEXT", "FLOAT4", "FLOAT4"},
			[][]any{{false, false, nil, float64(1), float64(0)}}, schema+"."+d.name+"("+args+")",
		))
	}
	for _, op := range pgTrgmOperators {
		q := "SELECT o.oprname,p.proname,c.oprname,o.oprleft=25 AS left_text,o.oprright=25 AS right_text,o.oprresult=p.prorettype AS result,o.oprkind,o.oprcanmerge,o.oprcanhash,o.oprnegate=0 AS no_negator,o.oprnamespace=p.pronamespace AS namespace,(SELECT count(*) FROM pg_depend d WHERE d.classid=2617 AND d.objid=o.oid AND d.refclassid=3079 AND d.refobjid=e.oid AND d.deptype='e') AS membership FROM pg_operator o JOIN pg_proc p ON p.oid=o.oprcode JOIN pg_operator c ON c.oid=o.oprcom JOIN pg_extension e ON e.extnamespace=o.oprnamespace AND e.extname='pg_trgm' WHERE o.oprname=$1 AND o.oprleft=25 AND o.oprright=25"
		run(fixture.Select(q,
			[]string{"oprname", "proname", "oprname", "left_text", "right_text", "result", "oprkind", "oprcanmerge", "oprcanhash", "no_negator", "namespace", "membership"},
			[]string{"NAME", "NAME", "NAME", "BOOL", "BOOL", "BOOL", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "INT8"},
			[][]any{{op.token, op.routine, op.commutator, true, true, true, "b", false, false, true, true, int64(1)}}, op.token,
		))
		restriction, join := "0::oid", "0::oid"
		if op.result == "BOOL" {
			restriction, join = "'pg_catalog.matchingsel'::regproc::oid", "'pg_catalog.matchingjoinsel'::regproc::oid"
		}
		run(fixture.Select(
			"SELECT oprrest="+restriction+" AS restriction,oprjoin="+join+" AS join_estimator FROM pg_operator WHERE oprnamespace=$1::regnamespace::oid AND oprname=$2 AND oprleft=25 AND oprright=25",
			[]string{"restriction", "join_estimator"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, schema, op.token,
		))
	}
	if !b.PG {
		run(fixture.Select("SELECT count(*) FROM pg_depend WHERE refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='pg_trgm') AND deptype='e'", []string{"count"}, []string{"INT8"}, [][]any{{int64(26)}}))
		run(fixture.Select("SELECT count(*) FROM pg_proc WHERE pronamespace=$1::regnamespace::oid AND proname IN ('gin_extract_value_trgm','gtrgm_consistent')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, schema))
	}
}

func TestReferencePgTrgmDeclarations(t *testing.T) {
	pgTrgmTrustedDeclarationCase(t)
	t.Run("Inventory", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("CREATE SCHEMA trgm_addon", 0))
			run(fixture.Exec("CREATE EXTENSION pg_trgm SCHEMA trgm_addon VERSION '1.6'", 0))
			pgTrgmInventory(t, p, b, c, "trgm_addon")
			run(fixture.ImplicitError("CREATE EXTENSION pg_trgm", fixture.Diagnostic{Code: "42710", Message: `extension "pg_trgm" already exists`}))
			run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm", 0))
			pgTrgmNotices(t, notices, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "pg_trgm" already exists, skipping`})
			pgTrgmInventory(t, p, b, c, "trgm_addon")
		})
	})
	for _, end := range []string{"ROLLBACK", "ROLLBACK TO s"} {
		t.Run(end, func(t *testing.T) {
			pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				run := pgTrgmRun(t, p, b, c)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT s", 0))
				run(fixture.Exec("CREATE EXTENSION pg_trgm", 0))
				pgTrgmLoaded(run, true)
				run(fixture.Exec(end, 0))
				pgTrgmLoaded(run, true)
				run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='pg_trgm'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Select("SELECT count(*) FROM pg_proc WHERE pronamespace='public'::regnamespace::oid AND proname IN ('similarity','show_trgm','word_similarity','strict_word_similarity','set_limit','show_limit')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				if end != "ROLLBACK" {
					run(fixture.Exec("COMMIT", 0))
				}
				pgTrgmNotices(t, notices)
			})
		})
	}
	for _, tc := range []struct {
		name, setup, create string
		diagnostic          fixture.Diagnostic
		loaded              bool
	}{
		{"MissingSchema", "", "CREATE EXTENSION pg_trgm SCHEMA trgm_absent", fixture.Diagnostic{Code: "3F000", Message: `schema "trgm_absent" does not exist`}, false},
		{"MissingVersion", "", "CREATE EXTENSION pg_trgm VERSION 'missing'", fixture.Diagnostic{Code: "22023", Message: `extension "pg_trgm" has no installation script nor update path for version "missing"`}, false},
		{
			"SetterCollision",
			"CREATE FUNCTION set_limit(real) RETURNS real LANGUAGE SQL AS 'SELECT $1'",
			"CREATE EXTENSION pg_trgm",
			fixture.Diagnostic{
				Code: "42723", Message: `function "set_limit" already exists with same argument types`,
				Context: "SQL statement \"CREATE FUNCTION set_limit(float4)\nRETURNS float4\nAS '$libdir/pg_trgm'\nLANGUAGE C STRICT VOLATILE PARALLEL UNSAFE\"\nextension script file \"pg_trgm--1.3.sql\", near line 7",
			},
			false,
		},
		{
			"GetterCollision",
			"CREATE FUNCTION show_limit() RETURNS real LANGUAGE SQL AS 'SELECT 0.875::real'",
			"CREATE EXTENSION pg_trgm",
			fixture.Diagnostic{
				Code: "42723", Message: `function "show_limit" already exists with same argument types`,
				Context: "SQL statement \"CREATE FUNCTION show_limit()\nRETURNS float4\nAS '$libdir/pg_trgm'\nLANGUAGE C STRICT STABLE PARALLEL SAFE\"\nextension script file \"pg_trgm--1.3.sql\", near line 13",
			},
			true,
		},
		{
			"RoutineCollision",
			"CREATE FUNCTION show_trgm(text) RETURNS text[] LANGUAGE SQL IMMUTABLE AS 'SELECT ARRAY[$1]'",
			"CREATE EXTENSION pg_trgm",
			fixture.Diagnostic{
				Code: "42723", Message: `function "show_trgm" already exists with same argument types`,
				Context: "SQL statement \"CREATE FUNCTION show_trgm(text)\nRETURNS _text\nAS '$libdir/pg_trgm'\nLANGUAGE C STRICT IMMUTABLE PARALLEL SAFE\"\nextension script file \"pg_trgm--1.3.sql\", near line 18",
			},
			true,
		},
		{
			"LateOperatorCollision",
			"CREATE FUNCTION trgm_same(text,text) RETURNS boolean LANGUAGE SQL IMMUTABLE AS 'SELECT $1=$2'; CREATE OPERATOR % (LEFTARG=text,RIGHTARG=text,FUNCTION=trgm_same)",
			"CREATE EXTENSION pg_trgm",
			fixture.Diagnostic{
				Code: "42723", Message: "operator % already exists",
				// Released statement text/line; next oracle checkpoint pins
				// the complete record now that the SQLSTATE is observed.
				Context: "SQL statement \"CREATE OPERATOR % (\n        LEFTARG = text,\n        RIGHTARG = text,\n        PROCEDURE = similarity_op,\n        COMMUTATOR = '%',\n        RESTRICT = contsel,\n        JOIN = contjoinsel\n)\"\nextension script file \"pg_trgm--1.3.sql\", near line 33",
			},
			true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				run := pgTrgmRun(t, p, b, c)
				run(fixture.Exec("CREATE TABLE trgm_sentinel(id int)", 0))
				run(fixture.Exec("INSERT INTO trgm_sentinel VALUES(17)", 1))
				if tc.setup != "" {
					for _, q := range strings.Split(tc.setup, "; ") {
						run(fixture.Exec(q, 0))
					}
				}
				run(fixture.ImplicitError(tc.create, tc.diagnostic))
				run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='pg_trgm'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				procedures, operators := int64(0), int64(0)
				if tc.name == "RoutineCollision" || tc.name == "SetterCollision" || tc.name == "GetterCollision" {
					procedures = 1
				}
				if tc.name == "LateOperatorCollision" {
					operators = 1
				}
				run(fixture.Select(
					"SELECT count(*) FROM pg_proc WHERE pronamespace='public'::regnamespace::oid AND proname IN ('show_trgm','similarity','word_similarity','strict_word_similarity','similarity_op','word_similarity_op','word_similarity_commutator_op','strict_word_similarity_op','strict_word_similarity_commutator_op','similarity_dist','word_similarity_dist_op','word_similarity_dist_commutator_op','strict_word_similarity_dist_op','strict_word_similarity_dist_commutator_op','set_limit','show_limit')",
					[]string{"count"}, []string{"INT8"}, [][]any{{procedures}},
				))
				run(fixture.Select("SELECT count(*) FROM pg_operator WHERE oprnamespace='public'::regnamespace::oid AND oprname IN ('%','<%','%>','<<%','%>>','<->','<<->','<->>','<<<->','<->>>')", []string{"count"}, []string{"INT8"}, [][]any{{operators}}))
				run(fixture.Select("SELECT id FROM trgm_sentinel ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
				pgTrgmLoaded(run, tc.loaded)
				pgTrgmNotices(t, notices)
			})
		})
	}
	t.Run("InstallPrivilege", func(t *testing.T) {
		pgTrgmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
			run := pgTrgmRun(t, p, b, c)
			run(fixture.Exec("CREATE USER trgm_reader NOSUPERUSER", 0))
			run(fixture.Exec("CREATE SCHEMA trgm_addon AUTHORIZATION trgm_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION trgm_reader", 0))
			run(fixture.ImplicitError("CREATE EXTENSION pg_trgm SCHEMA trgm_addon", fixture.Diagnostic{
				Code: "42501", Message: `permission denied to create extension "pg_trgm"`,
				Hint: "Must have CREATE privilege on current database to create this extension.",
			}))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			pgTrgmLoaded(run, false)
			pgTrgmNotices(t, notices)
		})
	})
}
