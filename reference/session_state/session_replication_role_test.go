package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

const replicationInsertSQL = "INSERT INTO srr_data VALUES($1,5) RETURNING id,v"

func replicationSetup(s *physicalCase) func(int64, int64) {
	s.t.Helper()
	for _, query := range []string{
		"CREATE TABLE srr_data(id integer PRIMARY KEY,v integer)",
		"CREATE FUNCTION srr_add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+100; RETURN NEW; END$$",
		"CREATE TRIGGER srr_before BEFORE INSERT ON srr_data FOR EACH ROW EXECUTE FUNCTION srr_add()",
	} {
		s.run(fixture.Exec(query, 0))
	}
	// This one handle predates every setting change and transaction boundary.
	stmt, err := s.conn.PrepareContext(s.p.Ctx, replicationInsertSQL)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { stmt.Close() })
	return func(id, value int64) {
		s.t.Helper()
		step := fixture.Select(replicationInsertSQL, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{id, value}}, id)
		fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.conn, Statement: stmt, SQL: replicationInsertSQL}, s.recorder, step)
	}
}

func replicationReads(s *physicalCase, value, source string) {
	s.t.Helper()
	for _, read := range []struct{ query, column string }{
		{"SHOW session_replication_role", "session_replication_role"},
		{"SELECT current_setting('session_replication_role')", "current_setting"},
	} {
		step := clientText(read.query, read.column, value)
		step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			settingsTextMetadata(t, rows, s.pg, read.column == "current_setting")
		}
		s.run(step)
	}
	want := []any{"session_replication_role", value, nil, "Client Connection Defaults / Statement Behavior", "Sets the session's behavior for triggers and rewrite rules.", nil, "superuser", "enum", source, nil, nil, "{origin,replica,local}", "origin", "origin", nil, nil, false}
	step := fixture.Select("SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE name='session_replication_role'", physicalColumns, physicalTypes, [][]any{want})
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		statisticsSettingsCheckMetadata(t, rows, s.pg, physicalColumns, physicalTypes)
	}
	s.run(step)
}

func replicationDirect(s *physicalCase, id, value int64) {
	s.t.Helper()
	s.run(fixture.Select(fmt.Sprintf("INSERT INTO srr_data VALUES(%d,5) RETURNING id,v", id), []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{id, value}}))
}

func replicationRows(s *physicalCase, rows ...[]any) {
	s.t.Helper()
	s.run(fixture.Select("SELECT id,v FROM srr_data ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, rows))
}

// The constants describe ordinary enabled-trigger behavior independently of the
// implementation's trigger-mode selector. local is distinct metadata, but has
// the same logical trigger effect as origin.
func replicationValueCycle(s *physicalCase, held func(int64, int64)) {
	s.t.Helper()
	replicationDirect(s, 1, 105)
	held(2, 105)
	replicationReads(s, "origin", "default")
	s.run(fixture.Exec("SET session_replication_role=replica", 0))
	replicationDirect(s, 3, 5)
	held(4, 5)
	replicationReads(s, "replica", "session")
	s.run(fixture.Exec("SET session_replication_role=local", 0))
	replicationDirect(s, 5, 105)
	held(6, 105)
	replicationReads(s, "local", "session")
	replicationRows(s, []any{int64(1), int64(105)}, []any{int64(2), int64(105)}, []any{int64(3), int64(5)}, []any{int64(4), int64(5)}, []any{int64(5), int64(105)}, []any{int64(6), int64(105)})
	s.run(fixture.Exec("RESET session_replication_role", 0))
	replicationReads(s, "origin", "default")
}

func TestReferenceSessionReplicationRoleAcceptance(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		held := replicationSetup(s)
		defer s.conn.ExecContext(s.p.Ctx, "ROLLBACK")
		replicationValueCycle(s, held)

		pool := s.p.Memory
		if s.pg {
			pool = s.p.Postgres
		}
		peer, err := pool.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.Close()
		other := &physicalCase{s.t, s.p, peer, s.pg, s.recorder}
		s.run(fixture.Exec("SET session_replication_role=replica", 0))
		replicationReads(s, "replica", "session")
		held(10, 5)
		replicationReads(other, "origin", "default")
		replicationDirect(other, 11, 105)
		s.run(fixture.Exec("RESET session_replication_role", 0))
		replicationReads(s, "origin", "default")

		s.run(fixture.Exec("BEGIN", 0))
		replicationReads(s, "origin", "default")
		s.run(fixture.Exec("SET session_replication_role=replica", 0))
		replicationReads(s, "replica", "session")
		held(20, 5)
		s.run(fixture.Exec("ROLLBACK", 0))
		replicationReads(s, "origin", "default")
		held(21, 105)

		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("SAVEPOINT srr_save", 0))
		replicationReads(s, "origin", "default")
		s.run(fixture.Exec("SET LOCAL session_replication_role=replica", 0))
		replicationReads(s, "replica", "session")
		held(30, 5)
		s.run(fixture.Exec("ROLLBACK TO srr_save", 0))
		replicationReads(s, "origin", "default")
		held(31, 105)
		s.run(fixture.Exec("COMMIT", 0))
		replicationReads(s, "origin", "default")

		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("SET LOCAL session_replication_role=replica", 0))
		replicationReads(s, "replica", "session")
		held(40, 5)
		s.run(fixture.Exec("COMMIT", 0))
		replicationReads(s, "origin", "default")
		held(41, 105)

		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("SET session_replication_role=replica", 0))
		replicationReads(s, "replica", "session")
		s.run(fixture.Exec("COMMIT", 0))
		replicationReads(s, "replica", "session")
		held(50, 5)
		s.run(fixture.Exec("RESET session_replication_role", 0))
		replicationReads(s, "origin", "default")
		held(51, 105)
		replicationReads(other, "origin", "default")
		// The complete ordered data also proves rollback removed ids 20 and 30,
		// while COMMIT retained both the LOCAL insertion and ordinary SET effect.
		replicationRows(s,
			[]any{int64(1), int64(105)}, []any{int64(2), int64(105)},
			[]any{int64(3), int64(5)}, []any{int64(4), int64(5)},
			[]any{int64(5), int64(105)}, []any{int64(6), int64(105)},
			[]any{int64(10), int64(5)}, []any{int64(11), int64(105)},
			[]any{int64(21), int64(105)}, []any{int64(31), int64(105)},
			[]any{int64(40), int64(5)}, []any{int64(41), int64(105)},
			[]any{int64(50), int64(5)}, []any{int64(51), int64(105)})
	})
}

func TestReferenceSessionReplicationRoleFeatureSmoke(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		held := replicationSetup(s)
		defer s.conn.ExecContext(s.p.Ctx, "ROLLBACK")
		replicationValueCycle(s, held)
		s.run(fixture.Exec("CREATE TABLE srr_parent(id integer PRIMARY KEY)", 0))
		s.run(fixture.Exec("CREATE TABLE srr_child(id integer PRIMARY KEY,pid integer,CONSTRAINT srr_fk FOREIGN KEY(pid) REFERENCES srr_parent(id))", 0))
		diagnostic := fixture.ForeignKeyChild("srr_child", "srr_fk", `Key (pid)=(99) is not present in table "srr_parent".`)
		s.run(fixture.ForeignKeyFail("INSERT INTO srr_child VALUES(1,99)", diagnostic, false))
		s.run(fixture.Exec("SET session_replication_role=replica", 0))
		replicationReads(s, "replica", "session")
		s.run(fixture.Exec("INSERT INTO srr_child VALUES(1,99)", 1))
		s.run(fixture.Exec("SET session_replication_role=origin", 0))
		replicationReads(s, "origin", "session")
		s.run(fixture.Select("SELECT id,pid FROM srr_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(99)}}))
		s.run(fixture.ForeignKeyFail("INSERT INTO srr_child VALUES(2,99)", diagnostic, false))
		s.run(fixture.Select("SELECT id,pid FROM srr_child ORDER BY id", []string{"id", "pid"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(99)}}))

		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("SAVEPOINT srr_save", 0))
		replicationReads(s, "origin", "session")
		s.run(fixture.Exec("SET LOCAL session_replication_role=replica", 0))
		replicationReads(s, "replica", "session")
		held(10, 5)
		s.run(fixture.Exec("ROLLBACK TO srr_save", 0))
		replicationReads(s, "origin", "session")
		held(11, 105)
		s.run(fixture.Exec("COMMIT", 0))
		replicationReads(s, "origin", "session")
		replicationRows(s,
			[]any{int64(1), int64(105)}, []any{int64(2), int64(105)},
			[]any{int64(3), int64(5)}, []any{int64(4), int64(5)},
			[]any{int64(5), int64(105)}, []any{int64(6), int64(105)},
			[]any{int64(11), int64(105)})
	})
}
