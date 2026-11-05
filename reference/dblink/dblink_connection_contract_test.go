package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkConnectionSignatures(t *testing.T) {
	connectionCase(t, false, func(s *connectionScenario) {
		s.exec("CREATE ROLE dbc_reader")
		q := "SELECT p.proname::text AS name,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proisstrict AS strict,p.prosecdef AS definer,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS symbol,p.proargnames IS NULL AS unnamed FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='dbc_ext' AND p.proname IN ('dblink_connect','dblink_connect_u','dblink_disconnect','dblink_get_connections') ORDER BY p.proname,p.pronargs"
		s.run(fixture.Select(q, []string{"name", "args", "result", "strict", "definer", "volatility", "parallel", "symbol", "unnamed"}, []string{"TEXT", "TEXT", "INT8", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{
			{"dblink_connect", "25", int64(25), true, false, "v", "r", "dblink_connect", true},
			{"dblink_connect", "25 25", int64(25), true, false, "v", "r", "dblink_connect", true},
			{"dblink_connect_u", "25", int64(25), true, true, "v", "r", "dblink_connect", true},
			{"dblink_connect_u", "25 25", int64(25), true, true, "v", "r", "dblink_connect", true},
			{"dblink_disconnect", "", int64(25), true, false, "v", "r", "dblink_disconnect", true},
			{"dblink_disconnect", "25", int64(25), true, false, "v", "r", "dblink_disconnect", true},
			{"dblink_get_connections", "", int64(1009), false, false, "v", "r", "dblink_get_connections", true},
		}))
		for _, signature := range []string{"dblink_connect_u(text)", "dblink_connect_u(text,text)"} {
			s.run(fixture.Select("SELECT has_function_privilege('dbc_reader','dbc_ext."+signature+"','EXECUTE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{false}}))
		}
		s.run(fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='dbc_ext' AND p.proname IN ('dblink_build_sql_insert','dblink_build_sql_update','dblink_build_sql_delete','dblink_get_pkey')", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
		s.run(fixture.Select("SELECT t.typarray=a.oid AS paired,a.typelem=t.oid AS element FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_type a ON a.oid=t.typarray WHERE n.nspname='dbc_ext' AND t.typname='dblink_pkey_results'", []string{"paired", "element"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		if !s.b.PG {
			s.run(fixture.Select("SELECT count(*) AS n FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid=3079 AND d.deptype='e' AND e.extname='dblink'", []string{"n"}, []string{"INT8"}, [][]any{{int64(13)}}))
		}
		if len(s.provider.Snapshot()) != 0 {
			s.t.Fatal("catalog inspection/prepare opened transport")
		}
	})
	for _, tc := range []struct{ q, message string }{
		{"SELECT dbc_ext.dblink_connect()", "function dbc_ext.dblink_connect() does not exist"},
		{"SELECT dbc_ext.dblink_connect(1)", "function dbc_ext.dblink_connect(integer) does not exist"},
		{"SELECT dbc_ext.dblink_connect(connstr => 'x')", "function dbc_ext.dblink_connect(connstr => unknown) does not exist"},
		{"SELECT dbc_ext.dblink_connect(connname => 'x',connstr => 'y')", "function dbc_ext.dblink_connect(connname => unknown, connstr => unknown) does not exist"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			connectionCase(t, false, func(s *connectionScenario) {
				step := connectionError(tc.q, "42883", tc.message)
				step.ErrorStage = ""
				step.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				step.Diagnostic.Position = "8"
				s.run(step)
			})
		})
	}
	for _, tc := range []struct{ suffix, message string }{
		{" FILTER (WHERE true)", "FILTER specified, but dbc_ext.dblink_connect is not an aggregate function"},
		{" OVER ()", "OVER specified, but dbc_ext.dblink_connect is not a window function nor an aggregate function"},
	} {
		t.Run(tc.suffix, func(t *testing.T) {
			connectionCase(t, false, func(s *connectionScenario) {
				step := connectionError("SELECT dbc_ext.dblink_connect('unused')"+tc.suffix, "42809", tc.message)
				step.ErrorStage = ""
				step.Diagnostic.Position = "8"
				s.run(step)
			})
		})
	}
}

func TestReferenceDblinkConnectionNulls(t *testing.T) {
	connectionCase(t, false, func(s *connectionScenario) {
		s.list()
		s.run(connectionError("SELECT dbc_ext.dblink_disconnect() AS value", "08003", "connection not available"))
		s.run(connectionError("SELECT dbc_ext.dblink_disconnect($1::text) AS value", "08003", `connection "missing" not available`, "missing"))
		for _, q := range []string{
			"SELECT dbc_ext.dblink_connect(NULL::text) AS value",
			"SELECT dbc_ext.dblink_connect(NULL::text,'dbname=unused') AS value",
			"SELECT dbc_ext.dblink_connect('unused',NULL::text) AS value",
			"SELECT dbc_ext.dblink_connect_u(NULL::text) AS value",
			"SELECT dbc_ext.dblink_connect_u(NULL::text,'dbname=unused') AS value",
			"SELECT dbc_ext.dblink_connect_u('unused',NULL::text) AS value",
			"SELECT dbc_ext.dblink_disconnect(NULL::text) AS value",
		} {
			s.run(connectionText(q, nil))
		}
		s.list()
		if len(s.provider.Snapshot()) != 0 {
			s.t.Fatal("STRICT NULL reached provider")
		}
	})
}

func TestReferenceDblinkConnectionNames(t *testing.T) {
	connectionCase(t, true, func(s *connectionScenario) {
		names := []string{"", "A", "a", "=", strings.Repeat("x", 63), strings.Repeat("z", 61) + "é"}
		pids := map[string]int32{}
		for _, name := range names {
			_, pids[name] = s.open(new(name))
		}
		_, defaultPID := s.open(nil)
		s.list(names...)
		beforeNull := s.activity()
		s.run(connectionText("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", nil, nil, s.conninfo("null_not_opened", false)))
		s.run(connectionText("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", nil, "A", nil))
		s.run(connectionText("SELECT dbc_ext.dblink_disconnect(NULL::text) AS value", nil))
		s.unchangedActivity(beforeNull)
		s.list(names...)
		s.remoteText(nil, defaultPID, "SELECT pg_backend_pid()::text", fmt.Sprint(defaultPID))
		s.remoteText(new(""), pids[""], "SELECT 'literal empty named session'::text", "literal empty named session")
		s.remoteText(nil, defaultPID, "SELECT 'separate default session'::text", "separate default session")
		for _, name := range []string{strings.Repeat("x", 63) + "tail", strings.Repeat("z", 61) + "é" + "tail"} {
			start := len(*s.notices)
			s.run(connectionError("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "42710", "duplicate connection name", name, s.conninfo("unused", false)))
			s.truncationNotice(start, name, name[:63])
		}
		// A multibyte character crossing byte63 clips entirely, never midway.
		long := strings.Repeat("m", 62) + "é"
		app := fmt.Sprintf("dbc_%d", connectionSequence.Add(1))
		start := len(*s.notices)
		s.run(connectionText("SELECT dbc_ext.dblink_connect($1::text,$2::text) AS value", "OK", long, s.conninfo(app, false)))
		s.truncationNotice(start, long, strings.Repeat("m", 62))
		pid := s.pid(app)
		s.list(append(names, strings.Repeat("m", 62))...)
		start = len(*s.notices)
		s.disconnect(new(long), pid)
		if len(*s.notices) != start {
			s.t.Fatal("lookup/removal emitted truncation notice")
		}
		for _, name := range names {
			s.disconnect(new(name), pids[name])
		}
		s.list()
		s.disconnect(nil, defaultPID)
	})
}

func (s *connectionScenario) truncationNotice(start int, original, clipped string) {
	s.t.Helper()
	want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf("identifier \"%s\" will be truncated to \"%s\"", original, clipped)}
	witnesses := fixture.NoticeWitnesses(s.t, s.b.PG, (*s.notices)[start:], want)
	if len(witnesses) != 1 || witnesses[0] != want {
		s.t.Fatalf("truncation notice %#v want %#v", witnesses, want)
	}
}
