package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestReferencePartialIndexUniquePreparedClaims(t *testing.T) {
	for _, operation := range []string{"insert", "leave", "delete"} {
		for _, completion := range []string{"COMMIT", "ROLLBACK"} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/held=%t", operation, completion, held), func(t *testing.T) {
					partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
						a, peer, run := partialUniqueLockSetup(t, p, b)
						finish := sequenceCoreConn(t, p.ctx, b.db)
						var database, owner string
						if err := a.QueryRowContext(p.ctx, "SELECT current_database(),current_user").Scan(&database, &owner); err != nil {
							t.Fatal(err)
						}
						if operation != "insert" {
							run(referenceExec("INSERT INTO t VALUES(1,7,TRUE)", 1))
						}
						run(referenceExec("BEGIN", 0))
						switch operation {
						case "insert":
							run(referenceExec("INSERT INTO t VALUES(1,7,TRUE)", 1))
						case "leave":
							run(referenceExec("UPDATE t SET active=FALSE WHERE id=1", 1))
						case "delete":
							run(referenceExec("DELETE FROM t WHERE id=1", 1))
						}
						const gid = "partial_unique_claim"
						before := time.Now().Add(-time.Second)
						preparedReferencePrepare(t, p, b, a, gid)
						entry := preparedReferenceOne(t, p, finish, gid)
						if entry.gid != gid || !entry.owner.Valid || entry.owner.String != owner || entry.database != database || entry.prepared.Before(before) || entry.prepared.After(time.Now().Add(time.Second)) {
							t.Fatal(entry, database, owner)
						}
						// Return-to-pool is not physical-origin close: disable idle retention.
						b.db.SetMaxIdleConns(0)
						if err := a.Close(); err != nil {
							t.Fatal(err)
						}
						if got := preparedReferenceOne(t, p, finish, gid); !reflect.DeepEqual(entry, got) {
							t.Fatal(entry, got)
						}
						referenceLockExec(t, p.ctx, peer, "SET lock_timeout='100ms'", 0)
						referenceRunStep(t, p.ctx, peer, b.recorder, referencePrepared(referenceExec("INSERT INTO t VALUES(9,7,FALSE)", 1)))
						referenceLockExec(t, p.ctx, peer, "RESET lock_timeout", 0)
						ids, keys, states := []int64{9}, []int64{7}, []any{false}
						if operation != "insert" {
							ids, keys, states = []int64{1, 9}, []int64{7, 7}, []any{true, false}
						}
						referenceRunStep(t, p.ctx, finish, b.recorder, partialUniqueLockRows(ids, keys, states))
						const query = "INSERT INTO t VALUES(2,7,TRUE)"
						// UPDATE's excluded successor occupies slot 2; insert/delete do not.
						physical := `while inserting index tuple (0,3) in relation "pu"`
						if operation == "leave" {
							physical = `while inserting index tuple (0,4) in relation "pu"`
						}
						partialUniqueLockTimeout(t, p, b, peer, query, "lock_timeout", "55P03", "canceling statement due to lock timeout", physical, held)
						if got := preparedReferenceOne(t, p, finish, gid); !reflect.DeepEqual(entry, got) {
							t.Fatal(entry, got)
						}
						var bp int
						if b.pg {
							bp = referenceLockPID(t, p.ctx, peer)
						}
						await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome { return referenceRowExecute(ctx, peer, query, held) })
						if b.pg {
							referenceRowWait(t, p.ctx, bp, 0)
						}
						referenceLockExec(t, p.ctx, finish, completion+" PREPARED "+preparedReferenceLiteral(gid), 0)
						o := await()
						collision := (operation == "insert") == (completion == "COMMIT")
						if collision {
							partialUniqueLockFailure(t, o, uniqueReferenceDuplicate("t", "pu", "Key (k)=(7) already exists."))
						} else {
							referenceState(t, o.err, "")
							referenceLockAffected(t, o.result, 1)
						}
						preparedReferenceAbsent(t, p, finish, gid)
						ids, keys, states = []int64{2, 9}, []int64{7, 7}, []any{true, false}
						if collision {
							ids, keys, states = []int64{1, 9}, []int64{7, 7}, []any{true, false}
						}
						if operation == "leave" && completion == "COMMIT" {
							ids, keys, states = []int64{1, 2, 9}, []int64{7, 7, 7}, []any{false, true, false}
						}
						referenceRunStep(t, p.ctx, finish, b.recorder, partialUniqueLockRows(ids, keys, states))
						// Same contender physical owner is reusable after prepared completion.
						referenceRunStep(t, p.ctx, peer, b.recorder, referencePrepared(referenceExec("INSERT INTO t VALUES(3,8,TRUE)", 1)))
						ids, keys, states = []int64{2, 3, 9}, []int64{7, 8, 7}, []any{true, true, false}
						if collision {
							ids, keys, states = []int64{1, 3, 9}, []int64{7, 8, 7}, []any{true, true, false}
						}
						if operation == "leave" && completion == "COMMIT" {
							ids, keys, states = []int64{1, 2, 3, 9}, []int64{7, 7, 8, 7}, []any{false, true, true, false}
						}
						referenceRunStep(t, p.ctx, finish, b.recorder, partialUniqueLockRows(ids, keys, states))
						referenceLockExec(t, p.ctx, finish, "BEGIN", 0)
						referenceLockExec(t, p.ctx, finish, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
						referenceLockExec(t, p.ctx, finish, "ROLLBACK", 0)
						preparedReferenceError(t, p.ctx, finish, completion+" PREPARED "+preparedReferenceLiteral(gid), "42704", "prepared transaction with identifier \""+gid+"\" does not exist")
					})
				})
			}
		}
	}
}

func TestReferencePartialIndexUniqueHeldStatementLifetime(t *testing.T) {
	for _, plain := range []bool{false, true} {
		t.Run(fmt.Sprintf("plain=%t", plain), func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ func(referenceStep)) {
				_, peer, run := partialUniqueLockSetup(t, p, b)
				run(referenceExec("BEGIN", 0))
				run(referenceExec("INSERT INTO t VALUES(1,7,TRUE)", 1))
				const query = "INSERT INTO t VALUES($1,$2,$3)"
				stmt, err := peer.PrepareContext(p.ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: peer, statement: stmt, query: query}
				execute := func(s referenceStep) {
					if !plain {
						referenceRunStep(t, p.ctx, held, b.recorder, s)
						return
					}
					// sql.Conn has no plain Prepare; the real retained statement
					// still exposes the distinct plain Exec consumer.
					result, err := stmt.Exec(s.args...)
					if s.diagnostic != nil {
						if result != nil {
							t.Fatal("failed held plain Exec published result", result)
						}
						referenceAssertDiagnostic(t, err, s.diagnostic)
					} else {
						referenceState(t, err, "")
						referenceLockAffected(t, result, s.affected)
					}
				}
				referenceLockExec(t, p.ctx, peer, "SET statement_timeout='100ms'", 0)
				want := controlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
				if b.pg {
					want.Context = `while inserting index tuple (0,2) in relation "pu"`
				}
				execute(referenceErrorDiagnostic(query, want, int64(2), int64(7), true))
				referenceLockExec(t, p.ctx, peer, "RESET statement_timeout", 0)
				referenceLockExec(t, p.ctx, peer, "SET lock_timeout='100ms'", 0)
				execute(referenceExec(query, 1, int64(3), int64(7), false))
				execute(referenceExec(query, 1, int64(4), int64(8), true))
				run(referenceExec("ROLLBACK", 0))
				execute(referenceExec(query, 1, int64(2), int64(7), true))
				execute(referenceErrorDiagnostic(query, uniqueReferenceDuplicate("t", "pu", "Key (k)=(7) already exists."), int64(5), int64(7), true))
				referenceLockExec(t, p.ctx, peer, "RESET lock_timeout", 0)
				run(partialUniqueLockRows([]int64{2, 3, 4}, []int64{7, 7, 8}, []any{true, false, true}))
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				run(referenceExec("BEGIN", 0))
				run(referenceExec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
				run(referenceExec("ROLLBACK", 0))
			})
		})
	}
}
