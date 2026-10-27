package reference_test

import (
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferencePlannerSettingsLifecycle(t *testing.T) {
	for _, tc := range []struct{ name, boot, other string }{{"enable_hashjoin","on","off"},{"enable_partitionwise_join","off","on"}} {
		t.Run(tc.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				peer := s.plannerPeer()
				read := func(want string) { s.plannerRead(tc.name, want, false); peer.plannerRead(tc.name, tc.boot, false) }
				exec := func(q string) { s.run(fixture.Exec(q, 0)) }
				read(tc.boot)
				exec("BEGIN"); exec("SET " + tc.name + "=" + tc.other); read(tc.other); exec("ROLLBACK"); read(tc.boot)
				exec("BEGIN"); exec("SET " + tc.name + "=" + tc.other); exec("COMMIT"); read(tc.other)
				exec("RESET " + tc.name); read(tc.boot)
				for _, end := range []string{"COMMIT","ROLLBACK"} {
					exec("BEGIN"); exec("SET LOCAL " + tc.name + "=" + tc.other); read(tc.other); exec(end); read(tc.boot)
				}
				exec("BEGIN"); exec("SAVEPOINT earlier"); exec("SET " + tc.name + "=" + tc.other)
				exec("SAVEPOINT later"); exec("SET LOCAL " + tc.name + "=" + tc.boot); read(tc.boot)
				exec("ROLLBACK TO later"); read(tc.other); exec("RELEASE later"); read(tc.other)
				exec("ROLLBACK TO earlier"); read(tc.boot); exec("COMMIT"); read(tc.boot)
				exec("BEGIN"); exec("SET " + tc.name + "=" + tc.other); exec("SAVEPOINT kept")
				exec("SET LOCAL " + tc.name + "=" + tc.boot); exec("RELEASE kept"); read(tc.boot)
				exec("COMMIT"); read(tc.other); exec("RESET " + tc.name)
				exec("BEGIN")
				s.run(fixture.Prepared(fixture.Select("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{tc.other}}, tc.name, tc.other, true)))
				read(tc.other); exec("COMMIT"); read(tc.boot)
				exec("BEGIN"); exec("SAVEPOINT valid")
				s.run(physicalError("SET " + tc.name + "='bogus'", physicalDiagnostic("22023", "parameter \"" + tc.name + "\" requires a Boolean value"), false, false))
				s.run(physicalError("SHOW " + tc.name, physicalDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"), false, true))
				exec("ROLLBACK TO valid"); read(tc.boot); exec("ROLLBACK"); read(tc.boot)
			})
		})
	}
}

func TestReferencePlannerSettingsAdmission(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		exec := func(q string) { s.run(fixture.Exec(q, 0)) }
		exec("CREATE TABLE pm_admit(k integer)")
		s.run(fixture.Exec("INSERT INTO pm_admit VALUES(2),(1),(1),(NULL)", 4))
		query := "SELECT DISTINCT k FROM pm_admit ORDER BY k NULLS LAST"
		want := [][]any{{int64(1)},{int64(2)},{nil}}
		stmt, err := s.conn.PrepareContext(s.p.Ctx, query)
		if err != nil { s.t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{Executor:s.conn, Statement:stmt, SQL:query}
		for _, value := range []string{"off","on"} {
			exec("SET enable_hashagg=" + value)
			fixture.RunStep(s.t, s.p.Ctx, held, s.recorder, fixture.Select(query, []string{"k"}, []string{"INT4"}, want))
		}
		for _, value:=range []string{"off","on"} {
			exec("SET enable_hashagg="+value)
			step:=fixture.Select("WITH x AS MATERIALIZED (SELECT DISTINCT k FROM pm_admit) SELECT k,(SELECT count(*) FROM pm_admit b WHERE b.k=x.k) AS n FROM x ORDER BY k NULLS LAST",[]string{"k","n"},[]string{"INT4","INT8"},[][]any{{int64(1),int64(2)},{int64(2),int64(1)},{nil,int64(0)}})
			s.run(step); step.Prepared=true; s.run(step)
		}
		// Native eager rows and PostgreSQL's streaming rows both retain their
		// admitted result when a peer changes its own settings.
		peer := s.plannerPeer()
		rows, err := stmt.QueryContext(s.p.Ctx)
		if err != nil { s.t.Fatal(err) }
		defer rows.Close()
		peer.run(fixture.Exec("SET enable_hashagg=off", 0))
		var got []any
		for rows.Next() { var k any; if err := rows.Scan(&k); err != nil { s.t.Fatal(err) }; got = append(got, fixture.Normalize(k)) }
		if err := rows.Err(); err != nil { s.t.Fatal(err) }; if err := rows.Close(); err != nil { s.t.Fatal(err) }
		if len(got) != 3 || got[0] != int64(1) || got[1] != int64(2) || got[2] != nil { s.t.Fatal(got) }
		exec("BEGIN"); exec("SET enable_hashagg=off")
		exec("DECLARE pm_cursor NO SCROLL CURSOR FOR " + query)
		s.run(fixture.Select("FETCH 1 FROM pm_cursor", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		exec("SET enable_hashagg=on")
		s.run(fixture.Select("FETCH ALL FROM pm_cursor", []string{"k"}, []string{"INT4"}, [][]any{{int64(2)},{nil}}))
		exec("CLOSE pm_cursor"); exec("DECLARE pm_new CURSOR FOR " + query)
		s.run(fixture.Select("FETCH ALL FROM pm_new", []string{"k"}, []string{"INT4"}, want))
		exec("CLOSE pm_new"); exec("COMMIT")
		exec("CREATE FUNCTION pm_effective() RETURNS text LANGUAGE SQL SET enable_hashjoin=off AS $$ SELECT current_setting('enable_hashjoin') $$")
		s.run(fixture.Select("SELECT pm_effective()", []string{"pm_effective"}, []string{"TEXT"}, [][]any{{"off"}}))
		s.plannerRead("enable_hashjoin", "on", false)
		exec("CREATE FUNCTION pm_nested() RETURNS text LANGUAGE plpgsql SET enable_hashagg=off AS $$ DECLARE v text; BEGIN SELECT current_setting('enable_hashagg') INTO v; IF v <> 'off' THEN RAISE EXCEPTION 'wrong static setting'; END IF; PERFORM set_config('enable_hashagg','on',false); EXECUTE 'SELECT current_setting(''enable_hashagg'')' INTO v; RETURN v; END $$")
		s.run(fixture.Select("SELECT pm_nested()", []string{"pm_nested"}, []string{"TEXT"}, [][]any{{"on"}}))
		s.plannerRead("enable_hashagg", "on", false)
		exec("CREATE FUNCTION pm_fail() RETURNS integer LANGUAGE plpgsql SET enable_hashjoin=off AS $$ BEGIN RAISE EXCEPTION 'planner failure'; END $$")
		s.run(fixture.Error("SELECT pm_fail()", "P0001"))
		s.plannerRead("enable_hashjoin", "on", false)
		// Each body actually groups duplicate/NULL input. Presentation alone
		// cannot witness the nested SQL admission; private owner tests observe
		// these same hash/equality branches.
		grouped := "SELECT count(*) FROM (SELECT k,count(*) FROM pm_admit GROUP BY k) g"
		exec("CREATE FUNCTION pm_group_effective() RETURNS bigint LANGUAGE SQL SET enable_hashagg=off AS $$ " + grouped + " $$")
		s.run(fixture.Select("SELECT pm_group_effective()", []string{"pm_group_effective"}, []string{"INT8"}, [][]any{{int64(3)}}))
		body := " DECLARE a bigint; b bigint; BEGIN SELECT count(*) INTO a FROM (SELECT k,count(*) FROM pm_admit GROUP BY k) g; PERFORM set_config('enable_hashagg','on',false); EXECUTE 'SELECT count(*) FROM (SELECT k,count(*) FROM pm_admit GROUP BY k) g' INTO b; "
		exec("CREATE FUNCTION pm_group_nested() RETURNS bigint LANGUAGE plpgsql SET enable_hashagg=off AS $$" + body + "RETURN a*10+b; END $$")
		for _, prepared := range []bool{false,true} {
			step := fixture.Select("SELECT pm_group_nested()", []string{"pm_group_nested"}, []string{"INT8"}, [][]any{{int64(33)}})
			step.Prepared = prepared
			s.run(step)
		}
		// The already admitted outer derived grouping remains valid across a
		// detached invocation and a separately admitted changed inner statement.
		s.run(fixture.Select("SELECT k,count(*),pm_group_nested() AS n FROM (SELECT k FROM pm_admit) x GROUP BY k ORDER BY k NULLS LAST", []string{"k","count","n"}, []string{"INT4","INT8","INT8"}, [][]any{{int64(1),int64(2),int64(33)},{int64(2),int64(1),int64(33)},{nil,int64(1),int64(33)}}))
		s.plannerRead("enable_hashagg", "on", false)
		exec("CREATE FUNCTION pm_group_fail() RETURNS bigint LANGUAGE plpgsql SET enable_hashagg=off AS $$" + body + "RAISE EXCEPTION 'planner grouped failure'; END $$")
		s.run(fixture.Error("SELECT pm_group_fail()", "P0001"))
		s.plannerRead("enable_hashagg", "on", false)
		s.run(fixture.Select(query, []string{"k"}, []string{"INT4"}, want))
	})
}

func (s *physicalCase) plannerPeer() *physicalCase {
	s.t.Helper()
	for _, backend := range s.p.Backends() {
		if backend.PG != s.pg {
			continue
		}
		conn, err := backend.DB.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		s.t.Cleanup(func() { conn.Close() })
		return &physicalCase{s.t, s.p, conn, s.pg, backend.Recorder}
	}
	s.t.Fatal("missing scenario backend")
	return nil
}
