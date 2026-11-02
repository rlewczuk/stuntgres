package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGINPendingListLimitSettings(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		peer := s.ginPeer()
		s.ginState("4MB", "4096", "default", false)
		peer.ginState("4MB", "4096", "default", true)
		const readSQL = "SELECT current_setting('gin_pending_list_limit')"
		reader, err := s.conn.PrepareContext(s.p.Ctx, readSQL)
		if err != nil {
			s.t.Fatal(err)
		}
		defer reader.Close()
		const setSQL = "SET gin_pending_list_limit='8MB'"
		setter, err := s.conn.PrepareContext(s.p.Ctx, setSQL)
		if err != nil {
			s.t.Fatal(err)
		}
		defer setter.Close()
		// Neither a held read nor a held assignment changes the setting at Prepare.
		s.ginState("4MB", "4096", "default", true)
		s.run(fixture.Exec("SET gin_pending_list_limit='64kB'", 0))
		s.ginState("64kB", "64", "session", true)
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: reader, SQL: readSQL}, s.recorder, toastText(s.pg, readSQL, "current_setting", "64kB"))
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: setter, SQL: setSQL}, s.recorder, fixture.Exec(setSQL, 0))
		s.ginState("8MB", "8192", "session", false)
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: reader, SQL: readSQL}, s.recorder, toastText(s.pg, readSQL, "current_setting", "8MB"))
		peer.ginState("4MB", "4096", "default", false)
		step := toastText(s.pg, "SELECT set_config('gin_pending_list_limit',$1,false)", "set_config", "64kB")
		step.Args = []any{"64kB"}
		s.run(fixture.Prepared(step))
		s.ginState("64kB", "64", "session", false)
		s.run(fixture.Prepared(fixture.Exec("RESET gin_pending_list_limit", 0)))
		s.ginState("4MB", "4096", "default", true)
	})
}

func TestReferenceGINPendingListLimitTransactions(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		peer := s.ginPeer()
		s.ginState("4MB", "4096", "default", false)
		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("SET gin_pending_list_limit='8MB'", 0))
		s.run(fixture.Exec("SAVEPOINT gin_limit_save", 0))
		s.run(fixture.Prepared(fixture.Exec("SET LOCAL gin_pending_list_limit='64kB'", 0)))
		s.ginState("64kB", "64", "session", false)
		peer.ginState("4MB", "4096", "default", true)
		s.run(fixture.Exec("ROLLBACK TO gin_limit_save", 0))
		s.ginState("8MB", "8192", "session", true)
		s.run(fixture.Exec("ROLLBACK", 0))
		s.ginState("4MB", "4096", "default", false)
		for _, q := range []string{"BEGIN", "SET gin_pending_list_limit='8MB'", "COMMIT"} {
			s.run(fixture.Exec(q, 0))
		}
		s.ginState("8MB", "8192", "session", false)
		for _, local := range []string{"SET LOCAL gin_pending_list_limit='64kB'", "SELECT set_config('gin_pending_list_limit','64kB',true)"} {
			s.run(fixture.Exec("BEGIN", 0))
			if local[0] == 'S' && local[1] == 'E' && local[2] == 'L' {
				s.run(toastText(s.pg, local, "set_config", "64kB"))
			} else {
				s.run(fixture.Exec(local, 0))
			}
			s.ginState("64kB", "64", "session", true)
			s.run(fixture.Exec("COMMIT", 0))
			s.ginState("8MB", "8192", "session", false)
		}
		s.run(fixture.Exec("CREATE TABLE gin_limit_tx(id integer,a integer[])", 0))
		s.run(fixture.Exec("CREATE INDEX gin_limit_tx_i ON gin_limit_tx USING gin(a) WITH(fastupdate=on)", 0))
		s.run(fixture.Exec("INSERT INTO gin_limit_tx VALUES(1,'{1,2,3}'::integer[])", 1))
		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("SAVEPOINT gin_limit_error", 0))
		s.run(physicalError("SET gin_pending_list_limit='63'", ginLimitInvalid("63"), true, false))
		s.run(fixture.Exec("ROLLBACK TO gin_limit_error", 0))
		s.ginState("8MB", "8192", "session", true)
		if !s.pg {
			s.run(ginLimitDemand(false, "SELECT gin_clean_pending_list('gin_limit_tx_i'::regclass) AS cleaned", "INT8", nil))
			s.run(fixture.Exec("ROLLBACK TO gin_limit_error", 0))
			s.ginState("8MB", "8192", "session", false)
		}
		s.run(fixture.Select("SELECT id,a::text AS a FROM gin_limit_tx WHERE a @> '{1,3}'::integer[]", []string{"id", "a"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "{1,2,3}"}}))
		s.run(fixture.Exec("COMMIT", 0))
		s.ginState("8MB", "8192", "session", false)
		peer.ginState("4MB", "4096", "default", false)
	})
}
