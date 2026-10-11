package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePartialIndexUniquePreparedClaims(t *testing.T) {
	for _, operation := range []string{"insert", "leave", "delete"} {
		for _, completion := range []string{"COMMIT", "ROLLBACK"} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/held=%t", operation, completion, held), func(t *testing.T) {
					partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
						a, peer, run := partialUniqueLockSetup(t, p, b)
						finish := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
						var database, owner string
						if err := a.QueryRowContext(p.Ctx, "SELECT current_database(),current_user").Scan(&database, &owner); err != nil {
							t.Fatal(err)
						}
						if operation != "insert" {
							run(fixture.Exec("INSERT INTO t VALUES(1,7,TRUE)", 1))
						}
						run(fixture.Exec("BEGIN", 0))
						switch operation {
						case "insert":
							run(fixture.Exec("INSERT INTO t VALUES(1,7,TRUE)", 1))
						case "leave":
							run(fixture.Exec("UPDATE t SET active=FALSE WHERE id=1", 1))
						case "delete":
							run(fixture.Exec("DELETE FROM t WHERE id=1", 1))
						}
						const gid = "partial_unique_claim"
						before := time.Now().Add(-time.Second)
						fixture.PreparedReferencePrepare(t, p, b, a, gid)
						entry := fixture.PreparedReferenceOne(t, p, finish, gid)
						if entry.Gid != gid || !entry.Owner.Valid || entry.Owner.String != owner || entry.Database != database || entry.Prepared.Before(before) || entry.Prepared.After(time.Now().Add(time.Second)) {
							t.Fatal(entry, database, owner)
						}
						// Return-to-pool is not physical-origin close: disable idle retention.
						b.DB.SetMaxIdleConns(0)
						if err := a.Close(); err != nil {
							t.Fatal(err)
						}
						if got := fixture.PreparedReferenceOne(t, p, finish, gid); !reflect.DeepEqual(entry, got) {
							t.Fatal(entry, got)
						}
						fixture.LockExec(t, p.Ctx, peer, "SET lock_timeout='100ms'", 0)
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(9,7,FALSE)", 1)))
						fixture.LockExec(t, p.Ctx, peer, "RESET lock_timeout", 0)
						ids, keys, states := []int64{9}, []int64{7}, []any{false}
						if operation != "insert" {
							ids, keys, states = []int64{1, 9}, []int64{7, 7}, []any{true, false}
						}
						fixture.RunStep(t, p.Ctx, finish, b.Recorder, partialUniqueLockRows(ids, keys, states))
						const query = "INSERT INTO t VALUES(2,7,TRUE)"
						// UPDATE's excluded successor occupies slot 2; insert/delete do not.
						physical := `while inserting index tuple (0,3) in relation "pu"`
						if operation == "leave" {
							physical = `while inserting index tuple (0,4) in relation "pu"`
						}
						partialUniqueLockTimeout(t, p, b, peer, query, "lock_timeout", "55P03", "canceling statement due to lock timeout", physical, held)
						if got := fixture.PreparedReferenceOne(t, p, finish, gid); !reflect.DeepEqual(entry, got) {
							t.Fatal(entry, got)
						}
						var bp int
						if b.PG {
							bp = fixture.LockPID(t, p.Ctx, peer)
						}
						await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome { return fixture.RowExecute(ctx, peer, query, held) })
						if b.PG {
							fixture.RowWait(t, p.Ctx, bp, 0)
						}
						fixture.LockExec(t, p.Ctx, finish, completion+" PREPARED "+fixture.PreparedReferenceLiteral(gid), 0)
						o := await()
						collision := (operation == "insert") == (completion == "COMMIT")
						if collision {
							partialUniqueLockFailure(t, o, fixture.UniqueReferenceDuplicate("t", "pu", "Key (k)=(7) already exists."))
						} else {
							fixture.State(t, o.Err, "")
							fixture.LockAffected(t, o.Result, 1)
						}
						fixture.PreparedReferenceAbsent(t, p, finish, gid)
						ids, keys, states = []int64{2, 9}, []int64{7, 7}, []any{true, false}
						if collision {
							ids, keys, states = []int64{1, 9}, []int64{7, 7}, []any{true, false}
						}
						if operation == "leave" && completion == "COMMIT" {
							ids, keys, states = []int64{1, 2, 9}, []int64{7, 7, 7}, []any{false, true, false}
						}
						fixture.RunStep(t, p.Ctx, finish, b.Recorder, partialUniqueLockRows(ids, keys, states))
						// Same contender physical owner is reusable after prepared completion.
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES(3,8,TRUE)", 1)))
						ids, keys, states = []int64{2, 3, 9}, []int64{7, 8, 7}, []any{true, true, false}
						if collision {
							ids, keys, states = []int64{1, 3, 9}, []int64{7, 8, 7}, []any{true, true, false}
						}
						if operation == "leave" && completion == "COMMIT" {
							ids, keys, states = []int64{1, 2, 3, 9}, []int64{7, 7, 8, 7}, []any{false, true, true, false}
						}
						fixture.RunStep(t, p.Ctx, finish, b.Recorder, partialUniqueLockRows(ids, keys, states))
						fixture.LockExec(t, p.Ctx, finish, "BEGIN", 0)
						fixture.LockExec(t, p.Ctx, finish, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
						fixture.LockExec(t, p.Ctx, finish, "ROLLBACK", 0)
						fixture.PreparedReferenceError(t, p.Ctx, finish, completion+" PREPARED "+fixture.PreparedReferenceLiteral(gid), "42704", "prepared transaction with identifier \""+gid+"\" does not exist")
					})
				})
			}
		}
	}
}

func TestReferencePartialIndexUniqueHeldStatementLifetime(t *testing.T) {
	for _, plain := range []bool{false, true} {
		t.Run(fmt.Sprintf("plain=%t", plain), func(t *testing.T) {
			partialIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ func(fixture.Step)) {
				_, peer, run := partialUniqueLockSetup(t, p, b)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1,7,TRUE)", 1))
				const query = "INSERT INTO t VALUES($1,$2,$3)"
				stmt, err := peer.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: peer, Statement: stmt, SQL: query}
				execute := func(s fixture.Step) {
					if !plain {
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						return
					}
					// sql.Conn has no plain Prepare; the real retained statement
					// still exposes the distinct plain Exec consumer.
					result, err := stmt.Exec(s.Args...)
					if s.Diagnostic != nil {
						if result != nil {
							t.Fatal("failed held plain Exec published result", result)
						}
						fixture.AssertDiagnostic(t, err, s.Diagnostic)
					} else {
						fixture.State(t, err, "")
						fixture.LockAffected(t, result, s.Affected)
					}
				}
				fixture.LockExec(t, p.Ctx, peer, "SET statement_timeout='100ms'", 0)
				want := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
				if b.PG {
					want.Context = `while inserting index tuple (0,2) in relation "pu"`
				}
				execute(fixture.ErrorDiagnostic(query, want, int64(2), int64(7), true))
				fixture.LockExec(t, p.Ctx, peer, "RESET statement_timeout", 0)
				fixture.LockExec(t, p.Ctx, peer, "SET lock_timeout='100ms'", 0)
				execute(fixture.Exec(query, 1, int64(3), int64(7), false))
				execute(fixture.Exec(query, 1, int64(4), int64(8), true))
				run(fixture.Exec("ROLLBACK", 0))
				execute(fixture.Exec(query, 1, int64(2), int64(7), true))
				execute(fixture.ErrorDiagnostic(query, fixture.UniqueReferenceDuplicate("t", "pu", "Key (k)=(7) already exists."), int64(5), int64(7), true))
				fixture.LockExec(t, p.Ctx, peer, "RESET lock_timeout", 0)
				run(partialUniqueLockRows([]int64{2, 3, 4}, []int64{7, 7, 8}, []any{true, false, true}))
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
				run(fixture.Exec("ROLLBACK", 0))
			})
		})
	}
}
