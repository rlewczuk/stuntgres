package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBackendStateSnapshots(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		for _, origin := range []string{"selected", "activity", "identity"} {
			t.Run(mode+"/"+origin, func(t *testing.T) {
				activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
					peer := statActivityConn(t, p, b)
					admin := statActivityConn(t, p, b)
					mapped := stateMapped(t, p, b, o, peer)
					id, pid := mapped[0], mapped[1].(int64)
					activityExec(t, p, peer, "CREATE TABLE bs_cut(v integer)")
					activityExec(t, p, peer, "BEGIN")
					defer peer.ExecContext(context.Background(), "ROLLBACK")
					activityExec(t, p, peer, "SAVEPOINT first")
					activityExec(t, p, peer, "/* old cut */ INSERT INTO bs_cut VALUES(1)")
					held := statePrepare(t, p, o)
					activityExec(t, p, o, "SET stats_fetch_consistency="+mode)
					activityExec(t, p, o, "BEGIN")
					defer o.ExecContext(context.Background(), "ROLLBACK")
					switch origin {
					case "activity":
						statActivityGet(t, p, o, pid, false)
					case "identity":
						identitySet(t, p, b, o)
					case "selected":
						stateRead(t, p, b, held, id)
					}
					old := stateRead(t, p, b, held, id)
					if old[0] != "/* old cut */ INSERT INTO bs_cut VALUES(1)" || old[5] != "(1,f)" {
						t.Fatal("first common cut", old)
					}
					var stamp sql.NullTime
					if err := o.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&stamp); err != nil || stamp.Valid {
						t.Fatal("activity initialized cumulative cut", stamp, err)
					}
					activityExec(t, p, peer, "SAVEPOINT second")
					activityExec(t, p, peer, "/* changed cut */ INSERT INTO bs_cut VALUES(2)")
					newcomer := statActivityConn(t, p, b)
					newPID := statActivityPID(t, p, newcomer)
					identityPIDAbsent(t, identitySet(t, p, b, o), newPID)
					stateEqual(t, stateRead(t, p, b, held, id), old)
					shared := statActivityGet(t, p, o, pid, false)
					stateSource(t, old, shared, "/* old cut */ INSERT INTO bs_cut VALUES(1)")
					statActivityClear(t, p, o)
					fresh := stateRead(t, p, b, held, id)
					if fresh[0] != "/* changed cut */ INSERT INTO bs_cut VALUES(2)" || fresh[5] != "(2,f)" {
						t.Fatal("clear did not refresh all facts", fresh)
					}
					identityForPID(t, identitySet(t, p, b, o), newPID)
					activityExec(t, p, peer, "COMMIT")
					stateEqual(t, stateRead(t, p, b, held, id), fresh)
					activityExec(t, p, o, "COMMIT")
					ended := stateRead(t, p, b, held, id)
					if ended[0] != "COMMIT" || ended[2] != nil || ended[5] != "(0,f)" {
						t.Fatal("outer end did not refresh", ended)
					}
					activityExec(t, p, o, "BEGIN")
					old = stateRead(t, p, b, held, id)
					statActivitySignal(t, p, admin, pid, true)
					statActivityRetired(t, p, admin, pid)
					replacement := statActivityConn(t, p, b)
					replacementPID := statActivityPID(t, p, replacement)
					identityPIDAbsent(t, identitySet(t, p, b, o), replacementPID)
					retained := stateRead(t, p, b, held, id)
					stateEqual(t, retained[:3], old[:3])
					stateEqual(t, retained[3:], []any{nil, nil, old[5]})
					activityExec(t, p, o, "ROLLBACK")
					identityRetiredFresh(t, p, b, o, mapped)
					identityForPID(t, identitySet(t, p, b, o), replacementPID)
					// Do not pin recyclable allocation. A successor sharing the old
					// slot must expose its own source/count rather than the old cut.
					newMap := identityForPID(t, identitySet(t, p, b, o), replacementPID)
					if newMap[0] == id {
						row := stateRead(t, p, b, held, id)
						if row[0] == old[0] || row[5] != "(0,f)" {
							t.Fatal(fmt.Sprint("slot adopted old incarnation", row))
						}
					}
				})
			})
		}
	}
}
