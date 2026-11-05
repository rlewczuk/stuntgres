package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheNUMADiagnostics(t *testing.T) {
	bcNUMAScenario(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		missing := func(q, signature string) fixture.Step {
			s := bcError(q, "42883", "function "+signature+" does not exist")
			s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			s.Diagnostic.Position = fixture.Position(strings.Index(q, "pg_buffercache_numa_pages") + 1)
			// Qualified references point at the start of the complete name.
			if i := strings.Index(q, "bc_part."); i >= 0 {
				s.Diagnostic.Position = fixture.Position(i + 1)
			}
			return s
		}
		t.Run("absent_installation", func(t *testing.T) {
			bcFailure(t, p, b, c, missing("SELECT * FROM "+bcNUMAFunction(), "bc_part.pg_buffercache_numa_pages()"), "prepare")
		})
		t.Run("install_conflicts_are_atomic", func(t *testing.T) { bcNUMAInstallConflicts(t, p, b, c) })
		t.Run("install_replacement_validation", func(t *testing.T) {
			bcNUMAReplacementConflicts(t, p, b, c)
		})
		bcDDL(t, p, b, c, "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
		tests := []struct {
			name string
			step fixture.Step
			stage string
			runtime bool
		}{
			{"missing_coldef", bcError("SELECT * FROM bc_part.pg_buffercache_numa_pages()", "42601", "a column definition list is required for functions returning \"record\""), "prepare", false},
			{"scalar", bcError("SELECT bc_part.pg_buffercache_numa_pages()", "XX000", "return type must be a row type"), "execute", true},
			{"two_outputs", bcError("SELECT * FROM "+bcFrom("bc_part.pg_buffercache_numa_pages", bcNUMANames[:2], bcNUMASQLTypes[:2]), "XX000", "incorrect number of output arguments"), "execute", true},
			{"four_outputs", bcError("SELECT * FROM "+bcFrom("bc_part.pg_buffercache_numa_pages", []string{"bufferid", "os_page_num", "numa_node", "extra"}, []string{"integer", "bigint", "integer", "integer"}), "XX000", "incorrect number of output arguments"), "execute", true},
			{"eight_not_historical_prefix", bcError("SELECT * FROM "+bcFrom("bc_part.pg_buffercache_numa_pages", bcNames[:8], bcSQLTypes[:8]), "XX000", "incorrect number of output arguments"), "execute", true},
			{"extra_integer", missing("SELECT * FROM bc_part.pg_buffercache_numa_pages(1)", "bc_part.pg_buffercache_numa_pages(integer)"), "prepare", false},
			{"extra_null", missing("SELECT * FROM bc_part.pg_buffercache_numa_pages(NULL)", "bc_part.pg_buffercache_numa_pages(unknown)"), "prepare", false},
			{"extra_typed_null", missing("SELECT * FROM bc_part.pg_buffercache_numa_pages(NULL::integer)", "bc_part.pg_buffercache_numa_pages(integer)"), "prepare", false},
			{"missing_schema", bcError("SELECT * FROM bc_missing.pg_buffercache_numa_pages()", "3F000", "schema \"bc_missing\" does not exist"), "prepare", false},
		}
		tests[0].step.Diagnostic.Position = fixture.Position(15)
		tests[8].step.Diagnostic.Position = fixture.Position(15)
		for i, actual := range []string{"integer", "bigint", "integer"} {
			wrong := append([]string{}, bcNUMASQLTypes...)
			wrong[i] = "text"
			if i == 0 {
				wrong[i] = "bigint"
			}
			s := bcError("SELECT * FROM "+bcFrom("bc_part.pg_buffercache_numa_pages", bcNUMANames, wrong), "42804", "function return row and query-specified return row do not match")
			s.Diagnostic.Detail = fmt.Sprintf("Returned type %s at ordinal position %d, but query expects %s.", actual, i+1, wrong[i])
			tests = append(tests, struct { name string; step fixture.Step; stage string; runtime bool }{fmt.Sprintf("wrong_type_%d", i+1), s, "execute", true})
		}
		duplicate := bcError("SELECT * FROM "+bcFrom("bc_part.pg_buffercache_numa_pages", []string{"bufferid", "bufferid", "numa_node"}, bcNUMASQLTypes), "42701", "column name \"bufferid\" specified more than once")
		tests = append(tests, struct { name string; step fixture.Step; stage string; runtime bool }{"duplicate_name", duplicate, "prepare", false})
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				s := tc.step
				if tc.runtime {
					s = bcNUMADemandStep(b, s)
				}
				bcFailure(t, p, b, c, s, tc.stage)
			})
		}
		bcDDL(t, p, b, c, "SET search_path=pg_catalog")
		defer c.ExecContext(p.Ctx, "RESET search_path")
		q := "SELECT * FROM pg_buffercache_numa_pages()"
		bcFailure(t, p, b, c, missing(q, "pg_buffercache_numa_pages()"), "prepare")
		bcNUMAEmpty(t, p, b, c, bcNUMAFunction(), false)
		bcNUMAEmpty(t, p, b, c, "bc_part.pg_buffercache_numa", true)
		// Recovery demands the real callable again after all failure paths.
		bcNUMABoth(t, p, b, c, bcNUMACount("SELECT count(DISTINCT bufferid) AS n FROM "+bcNUMAFunction(), bcCapacity(t, p, c)))
	})
}

func bcNUMAInstallConflicts(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, view := range []bool{false, true} {
		setup := "CREATE FUNCTION bc_part.pg_buffercache_numa_pages() RETURNS integer LANGUAGE SQL AS 'SELECT 77'"
		drop := "DROP FUNCTION bc_part.pg_buffercache_numa_pages()"
		code, message := "42P13", "cannot change return type of existing function"
		if view {
			setup, drop = "CREATE VIEW bc_part.pg_buffercache_numa AS SELECT 88 AS sentinel", "DROP VIEW bc_part.pg_buffercache_numa"
			code, message = "42P07", "relation \"pg_buffercache_numa\" already exists"
		}
		bcDDL(t, p, b, c, setup)
		s := bcError("CREATE EXTENSION pg_buffercache SCHEMA bc_part", code, message)
		s.SelectRows = false
		// Independently fixed REL_18_6 script/ProcedureCreate expectations;
		// root confirms complete Context and Hint in the pinned oracle.
		if view {
			s.Diagnostic.Context = "SQL statement \"CREATE VIEW pg_buffercache_numa AS\n\tSELECT P.* FROM pg_buffercache_numa_pages() AS P\n\t(bufferid integer, os_page_num bigint, numa_node integer)\"\nextension script file \"pg_buffercache--1.5--1.6.sql\", near line 13"
		} else {
			s.Diagnostic.Hint = "Use DROP FUNCTION pg_buffercache_numa_pages() first."
			s.Diagnostic.Context = "SQL statement \"CREATE OR REPLACE FUNCTION pg_buffercache_numa_pages()\nRETURNS SETOF RECORD\nAS '$libdir/pg_buffercache', 'pg_buffercache_numa_pages'\nLANGUAGE C PARALLEL SAFE\"\nextension script file \"pg_buffercache--1.5--1.6.sql\", near line 7"
		}
		bcFailure(t, p, b, c, s, "execute")
		for _, q := range []string{
			"SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'",
			"SELECT count(*) AS n FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname IN ('pg_buffercache_pages','pg_buffercache_evict','pg_buffercache_evict_relation','pg_buffercache_evict_all','pg_buffercache_summary','pg_buffercache_usage_counts')",
			"SELECT count(*) AS n FROM pg_class WHERE relnamespace='bc_part'::regnamespace AND relname='pg_buffercache'",
			"SELECT count(*) AS n FROM pg_type WHERE typnamespace='bc_part'::regnamespace AND typname IN ('pg_buffercache','_pg_buffercache')",
		} {
			bcBoth(t, p, b, c, bcNUMACount(q, 0))
		}
		if view {
			bcBoth(t, p, b, c, fixture.Select("SELECT sentinel FROM bc_part.pg_buffercache_numa", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(88)}}))
			bcBoth(t, p, b, c, bcNUMACount("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname='pg_buffercache_numa_pages'", 0))
		} else {
			bcBoth(t, p, b, c, fixture.Select("SELECT bc_part.pg_buffercache_numa_pages() AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(77)}}))
			bcBoth(t, p, b, c, bcNUMACount("SELECT count(*) AS n FROM pg_class WHERE relnamespace='bc_part'::regnamespace AND relname='pg_buffercache_numa'", 0))
			bcBoth(t, p, b, c, bcNUMACount("SELECT count(*) AS n FROM pg_type WHERE typnamespace='bc_part'::regnamespace AND typname IN ('pg_buffercache_numa','_pg_buffercache_numa')", 0))
		}
		bcDDL(t, p, b, c, drop)
	}
	bcDDL(t, p, b, c, "CREATE FUNCTION bc_part.pg_buffercache_numa_pages() RETURNS SETOF record LANGUAGE SQL AS 'SELECT 77'")
	var original int64
	identity := "SELECT 'bc_part.pg_buffercache_numa_pages()'::regprocedure::oid::bigint AS original"
	if err := c.QueryRowContext(p.Ctx, identity).Scan(&original); err != nil || original <= 0 { t.Fatal("compatible shadow identity", original, err) }
	s := bcError("CREATE EXTENSION pg_buffercache SCHEMA bc_part", "55000", "function pg_buffercache_numa_pages() is not a member of extension \"pg_buffercache\"")
	s.SelectRows = false
	s.Diagnostic.Detail = "An extension is not allowed to replace an object that it does not own."
	s.Diagnostic.Context = "SQL statement \"CREATE OR REPLACE FUNCTION pg_buffercache_numa_pages()\nRETURNS SETOF RECORD\nAS '$libdir/pg_buffercache', 'pg_buffercache_numa_pages'\nLANGUAGE C PARALLEL SAFE\"\nextension script file \"pg_buffercache--1.5--1.6.sql\", near line 7"
	bcFailure(t, p, b, c, s, "execute")
	bcBoth(t, p, b, c, fixture.Select(identity, []string{"original"}, []string{"INT8"}, [][]any{{original}}))
	bcBoth(t, p, b, c, fixture.Select("SELECT * FROM bc_part.pg_buffercache_numa_pages() AS p(sentinel integer)", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(77)}}))
	for _, q := range []string{
		"SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'",
		"SELECT count(*) AS n FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname IN ('pg_buffercache_pages','pg_buffercache_evict','pg_buffercache_evict_relation','pg_buffercache_evict_all','pg_buffercache_summary','pg_buffercache_usage_counts')",
		"SELECT count(*) AS n FROM pg_class WHERE relnamespace='bc_part'::regnamespace AND relname IN ('pg_buffercache','pg_buffercache_numa')",
		"SELECT count(*) AS n FROM pg_type WHERE typnamespace='bc_part'::regnamespace AND typname IN ('pg_buffercache','_pg_buffercache','pg_buffercache_numa','_pg_buffercache_numa')",
		"SELECT count(*) AS n FROM pg_depend WHERE classid=1255 AND objid='bc_part.pg_buffercache_numa_pages()'::regprocedure AND deptype='e'",
	} {
		bcBoth(t, p, b, c, bcNUMACount(q, 0))
	}
	bcDDL(t, p, b, c, "DROP FUNCTION bc_part.pg_buffercache_numa_pages()")
}

func bcNUMAReplacementConflicts(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct {
		name, create, drop, cleanup, kind, body, code, message, detail, hint string
	}{
		{
			name: "procedure_kind",
			create: "CREATE PROCEDURE bc_part.pg_buffercache_numa_pages() LANGUAGE SQL AS 'SELECT 77'",
			drop: "DROP PROCEDURE bc_part.pg_buffercache_numa_pages()",
			cleanup: "DROP PROCEDURE IF EXISTS bc_part.pg_buffercache_numa_pages()",
			kind: "p", body: "SELECT 77",
			code: "42809", message: "cannot change routine kind",
			detail: "\"pg_buffercache_numa_pages\" is a procedure.",
		},
		{
			name: "declared_out_shape",
			create: "CREATE FUNCTION bc_part.pg_buffercache_numa_pages() RETURNS TABLE(bufferid integer, os_page_num bigint, numa_node integer) LANGUAGE SQL AS 'SELECT 1, 0::bigint, 0'",
			drop: "DROP FUNCTION bc_part.pg_buffercache_numa_pages()",
			cleanup: "DROP FUNCTION IF EXISTS bc_part.pg_buffercache_numa_pages()",
			kind: "f", body: "SELECT 1, 0::bigint, 0",
			code: "42P13", message: "cannot change return type of existing function",
			detail: "Row type defined by OUT parameters is different.",
			hint: "Use DROP FUNCTION pg_buffercache_numa_pages() first.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bcDDL(t, p, b, c, tc.create)
			defer func() {
				for _, q := range []string{"DROP EXTENSION IF EXISTS pg_buffercache CASCADE", tc.cleanup} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil { t.Error("replacement fixture cleanup", q, err) }
				}
			}()
			var original int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname='pg_buffercache_numa_pages' AND pronargs=0").Scan(&original); err != nil || original <= 0 {
				t.Fatal("genuine incumbent routine identity", original, err)
			}
			preserved := fixture.Select("SELECT oid::bigint AS original,prokind::text AS kind,prosrc,proacl::text AS acl,pg_get_userbyid(proowner)=current_user AS owner FROM pg_proc WHERE oid=$1::oid", []string{"original", "kind", "prosrc", "acl", "owner"}, []string{"INT8", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{original, tc.kind, tc.body, nil, true}}, original)
			bcBoth(t, p, b, c, preserved)
			s := bcError("CREATE EXTENSION pg_buffercache SCHEMA bc_part", tc.code, tc.message)
			s.SelectRows = false
			s.Diagnostic.Detail, s.Diagnostic.Hint = tc.detail, tc.hint
			// Released replacement validation precedes extension membership.
			// Both records require fresh pinned-oracle evidence.
			s.Diagnostic.Context = "SQL statement \"CREATE OR REPLACE FUNCTION pg_buffercache_numa_pages()\nRETURNS SETOF RECORD\nAS '$libdir/pg_buffercache', 'pg_buffercache_numa_pages'\nLANGUAGE C PARALLEL SAFE\"\nextension script file \"pg_buffercache--1.5--1.6.sql\", near line 7"
			bcFailure(t, p, b, c, s, "execute")
			bcBoth(t, p, b, c, preserved)
			for _, q := range []string{
				"SELECT count(*) AS n FROM pg_extension WHERE extname='pg_buffercache'",
				"SELECT count(*) AS n FROM pg_proc WHERE pronamespace='bc_part'::regnamespace AND proname IN ('pg_buffercache_pages','pg_buffercache_evict','pg_buffercache_evict_relation','pg_buffercache_evict_all','pg_buffercache_summary','pg_buffercache_usage_counts')",
				"SELECT count(*) AS n FROM pg_class WHERE relnamespace='bc_part'::regnamespace AND relname IN ('pg_buffercache','pg_buffercache_numa')",
				"SELECT count(*) AS n FROM pg_type WHERE typnamespace='bc_part'::regnamespace AND typname IN ('pg_buffercache','_pg_buffercache','pg_buffercache_numa','_pg_buffercache_numa')",
				"SELECT count(*) AS n FROM pg_depend WHERE classid=1255 AND objid='bc_part.pg_buffercache_numa_pages()'::regprocedure AND deptype='e'",
				"SELECT count(*) AS n FROM pg_init_privs i JOIN pg_proc p ON p.oid=i.objoid WHERE i.classoid=1255 AND p.pronamespace='bc_part'::regnamespace",
			} {
				bcBoth(t, p, b, c, bcNUMACount(q, 0))
			}
			if tc.kind == "p" {
				bcBoth(t, p, b, c, fixture.Exec("CALL bc_part.pg_buffercache_numa_pages()", 0))
			} else {
				bcBoth(t, p, b, c, fixture.Select("SELECT * FROM bc_part.pg_buffercache_numa_pages()", bcNUMANames, bcNUMATypes, [][]any{{int64(1), int64(0), int64(0)}}))
			}
			bcDDL(t, p, b, c, tc.drop, "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
			bcNUMAEmpty(t, p, b, c, bcNUMAFunction(), false)
			bcNUMABoth(t, p, b, c, bcNUMACount("SELECT count(DISTINCT bufferid) AS n FROM "+bcNUMAFunction(), bcCapacity(t, p, c)))
		})
	}
}
