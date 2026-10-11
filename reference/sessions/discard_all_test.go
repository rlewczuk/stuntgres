package reference_test

import (
	"database/sql"
	"github.com/lib/pq"
	"net/url"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDiscardTransactionGates(t *testing.T) {
	discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		discardSeed(t, p, b, a, peer)
		t.Run("retained_message", func(t *testing.T) {
			for _, query := range []string{
				"INSERT INTO discard_durable VALUES(10); SET application_name='private'; DISCARD ALL",
				"DISCARD ALL; INSERT INTO discard_durable VALUES(10)",
			} {
				discardError(t, p, b, a, query, "25001", "DISCARD ALL cannot run inside a transaction block", "")
				discardScalar(t, p, b, a, "SELECT v FROM discard_durable", "v", "INT4", int64(9))
				discardScalar(t, p, b, a, "SHOW application_name", "application_name", "TEXT", "discard_7")
				discardScalar(t, p, b, a, "EXECUTE p", "v", "INT4", int64(7))
				discardScalar(t, p, b, a, "SELECT v FROM discard_t", "v", "INT4", int64(7))
				discardScalar(t, p, b, a, "SELECT currval('discard_s')", "currval", "INT8", int64(1))
				discardScalar(t, p, b, peer, "SELECT pg_try_advisory_lock(81001::bigint)", "pg_try_advisory_lock", "BOOL", false)
				discardPeerIntact(t, p, b, peer)
			}
		})
		for _, driverTx := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				var stmt *sql.Stmt
				var err error
				if prepared {
					stmt, err = a.PrepareContext(p.Ctx, "DISCARD ALL")
					if err != nil {
						t.Fatal(err)
					}
				}
				var source fixture.Executor = a
				var tx *sql.Tx
				if driverTx {
					tx, err = a.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					source = tx
				} else {
					discardExec(t, p, b, a, "BEGIN")
				}
				if prepared {
					source = fixture.HeldStatement{Executor: source, Statement: stmt, SQL: "DISCARD ALL"}
				}
				discardError(t, p, b, source, "DISCARD ALL", "25001", "DISCARD ALL cannot run inside a transaction block", "")
				if driverTx {
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
				} else {
					discardExec(t, p, b, a, "ROLLBACK")
				}
				if stmt != nil {
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
				}
				discardScalar(t, p, b, a, "EXECUTE p", "v", "INT4", int64(7))
				discardScalar(t, p, b, a, "SELECT v FROM discard_t", "v", "INT4", int64(7))
				discardScalar(t, p, b, a, "SELECT currval('discard_s')", "currval", "INT8", int64(1))
			}
		}
		discardExec(t, p, b, a, "BEGIN READ ONLY")
		for _, category := range []string{"PLANS", "SEQUENCES", "TEMP"} {
			discardExec(t, p, b, a, "DISCARD "+category)
		}
		discardError(t, p, b, a, "DISCARD ALL", "25001", "DISCARD ALL cannot run inside a transaction block", "")
		discardExec(t, p, b, a, "ROLLBACK")
		for _, category := range []string{"ALL", "PLANS", "SEQUENCES", "TEMP"} {
			discardExec(t, p, b, a, "BEGIN")
			discardError(t, p, b, a, "SELECT 1/0", "22012", "division by zero", "")
			discardError(t, p, b, a, "DISCARD "+category, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
			discardExec(t, p, b, a, "ROLLBACK")
		}
		discardExec(t, p, b, a, "SET default_transaction_read_only=on")
		discardExec(t, p, b, a, "DISCARD ALL")
		discardScalar(t, p, b, a, "SHOW default_transaction_read_only", "default_transaction_read_only", "TEXT", "off")
	})
}

func TestReferenceDiscardAllSettingsAuthorizationAndLocks(t *testing.T) {
	discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		settings := []struct{ name, value string }{
			{"application_name", "'changed'"}, {"search_path", "'pg_catalog, public'"}, {"TimeZone", "'America/New_York'"},
			{"bytea_output", "'escape'"}, {"statement_timeout", "'5s'"}, {"lock_timeout", "'5s'"},
			{"default_transaction_read_only", "'on'"},
		}
		defaults := make(map[string]string)
		for _, setting := range settings {
			var value string
			if err := a.QueryRowContext(p.Ctx, "SHOW "+setting.name).Scan(&value); err != nil {
				t.Fatal(err)
			}
			defaults[setting.name] = value
		}
		var sessionUser, currentUser string
		if err := a.QueryRowContext(p.Ctx, "SELECT session_user,current_user").Scan(&sessionUser, &currentUser); err != nil {
			t.Fatal(err)
		}
		discardSeed(t, p, b, a, peer)
		discardExec(t, p, b, a, "CREATE ROLE discard_actor")
		for _, setting := range settings {
			if setting.name != "default_transaction_read_only" {
				discardExec(t, p, b, a, "SET "+setting.name+"="+setting.value)
			}
		}
		discardExec(t, p, b, a, "SET discard.placeholder='changed'")
		keys := []struct{ args, lock, try string }{
			{"81002::bigint", "pg_try_advisory_lock", "pg_try_advisory_lock"},
			{"81003::bigint", "pg_try_advisory_lock_shared", "pg_try_advisory_lock"},
			{"81,4", "pg_try_advisory_lock", "pg_try_advisory_lock"},
			{"81,5", "pg_try_advisory_lock_shared", "pg_try_advisory_lock"},
		}
		for _, key := range keys {
			for range 2 {
				discardScalar(t, p, b, a, "SELECT "+key.lock+"("+key.args+")", key.lock, "BOOL", true)
			}
			discardScalar(t, p, b, peer, "SELECT "+key.try+"("+key.args+")", key.try, "BOOL", false)
		}
		discardExec(t, p, b, a, "SET SESSION AUTHORIZATION discard_actor")
		discardExec(t, p, b, a, "DISCARD ALL")
		for _, setting := range settings {
			discardScalar(t, p, b, a, "SHOW "+setting.name, setting.name, "TEXT", defaults[setting.name])
		}
		discardScalar(t, p, b, a, "SHOW discard.placeholder", "discard.placeholder", "TEXT", "")
		sqlPreparedStep(t, p, b, a, fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{sessionUser, currentUser}}))
		sqlPreparedStep(t, p, b, a, fixture.ErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
		discardMissingTable(t, p, b, a, "discard_t")
		discardMissingMemory(t, p, b, a, "discard_s")
		for _, key := range keys {
			discardScalar(t, p, b, peer, "SELECT "+key.try+"("+key.args+")", key.try, "BOOL", true)
		}
		discardPeerIntact(t, p, b, peer)
		discardExec(t, p, b, a, "PREPARE p AS SELECT 17 AS v")
		discardScalar(t, p, b, a, "EXECUTE p", "v", "INT4", int64(17))
		discardExec(t, p, b, a, "CREATE TEMP TABLE discard_t(v int)")
		discardScalar(t, p, b, a, "SELECT nextval('discard_s')", "nextval", "INT8", int64(21))
		if b.PG {
			u, err := url.Parse(fixture.DSN)
			if err != nil {
				t.Fatal(err)
			}
			options := u.Query()
			options.Set("options", "-c role=discard_actor -c application_name=discard_configured")
			u.RawQuery = options.Encode()
			connector, err := pq.NewConnector(u.String())
			if err != nil {
				t.Fatal(err)
			}
			configured, record := fixture.InsertSelectRecordedPool(t, connector)
			record.RawBackend = fixture.RawPostgres
			c, err := configured.Conn(p.Ctx)
			if err != nil {
				configured.Close()
				t.Fatal(err)
			}
			cb := fixture.Backend{Name: "configured", DB: configured, Recorder: record, PG: true}
			discardScalar(t, p, cb, c, "SHOW application_name", "application_name", "TEXT", "discard_configured")
			discardScalar(t, p, cb, c, "SELECT current_user", "current_user", "NAME", "discard_actor")
			discardExec(t, p, cb, c, "SET ROLE NONE")
			discardExec(t, p, cb, c, "SET application_name='changed'")
			discardExec(t, p, cb, c, "DISCARD ALL")
			discardScalar(t, p, cb, c, "SHOW application_name", "application_name", "TEXT", "discard_configured")
			discardScalar(t, p, cb, c, "SELECT current_user", "current_user", "NAME", "discard_actor")
			c.Close()
			configured.Close()
		}
		discardExec(t, p, b, a, "DROP ROLE discard_actor")
	})
}
