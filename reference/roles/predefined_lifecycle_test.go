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
	predefinedCases(t, []string{"pdef_actor", "pdef_plain", "pdef_victim"}, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("CREATE TABLE t(id INT)")
		s.Run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
		q := "SELECT id FROM t"
		held := predefinedHeld(s, q)
		s.Actor("pdef_actor")
		denied := fixture.RoleReferenceError(q, "42501", "permission denied for table t", "", false, "execute")
		denied.SelectRows = true
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, denied)
		s.Actor("")
		s.Fail("GRANT pg_read_all_data,pg_database_owner TO pdef_actor", "0A000", `role "pg_database_owner" cannot have explicit members`, "")
		s.Actor("pdef_actor")
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, denied)
		s.Actor("")
		s.Exec("GRANT pg_read_all_data TO pdef_actor")
		s.Actor("pdef_actor")
		allowed := fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, allowed)
		s.Exec("SET ROLE pg_read_all_data")
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, allowed)
		s.Actor("")
		s.Exec("REVOKE pg_read_all_data FROM pdef_actor")
		s.Actor("pdef_actor")
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, denied)
		s.Actor("")
		s.Exec("GRANT pg_read_all_data TO pdef_actor")
		s.Exec("GRANT pdef_plain TO pdef_actor")
		s.Fail("DROP ROLE pdef_victim,pg_read_all_data", "2BP01", "cannot drop role pg_read_all_data because it is required by the database system", "")
		s.Actor("pdef_actor")
		s.Exec("SET ROLE pdef_plain")
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, denied)
		s.Exec("RESET ROLE")
		fixture.RunStep(s.T, s.P.Ctx, held, s.Record, allowed)
		s.Actor("")
		s.Exec("SET ROLE pdef_victim")
		s.Exec("RESET ROLE")
		if s.Backend.PG {
			// A pre-canceled Go context can race a successful fast pq command.
			// Prove PostgreSQL rollback instead; do not label this cancellation.
			tx, err := s.C.BeginTx(s.P.Ctx, nil)
			if err != nil {
				s.T.Fatal(err)
			}
			defer tx.Rollback()
			step := fixture.Exec("GRANT pg_write_all_data TO pdef_actor", 0)
			step.Prepared = s.Prepared
			fixture.RunStep(s.T, s.P.Ctx, tx, s.Record, step)
			if err := tx.Rollback(); err != nil {
				s.T.Fatal(err)
			}
		} else {
			ctx, cancel := context.WithCancel(s.P.Ctx)
			cancel()
			var result sql.Result
			var err error
			if s.Prepared {
				stmt, prepareErr := s.C.PrepareContext(s.P.Ctx, "GRANT pg_write_all_data TO pdef_actor")
				if prepareErr != nil {
					s.T.Fatal(prepareErr)
				}
				result, err = stmt.ExecContext(ctx)
				if closeErr := stmt.Close(); closeErr != nil {
					s.T.Fatal(closeErr)
				}
			} else {
				result, err = s.C.ExecContext(ctx, "GRANT pg_write_all_data TO pdef_actor")
			}
			if result != nil || !errors.Is(err, context.Canceled) {
				s.T.Fatal("canceled native membership", result, err)
			}
		}
		s.Actor("pdef_actor")
		predefinedDenied(s, "INSERT INTO t VALUES(8)", "permission denied for table t", false)
		s.Actor("")
		if s.Backend.PG {
			return
		} // Host controls have no PostgreSQL API.
		predefinedNativeLifecycle(s)
	})
}

func predefinedNativeLifecycle(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	s.Exec("CREATE SCHEMA pdef_owner AUTHORIZATION pg_database_owner")
	s.Exec("SET ROLE pg_database_owner")
	s.Exec("CREATE TABLE pdef_owner.owned(id INT)")
	s.Run(fixture.Exec("INSERT INTO pdef_owner.owned VALUES(7)", 1))
	s.Exec("RESET ROLE")
	s.Exec("GRANT pg_monitor TO pdef_actor")
	s.Exec("REVOKE pg_read_all_stats FROM pg_monitor")
	s.Actor("pdef_actor")
	predefinedDenied(s, "SET ROLE pg_read_all_stats", `permission denied to set role "pg_read_all_stats"`, false)
	rows, err := s.C.QueryContext(s.P.Ctx, "SELECT id FROM pdef_owner.owned")
	if err != nil {
		s.T.Fatal(err)
	}
	defer rows.Close()
	identities, err := s.C.QueryContext(s.P.Ctx, "SELECT session_user,current_user")
	if err != nil {
		s.T.Fatal(err)
	}
	defer identities.Close()
	s.Actor("")
	s.Exec("REVOKE pg_read_all_data FROM pdef_actor")
	s.Actor("pdef_actor")
	predefinedFailure(s, "SELECT id FROM pdef_owner.owned", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_owner", Position: "16"}, true, "prepare")
	s.Actor("")
	s.Exec("GRANT pg_read_all_data TO pdef_actor")
	s.Actor("pdef_actor")
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
		s.T.Fatal(err)
	}
	for _, ddl := range []string{"CREATE ROLE", "GRANT ", "REVOKE ", "ALTER GROUP"} {
		if strings.Contains(dump.String(), ddl) {
			s.T.Fatal("cluster authority serialized", dump.String())
		}
	}
	if s.P.Postgres != nil {
		// Consume the actual native script on one pinned PostgreSQL session.
		replay, err := s.P.Postgres.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer replay.Close()
		defer replay.ExecContext(s.P.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(s.T, s.P.Ctx, replay, s.P.PostgresRecord, fixture.Exec(dump.String(), 1))
		fixture.RunStep(s.T, s.P.Ctx, replay, s.P.PostgresRecord, fixture.Select("SELECT id FROM pdef_owner.owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		fixture.RunStep(s.T, s.P.Ctx, replay, s.P.PostgresRecord, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		fixture.RunStep(s.T, s.P.Ctx, replay, s.P.PostgresRecord, fixture.Exec("DROP SCHEMA pdef_owner CASCADE", 0))
		fixture.RunStep(s.T, s.P.Ctx, replay, s.P.PostgresRecord, fixture.Exec("DROP TABLE t", 0))
	}
	failure := errors.New("predefined fixture input failure")
	if err := stuntgres.Restore(s.P.Ctx, s.P.Native, predefinedErrorReader{failure}); !errors.Is(err, failure) {
		s.T.Fatal("restore I/O", err)
	}
	predefinedSelect(s, "SELECT id FROM pdef_owner.owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
	ctx, cancel := context.WithCancel(s.P.Ctx)
	defer cancel()
	reader := &predefinedCancelReader{cancel, strings.NewReader(dump.String())}
	if err := stuntgres.Restore(ctx, s.P.Native, reader); !errors.Is(err, context.Canceled) {
		s.T.Fatal("restore final cancellation", err)
	}
	predefinedSelect(s, "SELECT id FROM pdef_owner.owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
	peer, err := s.P.Native.Conn(s.P.Ctx)
	if err != nil {
		s.T.Fatal(err)
	}
	defer peer.Close()
	pending := false
	fence := &predefinedFenceReader{source: strings.NewReader(dump.String()), prepare: func() {
		fixture.LockExec(s.T, s.P.Ctx, peer, "BEGIN", 0)
		fixture.LockExec(s.T, s.P.Ctx, peer, "INSERT INTO t VALUES(9)", 1)
		fixture.LockExec(s.T, s.P.Ctx, peer, "PREPARE TRANSACTION 'pdef_fence'", 0)
		pending = true
	}}
	defer func() {
		if pending {
			peer.ExecContext(s.P.Ctx, "ROLLBACK PREPARED 'pdef_fence'")
		}
	}()
	fixture.TransactionOrdinary(s.T, stuntgres.Restore(s.P.Ctx, s.P.Native, fence), "stuntgres: cannot replace catalog while prepared transactions exist")
	predefinedSelect(s, "SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
	fixture.LockExec(s.T, s.P.Ctx, peer, "ROLLBACK PREPARED 'pdef_fence'", 0)
	pending = false
	if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
		s.T.Fatal(err)
	}
	predefinedSelect(s, "SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"pdef_actor", "pdef_actor"}})
	predefinedDenied(s, "SET ROLE pg_read_all_stats", `permission denied to set role "pg_read_all_stats"`, false)
	s.Actor("")
	s.Exec("SET ROLE pg_database_owner")
	s.Exec("COMMENT ON SCHEMA public IS 'implicit owner after reset'")
	s.Exec("RESET ROLE")
	if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(dump.String())); err != nil {
		s.T.Fatal(err)
	}
	s.Actor("pdef_actor")
	predefinedSelect(s, "SELECT id FROM pdef_owner.owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
	predefinedDenied(s, "SET ROLE pg_read_all_stats", `permission denied to set role "pg_read_all_stats"`, false)
	fixture.RunStep(s.T, s.P.Ctx, predefinedHeld(s, "SELECT id FROM t"), s.Record, fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	fixture.ScalarRows(s.T, false, rows, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}, false)
	fixture.ScalarRows(s.T, false, identities, []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"pdef_actor", "pdef_actor"}}, false)
	s.Actor("")
	s.Exec("SET ROLE pg_database_owner")
	s.Exec("ALTER TABLE pdef_owner.owned ADD COLUMN v TEXT")
	s.Exec("RESET ROLE")
	// Fixed builtin ownership remaps into an independent Driver's authority.
	connector, err := stuntgres.NewDriver().OpenConnector("predefined-independent")
	if err != nil {
		s.T.Fatal(err)
	}
	fresh := sql.OpenDB(connector)
	defer fresh.Close()
	if err := stuntgres.Restore(s.P.Ctx, fresh, strings.NewReader(dump.String())); err != nil {
		s.T.Fatal(err)
	}
	freshPair := &fixture.Pair{Ctx: s.P.Ctx, NativeConnector: connector.(*stuntgres.Connector), MemoryRecord: &fixture.Recorder{RawBackend: fixture.RawNative}}
	var freshNotices []fixture.Diagnostic
	_, freshConn, record := fixture.NoticeReferencePool(s.T, freshPair, false, &freshNotices)
	fixture.RunStep(s.T, s.P.Ctx, freshConn, record, fixture.Exec("SET ROLE pg_database_owner", 0))
	fixture.RunStep(s.T, s.P.Ctx, freshConn, record, fixture.Select("SELECT id FROM pdef_owner.owned", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	fixture.RunStep(s.T, s.P.Ctx, freshConn, record, fixture.Exec("ALTER TABLE pdef_owner.owned ADD COLUMN remapped INT", 0))
}
