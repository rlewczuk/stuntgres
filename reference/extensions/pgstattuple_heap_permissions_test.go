package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstattupleHeapPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"hs_owner", "hs_reader"})
	heapStatsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE ROLE hs_owner", "CREATE ROLE hs_reader", "GRANT USAGE ON SCHEMA hs_part TO hs_owner,hs_reader", "ALTER TABLE hs_part.heap OWNER TO hs_owner"} { run(fixture.Exec(q, 0)) }
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		members := []struct{ name, typ string }{{"pgstattuple", "text"}, {"pgstattuple", "regclass"}, {"pgstattuple_approx", "regclass"}, {"pg_relpages", "text"}, {"pg_relpages", "regclass"}}
		// A parameter NULL alone can be folded through a STRICT function before
		// executor ACL admission. Keep a real nullable Var in the argument so
		// denied NULL calls actually exercise EXECUTE, including held plans.
		for _, q := range []string{
			"CREATE TABLE hs_part.acl_inputs(n text)",
			"INSERT INTO hs_part.acl_inputs VALUES(NULL)",
			"GRANT SELECT ON hs_part.acl_inputs TO hs_owner,hs_reader",
		} {
			count := int64(0)
			if q == "INSERT INTO hs_part.acl_inputs VALUES(NULL)" { count = 1 }
			run(fixture.Exec(q, count))
		}
		held := make([]fixture.HeldStatement, len(members))
		for i, m := range members {
			q := "SELECT s.* FROM hs_part.acl_inputs i CROSS JOIN LATERAL hs_part."+m.name+"(CAST(COALESCE($1::text,i.n) AS "+m.typ+")) s"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer st.Close()
			held[i] = fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		}
		denied := func(i int, arg any) {
			s := pgstatindexError(held[i].SQL, "42501", "permission denied for function "+members[i].name, arg)
			heapStatsBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
		}
		authorized := func(i int) {
			m := members[i]
			q := held[i].SQL
			var s fixture.Step
			switch m.name {
			case "pgstattuple": s = fixture.Select(q, heapStatsNames, heapStatsTypes, [][]any{make([]any, 9)}, nil)
			case "pgstattuple_approx": s = fixture.Select(q, heapApproxNames, heapApproxTypes, [][]any{make([]any, 10)}, nil)
			default: s = fixture.Select(q, []string{"s"}, []string{"INT8"}, [][]any{{nil}}, nil)
			}
			heapStatsBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
			if m.name == "pg_relpages" {
				s = fixture.Select(q, []string{"s"}, []string{"INT8"}, [][]any{{int64(0)}}, "hs_part.heap")
			} else if m.name == "pgstattuple" {
				s = fixture.Select(q, heapStatsNames, heapStatsTypes, [][]any{{int64(0), int64(0), int64(0), float64(0), int64(0), int64(0), float64(0), int64(0), float64(0)}}, "hs_part.heap")
			} else {
				s = fixture.Select(q, heapApproxNames, heapApproxTypes, [][]any{{int64(0), float64(0), int64(0), int64(0), float64(0), int64(0), int64(0), float64(0), int64(0), float64(0)}}, "hs_part.heap")
			}
			heapStatsBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION hs_owner", 0))
		run(fixture.Select("SELECT k FROM hs_part.heap", []string{"k"}, []string{"INT4"}, nil))
		for i := range members { denied(i, nil); denied(i, "hs_part.heap") }
		// Stock STRICT simplification elides a constant-NULL scalar bigint
		// call, but deliberately leaves RECORD calls for executor admission.
		// Keep both static behavior and the dynamic NULL denial above.
		for _, m := range members {
			q := "SELECT * FROM hs_part."+m.name+"($1::"+m.typ+")"
			if m.name == "pg_relpages" {
				heapStatsBoth(t, p, b, c, fixture.Select(q, []string{"pg_relpages"}, []string{"INT8"}, [][]any{{nil}}, nil))
				for _, arg := range []string{"NULL::"+m.typ, "$1::"+m.typ} {
					args := []any(nil)
					if arg == "$1::"+m.typ { args = []any{nil} }
					heapStatsBoth(t, p, b, c, fixture.Select("SELECT s.* FROM hs_part."+m.name+"("+arg+") s", []string{"s"}, []string{"INT8"}, [][]any{{nil}}, args...))
					heapStatsBoth(t, p, b, c, fixture.Select("SELECT s.* FROM hs_part."+m.name+"("+arg+") s(pages)", []string{"pages"}, []string{"INT8"}, [][]any{{nil}}, args...))
				}
			} else {
				heapStatsBoth(t, p, b, c, pgstatindexError(q, "42501", "permission denied for function "+m.name, nil))
			}
		}
		denied(0, "hs_part.missing")
		denied(3, "hs_part.missing")
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Each isolated EXECUTE grant is sufficient without table SELECT, and
		// does not grant a sibling overload. Revoke must affect held consumers.
		for i, m := range members {
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION hs_part."+m.name+"("+m.typ+") TO hs_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION hs_reader", 0))
			heapStatsBoth(t, p, b, c, pgstatindexError("SELECT k FROM hs_part.heap", "42501", "permission denied for table heap"))
			for j := range members { if j == i { authorized(j) } else { denied(j, nil) } }
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION hs_part."+m.name+"("+m.typ+") FROM hs_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION hs_reader", 0))
			denied(i, nil)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		for _, role := range []string{"pg_stat_scan_tables", "pg_monitor"} {
			run(fixture.Exec("GRANT "+role+" TO hs_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION hs_reader", 0))
			for i := range members { authorized(i) }
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE "+role+" FROM hs_reader", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION hs_reader", 0))
			for i := range members { denied(i, nil) }
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		// Schema lookup remains ordinary SQL authority, independent of EXECUTE.
		run(fixture.Exec("GRANT pg_stat_scan_tables TO hs_reader", 0))
		run(fixture.Exec("REVOKE USAGE ON SCHEMA hs_part FROM hs_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION hs_reader", 0))
		d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema hs_part")
		d.Position = "8"
		s := fixture.ImplicitError("SELECT hs_part.pg_relpages(NULL::text)", d)
		s.SelectRows = true
		heapStatsBoth(t, p, b, c, s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
