package reference_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func tempTablespacesSameValueAssignment(t *testing.T) {
	physicalCases(t, []string{"tt_same_login"}, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE ROLE tt_same_login LOGIN PASSWORD 'role-core-fixture'", 0))
		var database string
		if err := s.conn.QueryRowContext(s.p.Ctx, "SELECT current_database()").Scan(&database); err != nil { s.t.Fatal(err) }
		s.run(fixture.Exec("GRANT TEMP ON DATABASE "+`"`+database+`"`+" TO tt_same_login", 0))
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, q := range []string{"REVOKE CREATE ON TABLESPACE pg_global FROM tt_same_login", "REVOKE TEMP ON DATABASE "+`"`+database+`"`+" FROM tt_same_login"} {
				if _, err := s.conn.ExecContext(ctx, q); err != nil { s.t.Error(err) }
			}
		}()
		ordinary := s.login("tt_same_login")
		defer ordinary.conn.Close()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := ordinary.conn.ExecContext(ctx, "ROLLBACK"); err != nil { s.t.Error(err) }
			for _, name := range []string{"tt_same_before", "tt_same_0", "tt_same_1"} {
				if _, err := ordinary.conn.ExecContext(ctx, "DROP TABLE IF EXISTS pg_temp."+name); err != nil { s.t.Error(err) }
			}
		}()
		s.run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_global TO tt_same_login", 0))
		const assignmentSQL = "SELECT set_config('temp_tablespaces','pg_global, pg_default',false)"
		ordinary.run(clientText(assignmentSQL, "set_config", "pg_global, pg_default"))
		s.run(fixture.Exec("REVOKE CREATE ON TABLESPACE pg_global FROM tt_same_login", 0))
		ordinary.run(fixture.Exec("BEGIN", 0))
		ordinary.run(fixture.Exec("CREATE TEMP TABLE tt_same_before(v INT)", 0))
		ordinary.run(fixture.Exec("INSERT INTO tt_same_before VALUES(73)", 1))
		// The current transaction now has a filtered singleton/default list.
		// A fresh identical-string assignment must invoke the check/assign hook,
		// installing both now-eligible members. This distinguishes reassignment
		// from raw-string cache reuse without predicting its random first choice.
		s.run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_global TO tt_same_login", 0))
		ordinary.run(clientText(assignmentSQL, "set_config", "pg_global, pg_default"))
		var successes [2]bool
		for i := range successes {
			ordinary.run(fixture.Exec("SAVEPOINT tt_same_attempt", 0))
			result, err := ordinary.conn.ExecContext(s.p.Ctx, fmt.Sprintf("CREATE TEMP TABLE tt_same_%d(v INT)", i))
			if err == nil {
				n, err := result.RowsAffected()
				if err != nil || n != 0 { s.t.Fatalf("same-value attempt %d affected=%d: %v", i, n, err) }
				successes[i] = true
			} else {
				d := defaultTablespaceGlobal()
				fixture.AssertDiagnostic(s.t, err, &d)
				ordinary.run(fixture.Exec("ROLLBACK TO tt_same_attempt", 0))
			}
			ordinary.run(fixture.Exec("RELEASE tt_same_attempt", 0))
		}
		if successes[0] == successes[1] { s.t.Fatal("same-value assignment did not install both eligible members", successes) }
		for i, success := range successes {
			count := int64(0)
			if success { count = 1 }
			ordinary.run(fixture.Select(fmt.Sprintf("SELECT count(*) FROM pg_class WHERE relnamespace=pg_my_temp_schema() AND relname='tt_same_%d'", i), []string{"count"}, []string{"INT8"}, [][]any{{count}}))
			if !success { ordinary.tempTablespacesAbsent(fmt.Sprintf("tt_same_%d", i)) }
		}
		ordinary.tempTablespacesReads("pg_global, pg_default", "session")
		ordinary.run(fixture.Select("SELECT v FROM tt_same_before", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
		ordinary.run(fixture.Exec("COMMIT", 0))
	})
}
