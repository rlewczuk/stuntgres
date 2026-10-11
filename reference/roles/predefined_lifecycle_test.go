package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

type predefinedErrorReader struct{ err error }

func (r predefinedErrorReader) Read([]byte) (int, error) { return 0, r.err }

type predefinedCancelReader struct {
	cancel context.CancelFunc
	source *strings.Reader
}

func (r *predefinedCancelReader) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	if err == io.EOF {
		r.cancel()
	}
	return n, err
}

type predefinedFenceReader struct {
	source   *strings.Reader
	prepare  func()
	prepared bool
}

func (r *predefinedFenceReader) Read(p []byte) (int, error) {
	if !r.prepared {
		r.prepared = true
		r.prepare()
	}
	return r.source.Read(p)
}

func TestReferencePredefinedLifecycle(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor", "pdef_plain", "pdef_victim"}, func(s *membershipScenario, db *sql.DB) {
		s.exec("CREATE TABLE t(id INT)")
		s.run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		q := "SELECT id FROM t"
		held := predefinedHeld(s, q)
		s.actor("pdef_actor")
		denied := fixture.RoleReferenceError(q, "42501", "permission denied for table t", "", false, "execute")
		denied.SelectRows = true
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, denied)
		s.actor("")
		s.fail("GRANT pg_read_all_data,pg_database_owner TO pdef_actor", "0A000", `role "pg_database_owner" cannot have explicit members`, "")
		s.actor("pdef_actor")
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, denied)
		s.actor("")
		s.exec("GRANT pg_read_all_data TO pdef_actor")
		s.actor("pdef_actor")
		allowed := fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, allowed)
		s.exec("SET ROLE pg_read_all_data")
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, allowed)
		s.actor("")
		s.exec("REVOKE pg_read_all_data FROM pdef_actor")
		s.actor("pdef_actor")
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, denied)
		s.actor("")
		s.exec("GRANT pg_read_all_data TO pdef_actor")
		s.exec("GRANT pdef_plain TO pdef_actor")
		s.fail("DROP ROLE pdef_victim,pg_read_all_data", "2BP01", "cannot drop role pg_read_all_data because it is required by the database system", "")
		s.actor("pdef_actor")
		s.exec("SET ROLE pdef_plain")
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, denied)
		s.exec("RESET ROLE")
		fixture.RunStep(s.t, s.p.Ctx, held, s.record, allowed)
		s.actor("")
		s.exec("SET ROLE pdef_victim")
		s.exec("RESET ROLE")
		if s.backend.PG {
			// A pre-canceled Go context can race a successful fast pq command.
			// Prove PostgreSQL rollback instead; do not label this cancellation.
			tx, err := s.c.BeginTx(s.p.Ctx, nil)
			if err != nil {
				s.t.Fatal(err)
			}
			defer tx.Rollback()
			step := fixture.Exec("GRANT pg_write_all_data TO pdef_actor", 0)
			step.Prepared = s.prepared
			fixture.RunStep(s.t, s.p.Ctx, tx, s.record, step)
			if err := tx.Rollback(); err != nil {
				s.t.Fatal(err)
			}
		} else {
			ctx, cancel := context.WithCancel(s.p.Ctx)
			cancel()
			var result sql.Result
			var err error
			if s.prepared {
				stmt, prepareErr := s.c.PrepareContext(s.p.Ctx, "GRANT pg_write_all_data TO pdef_actor")
				if prepareErr != nil {
					s.t.Fatal(prepareErr)
				}
				result, err = stmt.ExecContext(ctx)
				if closeErr := stmt.Close(); closeErr != nil {
					s.t.Fatal(closeErr)
				}
			} else {
				result, err = s.c.ExecContext(ctx, "GRANT pg_write_all_data TO pdef_actor")
			}
			if result != nil || !errors.Is(err, context.Canceled) {
				s.t.Fatal("canceled native membership", result, err)
			}
		}
		s.actor("pdef_actor")
		predefinedDenied(s, "INSERT INTO t VALUES(8)", "permission denied for table t", false)
		s.actor("")
		if s.backend.PG {
			return
		} // Host controls have no PostgreSQL API.
		predefinedNativeLifecycle(s)
	})
}

func predefinedNativeLifecycle(s *membershipScenario) {
	s.t.Helper()
	s.exec("CREATE SCHEMA pdef_owner AUTHORIZATION pg_database_owner")
	s.exec("SET ROLE pg_database_owner")
	s.exec("CREATE TABLE pdef_owner.owned(id INT)")
	s.run(fixture.Exec("INSERT INTO pdef_owner.owned VALUES(7)", 1))
	s.exec("RESET ROLE")
	s.exec("GRANT pg_monitor TO pdef_actor")
	s.exec("REVOKE pg_read_all_stats FROM pg_monitor")
	s.actor("pdef_actor")
	predefinedDenied(s, "SET ROLE pg_read_all_stats", `permission denied to set role "pg_read_all_stats"`, false)
	rows, err := s.c.QueryContext(s.p.Ctx, "SELECT id FROM pdef_owner.owned")
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	identities, err := s.c.QueryContext(s.p.Ctx, "SELECT session_user,current_user")
	if err != nil {
		s.t.Fatal(err)
	}
	defer identities.Close()
	s.actor("")
	s.exec("REVOKE pg_read_all_data FROM pdef_actor")
	s.actor("pdef_actor")
	predefinedFailure(s, "SELECT id FROM pdef_owner.owned", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_owner", Position: "16"}, true, "prepare")
	s.actor("")
	s.exec("GRANT pg_read_all_data TO pdef_actor")
	s.actor("pdef_actor")
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
		s.t.Fatal(err)
	}
	for _, ddl := range []string{"CREATE ROLE", "GRANT ", "REVOKE ", "ALTER GROUP"} {
		if strings.Contains(dump.String(), ddl) {
			s.t.Fatal("cluster authority serialized", dump.String())
		}
	}
	if s.p.Postgres != nil {
		// Consume the actual native script on one pinned PostgreSQL session.
		replay, err := s.p.Postgres.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer replay.Close()
		defer replay.ExecContext(s.p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(s.t, s.p.Ctx, replay, s.p.PostgresRecord, fixture.Exec(dump.String(), 1))
		fixture.RunStep(s.t, s.p.Ctx, replay, s.p.PostgresRecord, fixture.Select("SELECT id FROM pdef_owner.owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		fixture.RunStep(s.t, s.p.Ctx, replay, s.p.PostgresRecord, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		fixture.RunStep(s.t, s.p.Ctx, replay, s.p.PostgresRecord, fixture.Exec("DROP SCHEMA pdef_owner CASCADE", 0))
		fixture.RunStep(s.t, s.p.Ctx, replay, s.p.PostgresRecord, fixture.Exec("DROP TABLE t", 0))
	}
	failure := errors.New("predefined fixture input failure")
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, predefinedErrorReader{failure}); !errors.Is(err, failure) {
		s.t.Fatal("restore I/O", err)
	}
	predefinedSelect(s, "SELECT id FROM pdef_owner.owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
	ctx, cancel := context.WithCancel(s.p.Ctx)
	defer cancel()
	reader := &predefinedCancelReader{cancel, strings.NewReader(dump.String())}
	if err := stuntgres.Restore(ctx, s.p.Native, reader); !errors.Is(err, context.Canceled) {
		s.t.Fatal("restore final cancellation", err)
	}
	predefinedSelect(s, "SELECT id FROM pdef_owner.owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
	peer, err := s.p.Native.Conn(s.p.Ctx)
	if err != nil {
		s.t.Fatal(err)
	}
	defer peer.Close()
	pending := false
	fence := &predefinedFenceReader{source: strings.NewReader(dump.String()), prepare: func() {
		fixture.LockExec(s.t, s.p.Ctx, peer, "BEGIN", 0)
		fixture.LockExec(s.t, s.p.Ctx, peer, "INSERT INTO t VALUES(9)", 1)
		fixture.LockExec(s.t, s.p.Ctx, peer, "PREPARE TRANSACTION 'pdef_fence'", 0)
		pending = true
	}}
	defer func() {
		if pending {
			peer.ExecContext(s.p.Ctx, "ROLLBACK PREPARED 'pdef_fence'")
		}
	}()
	fixture.TransactionOrdinary(s.t, stuntgres.Restore(s.p.Ctx, s.p.Native, fence), "stuntgres: cannot replace catalog while prepared transactions exist")
	predefinedSelect(s, "SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
	fixture.LockExec(s.t, s.p.Ctx, peer, "ROLLBACK PREPARED 'pdef_fence'", 0)
	pending = false
	if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
		s.t.Fatal(err)
	}
	predefinedSelect(s, "SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"pdef_actor", "pdef_actor"}})
	predefinedDenied(s, "SET ROLE pg_read_all_stats", `permission denied to set role "pg_read_all_stats"`, false)
	s.actor("")
	s.exec("SET ROLE pg_database_owner")
	s.exec("COMMENT ON SCHEMA public IS 'implicit owner after reset'")
	s.exec("RESET ROLE")
	if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump.String())); err != nil {
		s.t.Fatal(err)
	}
	s.actor("pdef_actor")
	predefinedSelect(s, "SELECT id FROM pdef_owner.owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
	predefinedDenied(s, "SET ROLE pg_read_all_stats", `permission denied to set role "pg_read_all_stats"`, false)
	fixture.RunStep(s.t, s.p.Ctx, predefinedHeld(s, "SELECT id FROM t"), s.record, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	fixture.ScalarRows(s.t, false, rows, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
	fixture.ScalarRows(s.t, false, identities, []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"pdef_actor", "pdef_actor"}}, false)
	s.actor("")
	s.exec("SET ROLE pg_database_owner")
	s.exec("ALTER TABLE pdef_owner.owned ADD COLUMN v TEXT")
	s.exec("RESET ROLE")
	// Fixed builtin ownership remaps into an independent Driver's authority.
	connector, err := stuntgres.NewDriver().OpenConnector("predefined-independent")
	if err != nil {
		s.t.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	if err := stuntgres.Restore(s.p.Ctx, fresh, strings.NewReader(dump.String())); err != nil {
		s.t.Fatal(err)
	}
	freshPair := &fixture.Pair{Ctx: s.p.Ctx, NativeConnector: connector.(*stuntgres.Connector), MemoryRecord: &fixture.Recorder{RawBackend: fixture.RawNative}}
	var freshNotices []fixture.Diagnostic
	_, freshConn, record := fixture.NoticeReferencePool(s.t, freshPair, false, &freshNotices)
	fixture.RunStep(s.t, s.p.Ctx, freshConn, record, fixture.Exec("SET ROLE pg_database_owner", 0))
	fixture.RunStep(s.t, s.p.Ctx, freshConn, record, fixture.Select("SELECT id FROM pdef_owner.owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	fixture.RunStep(s.t, s.p.Ctx, freshConn, record, fixture.Exec("ALTER TABLE pdef_owner.owned ADD COLUMN remapped INT", 0))
}
