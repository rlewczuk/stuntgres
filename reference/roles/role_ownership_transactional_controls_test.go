package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipTransactionalControls(t *testing.T) {
	for _, operation := range []string{"ALTER TABLE own_control.t OWNER TO own_target", "REASSIGN OWNED BY own_source TO own_target", "DROP OWNED BY own_source CASCADE"} {
		t.Run(operation, func(t *testing.T) {
			ownershipTransactionCases(t, func(s *membershipScenario, route string) {
				s.exec("CREATE SCHEMA own_control AUTHORIZATION own_source")
				s.actor("own_source")
				s.exec("CREATE TABLE own_control.t(id INT)")
				s.run(fixture.Exec("INSERT INTO own_control.t VALUES(7)", 1))
				s.actor("")
				var committed bytes.Buffer
				if !s.backend.PG {
					if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &committed); err != nil {
						s.t.Fatal(err)
					}
				}
				x := ownershipBegin(s, route, operation)
				defer x.abort()
				x.exec(operation)
				if !s.backend.PG && route != "auto" {
					var pending bytes.Buffer
					if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &pending); err != nil || !bytes.Equal(pending.Bytes(), committed.Bytes()) {
						s.t.Fatal("Dump leaked pending ownership/cleanup", err)
					}
					destinationDriver := stuntgres.NewDriver()
					destination := fixture.CatalogControlPool(s.t, destinationDriver, "transactional_ownership_dump")
					for _, q := range []string{"CREATE ROLE own_source", "CREATE ROLE own_target", "CREATE ROLE own_other"} {
						fixture.LockExec(s.t, s.p.Ctx, destination, q, 0)
					}
					fixture.LockExec(s.t, s.p.Ctx, destination, "ALTER DATABASE transactional_ownership_dump OWNER TO own_other", 0)
					if err := stuntgres.Restore(s.p.Ctx, destination, bytes.NewReader(pending.Bytes())); err != nil {
						s.t.Fatal(err)
					}
					connector, err := destinationDriver.OpenConnector("transactional_ownership_dump")
					if err != nil {
						s.t.Fatal(err)
					}
					recordedDestination, destinationRecord := fixture.RecordedNativePool(s.t, connector)
					fixture.RunStep(s.t, s.p.Ctx, recordedDestination, destinationRecord, fixture.Select("SELECT id FROM own_control.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					fixture.RunStep(s.t, s.p.Ctx, recordedDestination, destinationRecord, fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='own_control.t'::regclass", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_source"}}))
					fixture.RunStep(s.t, s.p.Ctx, recordedDestination, destinationRecord, fixture.Select("SELECT r.rolname FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname=current_database()", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_other"}}))
					if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader("CREATE TABLE replacement(id INT); INSERT INTO absent VALUES(1);")); err == nil {
						s.t.Fatal("invalid late Restore accepted")
					}
				}
				x.end(true)
				if strings.HasPrefix(operation, "DROP") {
					ownershipCount(s, "SELECT count(*) FROM pg_class WHERE relname='t' AND relnamespace=(SELECT oid FROM pg_namespace WHERE nspname='own_control')", 0)
				} else {
					ownershipOwner(s, "own_control.t", "own_target")
					ownershipRows(s, "own_control.t")
				}
				if !s.backend.PG {
					var published bytes.Buffer
					if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &published); err != nil {
						s.t.Fatal(err)
					}
					replayDriver := stuntgres.NewDriver()
					replay := fixture.CatalogControlPool(s.t, replayDriver, "transactional_ownership_committed")
					for _, q := range []string{"CREATE ROLE own_source", "CREATE ROLE own_target"} {
						fixture.LockExec(s.t, s.p.Ctx, replay, q, 0)
					}
					if err := stuntgres.Restore(s.p.Ctx, replay, bytes.NewReader(published.Bytes())); err != nil {
						s.t.Fatal(err)
					}
					connector, err := replayDriver.OpenConnector("transactional_ownership_committed")
					if err != nil {
						s.t.Fatal(err)
					}
					recordedReplay, replayRecord := fixture.RecordedNativePool(s.t, connector)
					if !strings.HasPrefix(operation, "DROP") {
						fixture.RunStep(s.t, s.p.Ctx, recordedReplay, replayRecord, fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='own_control.t'::regclass", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_target"}}))
					}
				}
			})
		})
	}
}

func TestReferenceRoleOwnershipTransactionalReplacement(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			for _, ending := range []string{"commit", "rollback-to", "held"} {
				t.Run(ending, func(t *testing.T) {
					ownershipTransactionCases(t, func(s *membershipScenario, route string) {
						ownershipSeed(s, "tx_replace_owned", "own_source")
						var dump bytes.Buffer
						if !s.backend.PG {
							if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
								s.t.Fatal(err)
							}
						}
						var retained *sql.Rows
						if !s.backend.PG {
							var err error
							retained, err = s.backend.DB.QueryContext(s.p.Ctx, "SELECT id FROM tx_replace_owned")
							if err != nil {
								s.t.Fatal(err)
							}
							defer retained.Close()
						}
						x := ownershipBegin(s, route, "REASSIGN OWNED BY own_source TO own_target")
						defer x.abort()
						if route != "auto" {
							x.exec("SAVEPOINT private")
						}
						x.exec("REASSIGN OWNED BY own_source TO own_target")
						if s.backend.PG || route == "auto" {
							x.end(false)
							return
						}
						if replacement == "reset" {
							if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
								s.t.Fatal(err)
							}
						} else {
							if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
								s.t.Fatal(err)
							}
						}
						var err error
						switch ending {
						case "commit":
							if x.tx != nil {
								err = x.tx.Commit()
							} else {
								_, err = s.c.ExecContext(s.p.Ctx, "COMMIT")
							}
						case "rollback-to":
							_, err = x.e.ExecContext(s.p.Ctx, "ROLLBACK TO private")
						case "held":
							if stmt := x.held["REASSIGN OWNED BY own_source TO own_target"]; stmt != nil {
								_, err = stmt.ExecContext(s.p.Ctx)
							} else {
								_, err = x.e.ExecContext(s.p.Ctx, "REASSIGN OWNED BY own_source TO own_target")
							}
						}
						fixture.TransactionOrdinary(s.t, err, "stuntgres: catalog replaced before publication")
						x.abort()
						metadata, err := retained.ColumnTypes()
						if err != nil {
							s.t.Fatal(err)
						}
						fixture.ScalarAssertDescription(s.t, metadata, []string{"id"}, []string{"INT4"})
						fixture.TransactionRowValues(s.t, retained, 7)
						retained.Close()
						// A replacement never strands the same physical owner.
						if replacement == "reset" {
							s.exec("CREATE TABLE tx_replace_owned(id INT)")
							s.run(fixture.Exec("INSERT INTO tx_replace_owned VALUES(7)", 1))
							s.exec("ALTER TABLE tx_replace_owned OWNER TO own_source")
						}
						s.exec("BEGIN")
						s.exec("ALTER TABLE tx_replace_owned OWNER TO own_target")
						s.exec("COMMIT")
						ownershipOwner(s, "tx_replace_owned", "own_target")
						ownershipRows(s, "tx_replace_owned")
					})
				})
			}
		})
	}
}
