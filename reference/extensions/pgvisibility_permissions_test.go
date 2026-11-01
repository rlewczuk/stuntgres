package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pv_owner", "pv_reader"})
	pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE ROLE pv_owner", "CREATE ROLE pv_reader",
			"GRANT USAGE ON SCHEMA pv_part TO pv_owner,pv_reader",
			"ALTER TABLE pv_part.heap OWNER TO pv_owner",
			"CREATE TABLE pv_part.inputs(n text,z integer)",
			"GRANT SELECT ON pv_part.inputs TO pv_owner,pv_reader",
		} { pvRun(t, p, b, c, fixture.Exec(q, 0)) }
		pvRun(t, p, b, c, fixture.Exec("INSERT INTO pv_part.inputs VALUES(NULL,0)", 1))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		members := []struct{ name, signature, suffix string; names, types []string; set bool }{
			{"pg_visibility_map", "regclass,bigint", ",0", pvMapNames, pvMapTypes, false},
			{"pg_visibility", "regclass,bigint", ",0", pvPageNames, pvPageTypes, false},
			{"pg_visibility_map", "regclass", "", pvMapRowsNames, pvMapRowsTypes, true},
			{"pg_visibility", "regclass", "", pvPageRowsNames, pvPageRowsTypes, true},
			{"pg_visibility_map_summary", "regclass", "", pvMapNames, pvSummaryTypes, false},
		}
		held := make([]fixture.HeldStatement, len(members))
		failing := make([]fixture.HeldStatement, len(members))
		for i, m := range members {
			for j, q := range []string{
				"SELECT s.* FROM pv_part.inputs i CROSS JOIN LATERAL pv_part."+m.name+"(COALESCE($1::text,i.n)::regclass"+m.suffix+") s",
				"SELECT s.* FROM pv_part.inputs i CROSS JOIN LATERAL pv_part."+m.name+"(CASE WHEN i.z=0 THEN (1/i.z)::oid::regclass ELSE NULL::regclass END"+m.suffix+") s",
			} {
				st, err := c.PrepareContext(p.Ctx, q)
				if err != nil { t.Fatal(err) }; defer st.Close()
				h := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
				if j==0 { held[i]=h } else { failing[i]=h }
			}
		}
		denied := func(i int) {
			for _, arg := range []any{nil, "pv_part.heap"} {
				s := pvError(held[i].SQL, "42501", "permission denied for function "+members[i].name, arg)
				pvBoth(t, p, b, c, s); fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
			}
			s := pvError(failing[i].SQL, "42501", "permission denied for function "+members[i].name)
			pvBoth(t, p, b, c, s); fixture.RunStep(t, p.Ctx, failing[i], b.Recorder, s)
		}
		authorized := func(i int) {
			m := members[i]
			for _, arg := range []any{nil, "pv_part.heap"} {
				var expected [][]any
				if !m.set {
					expected = [][]any{make([]any, len(m.names))}
					if arg != nil { for j := range expected[0] { if m.name=="pg_visibility_map_summary" { expected[0][j]=int64(0) } else { expected[0][j]=false } } }
				}
				s := fixture.Select(held[i].SQL, m.names, m.types, expected, arg)
				pvBoth(t, p, b, c, s); fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
			}
			s := pvError(failing[i].SQL, "22012", "division by zero")
			pvBoth(t, p, b, c, s); fixture.RunStep(t, p.Ctx, failing[i], b.Recorder, s)
		}
		pvRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION pv_owner", 0))
		pvRun(t, p, b, c, fixture.Select("SELECT k FROM pv_part.heap", []string{"k"}, []string{"INT4"}, nil))
		for i := range members { denied(i) }
		pvRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for i, m := range members {
			pvRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION pv_part."+m.name+"("+m.signature+") TO pv_reader", 0))
			pvRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION pv_reader", 0))
			pvBoth(t, p, b, c, pvError("SELECT k FROM pv_part.heap", "42501", "permission denied for table heap"))
			for j := range members { if i==j { authorized(j) } else { denied(j) } }
			pvRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			pvRun(t, p, b, c, fixture.Exec("REVOKE EXECUTE ON FUNCTION pv_part."+m.name+"("+m.signature+") FROM pv_reader", 0))
			pvRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION pv_reader", 0)); denied(i)
			pvRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		for _, role := range []string{"pg_stat_scan_tables", "pg_monitor"} {
			pvRun(t, p, b, c, fixture.Exec("GRANT "+role+" TO pv_reader", 0))
			pvRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION pv_reader", 0))
			for i := range members { authorized(i) }
			pvRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			pvRun(t, p, b, c, fixture.Exec("REVOKE "+role+" FROM pv_reader", 0))
		}
		// PG18 membership inheritance is per-grant. NOINHERIT must be in force
		// before GRANT; SET ROLE still gives the genuine scan role's authority.
		for _, q := range []string{"ALTER ROLE pv_reader NOINHERIT", "GRANT pg_stat_scan_tables TO pv_reader", "SET SESSION AUTHORIZATION pv_reader"} { pvRun(t, p, b, c, fixture.Exec(q, 0)) }
		for i := range members { denied(i) }
		pvRun(t, p, b, c, fixture.Exec("SET ROLE pg_stat_scan_tables", 0))
		// The role needs schema USAGE in addition to EXECUTE.
		pvRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
		pvRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		pvRun(t, p, b, c, fixture.Exec("GRANT USAGE ON SCHEMA pv_part TO pg_stat_scan_tables", 0))
		pvRun(t, p, b, c, fixture.Exec("GRANT SELECT ON pv_part.inputs TO pg_stat_scan_tables", 0))
		pvRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION pv_reader", 0))
		pvRun(t, p, b, c, fixture.Exec("SET ROLE pg_stat_scan_tables", 0))
		for i := range members { authorized(i) }
		for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "REVOKE pg_stat_scan_tables FROM pv_reader", "REVOKE USAGE ON SCHEMA pv_part FROM pg_stat_scan_tables", "REVOKE SELECT ON pv_part.inputs FROM pg_stat_scan_tables", "ALTER ROLE pv_reader INHERIT", "GRANT pg_stat_scan_tables TO pv_reader", "REVOKE USAGE ON SCHEMA pv_part FROM pv_reader", "SET SESSION AUTHORIZATION pv_reader"} { pvRun(t, p, b, c, fixture.Exec(q, 0)) }
		s := pvError("SELECT * FROM pv_part.pg_visibility_map_summary(NULL::regclass)", "42501", "permission denied for schema pv_part")
		s.Diagnostic.Position = "15"; pvBoth(t, p, b, c, s)
		pvRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Untrusted extension installation requires actual superuser, regardless
		// of table ownership or membership in monitoring roles.
		pvRun(t, p, b, c, fixture.Exec("DROP EXTENSION pg_visibility", 0))
		pvRun(t, p, b, c, fixture.Exec("GRANT CREATE ON SCHEMA pv_part TO pv_reader", 0))
		pvRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION pv_reader", 0))
		s = pvError("CREATE EXTENSION pg_visibility SCHEMA pv_part", "42501", "permission denied to create extension \"pg_visibility\"")
		s.SelectRows = false; s.Diagnostic.Hint = "Must be superuser to create this extension."
		pvRun(t, p, b, c, s)
		pvRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		pvRun(t, p, b, c, fixture.Exec("CREATE EXTENSION pg_visibility SCHEMA pv_part", 0))
	})
}
