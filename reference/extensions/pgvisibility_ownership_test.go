package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityOwnership(t *testing.T) {
	for _, mutation := range []string{"insert", "update", "delete", "repeated_update", "insert_update_delete"} {
		t.Run(mutation, func(t *testing.T) {
			pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, writer *sql.Conn) {
				peer := pvConn(t, p, b); defer peer.Close()
				seed := mutation != "insert" && mutation != "insert_update_delete"
				if seed {
					pvRun(t, p, b, writer, fixture.Exec("INSERT INTO pv_part.heap VALUES(1)", 1))
					pvFreeze(t, p, b, writer)
					pvOneFrozen(t, p, b, writer)
				}
				pvRun(t, p, b, writer, fixture.Exec("BEGIN", 0)); defer pvRollback(writer)
				pvRun(t, p, b, writer, fixture.Exec("SAVEPOINT before_write", 0))
				var writes []string
				var own [][]any
				switch mutation {
				case "insert": writes = []string{"INSERT INTO pv_part.heap VALUES(2)"}; own = [][]any{{int64(2)}}
				case "update": writes = []string{"UPDATE pv_part.heap SET k=2"}; own = [][]any{{int64(2)}}
				case "delete": writes = []string{"DELETE FROM pv_part.heap"}
				case "repeated_update": writes = []string{"UPDATE pv_part.heap SET k=2", "UPDATE pv_part.heap SET k=3"}; own = [][]any{{int64(3)}}
				case "insert_update_delete": writes = []string{"INSERT INTO pv_part.heap VALUES(2)", "UPDATE pv_part.heap SET k=3", "DELETE FROM pv_part.heap"}
				}
				for _, q := range writes { pvRun(t, p, b, writer, fixture.Exec(q, 1)) }
				pvRun(t, p, b, writer, fixture.Select("SELECT k FROM pv_part.heap ORDER BY k", []string{"k"}, []string{"INT4"}, own))
				var committed [][]any
				if seed { committed = [][]any{{int64(1)}} }
				pvRun(t, p, b, peer, fixture.Select("SELECT k FROM pv_part.heap ORDER BY k", []string{"k"}, []string{"INT4"}, committed))
				// Same one-page physical cohort, different SELECT visibility. Both
				// callers must include private/ended versions in universal proof.
				pvNoUniversal(t, p, b, writer)
				pvNoUniversal(t, p, b, peer)
				pvRun(t, p, b, writer, fixture.Exec("ROLLBACK TO SAVEPOINT before_write", 0))
				pvRun(t, p, b, writer, fixture.Select("SELECT k FROM pv_part.heap ORDER BY k", []string{"k"}, []string{"INT4"}, committed))
				pvRun(t, p, b, writer, fixture.Exec("ROLLBACK", 0))
				pvCoherent(t, p, b, peer, "pv_part.heap")
				// PostgreSQL may retain aborted physical debris. Real maintenance,
				// not rollback itself, supplies the next positive proof witness.
				pvFreeze(t, p, b, peer)
				if seed { pvOneFrozen(t, p, b, peer) }
				pvRun(t, p, b, writer, fixture.Exec("BEGIN", 0))
				for _, q := range writes { pvRun(t, p, b, writer, fixture.Exec(q, 1)) }
				pvRun(t, p, b, writer, fixture.Exec("COMMIT", 0))
				pvRun(t, p, b, peer, fixture.Select("SELECT k FROM pv_part.heap ORDER BY k", []string{"k"}, []string{"INT4"}, own))
				pvCoherent(t, p, b, peer, "pv_part.heap")
				pvFreeze(t, p, b, peer)
				if len(own) == 1 { pvOneFrozen(t, p, b, peer) }
			})
		})
	}
}

func TestReferencePgvisibilitySnapshots(t *testing.T) {
	for _, isolation := range []string{"REPEATABLE READ", "SERIALIZABLE", "READ COMMITTED"} {
		t.Run(isolation, func(t *testing.T) {
			pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, writer *sql.Conn) {
				reader := pvConn(t, p, b); defer reader.Close(); defer pvRollback(reader)
				pvRun(t, p, b, reader, fixture.Exec("BEGIN ISOLATION LEVEL "+isolation+" READ ONLY", 0))
				// Establish a real read-only snapshot before insertion, without
				// requesting an XID just to inspect it.
				pvRun(t, p, b, reader, fixture.Select("SELECT k FROM pv_part.heap", []string{"k"}, []string{"INT4"}, nil))
				pvRun(t, p, b, writer, fixture.Exec("INSERT INTO pv_part.heap VALUES(7)", 1))
				pvFreeze(t, p, b, writer)
				var selected [][]any
				if isolation == "READ COMMITTED" { selected = [][]any{{int64(7)}} }
				pvRun(t, p, b, reader, fixture.Select("SELECT k FROM pv_part.heap", []string{"k"}, []string{"INT4"}, selected))
				if isolation != "READ COMMITTED" {
					// Frozen alone cannot defeat an older advertised frontier.
					// PG's vacuum horizon must independently keep the VM clear.
					pvProbe(t, p, b, reader, "pv_part.heap", 0, false, false, false)
					pvProbe(t, p, b, writer, "pv_part.heap", 0, false, false, false)
				} else {
					// Native may conservatively reject an unknown peer RC frontier;
					// PG's retained vacuum horizon may also keep false bits. Neither
					// is learned from the caller's now-visible SELECT result.
					pvCoherent(t, p, b, reader, "pv_part.heap")
				}
				pvRun(t, p, b, reader, fixture.Exec("ROLLBACK", 0))
				pvFreeze(t, p, b, writer)
				pvOneFrozen(t, p, b, writer)
			})
		})
	}
}

func TestReferencePgvisibilityTupleLocks(t *testing.T) {
	for _, strength := range []string{"KEY SHARE", "SHARE", "NO KEY UPDATE", "UPDATE"} {
		t.Run(strength, func(t *testing.T) {
			pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn) {
				pvRun(t, p, b, observer, fixture.Exec("INSERT INTO pv_part.heap VALUES(1)", 1))
				pvFreeze(t, p, b, observer); pvOneFrozen(t, p, b, observer)
				locker := pvConn(t, p, b); defer locker.Close(); defer pvRollback(locker)
				pvRun(t, p, b, locker, fixture.Exec("BEGIN", 0))
				pvRun(t, p, b, locker, fixture.Exec("SAVEPOINT before_lock", 0))
				pvRun(t, p, b, locker, fixture.Select("SELECT k FROM pv_part.heap FOR "+strength, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				bits := pvCoherent(t, p, b, observer, "pv_part.heap")
				if len(bits) != 1 || bits[0].af { t.Fatal("real tuple lock reported all-frozen", bits) }
				if b.PG && (!bits[0].av || !bits[0].pd) { t.Fatal("lock-only unexpectedly cleared visible", bits) }
				pvRun(t, p, b, locker, fixture.Exec("ROLLBACK TO SAVEPOINT before_lock", 0))
				pvCoherent(t, p, b, observer, "pv_part.heap")
				pvRun(t, p, b, locker, fixture.Select("SELECT k FROM pv_part.heap FOR "+strength, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				bits = pvCoherent(t, p, b, observer, "pv_part.heap")
				if len(bits) != 1 || bits[0].af { t.Fatal("reacquired tuple lock reported frozen", bits) }
				pvRun(t, p, b, locker, fixture.Exec("COMMIT", 0))
				pvCoherent(t, p, b, observer, "pv_part.heap")
				pvFreeze(t, p, b, observer); pvOneFrozen(t, p, b, observer)
			})
		})
	}
	// Genuine compatible multi-owner evidence, not just a singleton xmax.
	t.Run("multiple_owners", func(t *testing.T) {
		pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn) {
			pvRun(t, p, b, observer, fixture.Exec("INSERT INTO pv_part.heap VALUES(1)", 1)); pvFreeze(t, p, b, observer)
			a, z := pvConn(t, p, b), pvConn(t, p, b); defer a.Close(); defer z.Close(); defer pvRollback(a); defer pvRollback(z)
			for _, c := range []*sql.Conn{a,z} {
				pvRun(t, p, b, c, fixture.Exec("BEGIN", 0))
				pvRun(t, p, b, c, fixture.Select("SELECT k FROM pv_part.heap FOR KEY SHARE", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
			}
			for _, c := range []*sql.Conn{a,z} {
				bits := pvCoherent(t, p, b, observer, "pv_part.heap")
				if len(bits)!=1 || bits[0].af { t.Fatal("active multi-owner lock frozen", bits) }
				pvRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
			}
			pvFreeze(t, p, b, observer); pvOneFrozen(t, p, b, observer)
		})
	})
}

func TestReferencePgvisibilityPreparedOwners(t *testing.T) {
	for _, mutation := range []string{"insert", "update", "delete", "lock"} {
		for _, finish := range []string{"COMMIT", "ROLLBACK"} {
			t.Run(mutation+"_"+finish, func(t *testing.T) {
				pvCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn) {
					seed := mutation != "insert"
					if seed { pvRun(t, p, b, observer, fixture.Exec("INSERT INTO pv_part.heap VALUES(1)", 1)); pvFreeze(t, p, b, observer) }
					writer := pvConn(t, p, b); defer writer.Close(); defer pvRollback(writer)
					pvRun(t, p, b, writer, fixture.Exec("BEGIN", 0))
					switch mutation {
					case "insert": pvRun(t, p, b, writer, fixture.Exec("INSERT INTO pv_part.heap VALUES(2)", 1))
					case "update": pvRun(t, p, b, writer, fixture.Exec("UPDATE pv_part.heap SET k=2", 1))
					case "delete": pvRun(t, p, b, writer, fixture.Exec("DELETE FROM pv_part.heap", 1))
					case "lock": pvRun(t, p, b, writer, fixture.Select("SELECT k FROM pv_part.heap FOR UPDATE", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
					}
					gid := fmt.Sprintf("pv_%s_%s_%s", mutation, finish, b.Name)
					active := false; defer pvPreparedCleanup(t, observer, gid, &active)
					pvRun(t, p, b, writer, fixture.Exec("PREPARE TRANSACTION '"+gid+"'", 0)); active = true
					// Returning Conn alone only pools a session. ErrBadConn forces
					// physical close so this proof really uses detached ownership.
					if err := writer.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn { t.Fatal("disconnect prepared owner", err) }
					if mutation == "lock" {
						bits := pvCoherent(t, p, b, observer, "pv_part.heap")
						if len(bits)!=1 || bits[0].af { t.Fatal("prepared tuple lock frozen", bits) }
					} else { pvNoUniversal(t, p, b, observer) }
					var before [][]any
					if seed { before = [][]any{{int64(1)}} }
					pvRun(t, p, b, observer, fixture.Select("SELECT k FROM pv_part.heap", []string{"k"}, []string{"INT4"}, before))
					pvRun(t, p, b, observer, fixture.Exec(finish+" PREPARED '"+gid+"'", 0)); active = false
					after := before
					if finish == "COMMIT" {
						switch mutation { case "insert", "update": after = [][]any{{int64(2)}}; case "delete": after = nil }
					}
					pvRun(t, p, b, observer, fixture.Select("SELECT k FROM pv_part.heap", []string{"k"}, []string{"INT4"}, after))
					pvFreeze(t, p, b, observer)
					pvCoherent(t, p, b, observer, "pv_part.heap")
					if len(after)==1 { pvOneFrozen(t, p, b, observer) }
				})
			})
		}
	}
}
