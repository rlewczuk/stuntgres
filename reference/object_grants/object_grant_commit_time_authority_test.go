package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantCommitTimeAuthority(t *testing.T) {
	for _, name := range []string{"pg_xact_commit_timestamp", "pg_xact_commit_timestamp_origin", "pg_last_committed_xact"} {
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				// This package deliberately retains the fixture's default-off startup
				// mode. An authorized data lookup must expose the real 55000, not a
				// fabricated timestamp or an attempt to SET a startup-only setting.
				run(fixture.Select("SHOW track_commit_timestamp", []string{"track_commit_timestamp"}, []string{"TEXT"}, [][]any{{"off"}}))
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_commit_time_input(id INT,x xid)", 0))
				run(fixture.Exec("INSERT INTO og_commit_time_input VALUES(1,'1'),(2,NULL),(3,'3')", 3))
				run(fixture.Exec("GRANT SELECT ON og_commit_time_input TO og_object_reader", 0))
				last := name == "pg_last_committed_xact"
				target, call, typ := "pg_catalog."+name+"(xid)", "pg_catalog."+name+"(x)", "TIMESTAMPTZ"
				var special any
				if name == "pg_xact_commit_timestamp_origin" {
					typ, special = "RECORD", "(,)"
				}
				if last {
					target, call, typ = "pg_catalog."+name+"()", "pg_catalog."+name+"()", "RECORD"
				}
				viewQuery := "SELECT " + call + " AS v FROM og_commit_time_input WHERE id=1"
				if name == "pg_xact_commit_timestamp_origin" {
					viewQuery = "SELECT (" + call + ").* FROM og_commit_time_input WHERE id=1"
				}
				if last {
					viewQuery = "SELECT * FROM pg_catalog.pg_last_committed_xact()"
				}
				run(fixture.Exec("CREATE VIEW og_commit_time_view AS "+viewQuery, 0))
				run(fixture.Exec("GRANT SELECT ON og_commit_time_view TO og_object_reader", 0))
				value := func(expr, tail string, want any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_commit_time_input"+tail, []string{"v"}, []string{typ}, [][]any{{want}}, args...)
				}
				disabled := func(q string, args ...any) fixture.Step {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "55000", Message: "could not get commit timestamp data", Hint: `Make sure the configuration parameter "track_commit_timestamp" is set.`}, args...)
					s.SelectRows, s.ErrorStage = true, "execute"
					return s
				}
				var cases []objectGrantProviderCase
				// Pinned PostgreSQL retains EXECUTE demand for RECORD-returning
				// origin(NULL), and for noarg last even under LIMIT 0. Only the
				// scalar known-NULL call disappears; row-backed NULL still demands
				// its callable. Strict metadata alone is not this demand contract.
				if last {
					cases = []objectGrantProviderCase{
						{disabled("SELECT " + call + " AS v FROM og_commit_time_input WHERE id=1"), true, true},
						{disabled("SELECT pg_last_committed_xact()"), true, true},
						{disabled("SELECT * FROM og_commit_time_view"), true, true},
						{disabled("SELECT * FROM pg_catalog.pg_last_committed_xact()"), true, true},
						{fixture.Select("SELECT pg_catalog.pg_last_committed_xact() AS v LIMIT 0", []string{"v"}, []string{"RECORD"}, nil), true, true},
						{fixture.Select("SELECT * FROM pg_catalog.pg_last_committed_xact() LIMIT 0", []string{"xid", "timestamp", "roident"}, []string{"XID", "TIMESTAMPTZ", "OID"}, nil), true, true},
					}
				} else {
					cases = []objectGrantProviderCase{
						{value(call, " WHERE id=1", special), true, true},
						{value(call, " WHERE id=2", nil), true, true},
						{value(name+"(NULL::xid)", " WHERE id=1", nil), name == "pg_xact_commit_timestamp_origin", true},
						{value(name+"('1'::xid)", " WHERE id=1", special), true, true},
						{value("pg_catalog."+name+"($1::xid)", " WHERE id=1", special, "1"), true, true},
						{value("pg_catalog."+name+"($1::xid)", " WHERE id=1", nil, nil), name == "pg_xact_commit_timestamp_origin", false},
						{disabled("SELECT " + call + " AS v FROM og_commit_time_input WHERE id=3"), true, true},
					}
					if name == "pg_xact_commit_timestamp_origin" {
						cases = append(cases,
							objectGrantProviderCase{fixture.Select("SELECT * FROM og_commit_time_view", []string{"timestamp", "roident"}, []string{"TIMESTAMPTZ", "OID"}, [][]any{{nil, nil}}), true, true},
							objectGrantProviderCase{fixture.Select("SELECT ("+call+").* FROM og_commit_time_input WHERE id=1", []string{"timestamp", "roident"}, []string{"TIMESTAMPTZ", "OID"}, [][]any{{nil, nil}}), true, true},
							objectGrantProviderCase{fixture.Select("SELECT * FROM pg_catalog.pg_xact_commit_timestamp_origin('1'::xid)", []string{"timestamp", "roident"}, []string{"TIMESTAMPTZ", "OID"}, [][]any{{nil, nil}}), true, true},
							objectGrantProviderCase{fixture.Select("SELECT * FROM pg_catalog.pg_xact_commit_timestamp_origin(NULL::xid)", []string{"timestamp", "roident"}, []string{"TIMESTAMPTZ", "OID"}, [][]any{{nil, nil}}), true, true},
						)
					} else {
						cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT v FROM og_commit_time_view", []string{"v"}, []string{typ}, [][]any{{special}}), true, true})
						cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT * FROM pg_catalog.pg_xact_commit_timestamp('1'::xid)", []string{name}, []string{"TIMESTAMPTZ"}, [][]any{{nil}}), true, true})
						cases = append(cases, objectGrantProviderCase{fixture.Select("SELECT * FROM pg_catalog.pg_xact_commit_timestamp(NULL::xid)", []string{name}, []string{"TIMESTAMPTZ"}, [][]any{{nil}}), false, true})
					}
				}
				objectGrantProviderLifecycle(t, p, b, c, run, target, "function", name, cases, nil)
			})
		})
	}
}
