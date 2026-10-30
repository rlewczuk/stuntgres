package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWRowLockFailureAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run("commit/"+fdwRowLockModeName(prepared), func(t *testing.T) {
			fdwRowLockAcknowledgedCommit(t, prepared)
		})
		for _, phase := range []string{"refetch", "recheck", "import", "project", "end"} {
			t.Run(fdwRowLockModeName(prepared)+"/"+phase, func(t *testing.T) {
				failure := errors.New("row locking failed after actual source effects")
				fdwRowLockConfiguredScenario(t, true, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, locker *fdwSQLRowLocker, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE fdw_failure_local(id integer)", 0))
					run(fixture.Exec("BEGIN", 0))
					defer fdwRowLockRollback(t, c)
					run(fixture.Exec("INSERT INTO fdw_failure_local VALUES(1)", 1))
					q := "SELECT id,label,flag FROM fdw_data ORDER BY id FOR UPDATE"
					locks := 0
					if b.PG {
						// This backend has no late callback: independently acquire a
						// real earlier lock and write, then fail actual local SQL.
						fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE", prepared, [][]any{{int64(1), "one", true}}))
						run(fixture.Exec("UPDATE fdw_data SET label='suffix' WHERE id=1", 1))
						q = "SELECT id/(id-2) AS id,label,flag FROM (SELECT id,label,flag FROM fdw_data ORDER BY id FOR UPDATE) s ORDER BY id"
					} else {
						locker.afterRefetch = func(ctx context.Context, r stuntgres.ForeignRowMarkRequest, row stuntgres.ForeignRow) (stuntgres.ForeignRow, error) {
							locks++ // This hook runs only AFTER actual SELECT FOR ... succeeds.
							tx, err := fdwWriterTx(r.ForeignRequest)
							if err != nil {
								return nil, err
							}
							if locks == 1 {
								if err := tx.QueryRowContext(ctx, "UPDATE fdw_source.data SET label='suffix',revision=revision+1 WHERE rid=1 RETURNING id,label,flag").Scan(&row[0], &row[1], &row[2]); err != nil {
									return nil, err
								}
							}
							if locks == 2 {
								switch phase {
								case "refetch":
									return nil, failure
								case "import":
									row[0] = "not an integer"
								case "project":
									// Actual source RETURNING produces the failing new key;
									// the projection was valid on the captured id=2.
									if err := tx.QueryRowContext(ctx, "UPDATE fdw_source.data SET id=3,revision=2 WHERE rid=2 RETURNING id,label,flag").Scan(&row[0], &row[1], &row[2]); err != nil {
										return nil, err
									}
								}
							}
							return row, nil
						}
						if phase == "project" {
							q = "SELECT id/(id-3) AS id,label,flag FROM fdw_data ORDER BY id FOR UPDATE"
						}
						if phase == "recheck" {
							locker.recheck = func(ctx context.Context, row stuntgres.ForeignRow) (bool, error) {
								if row[0] == int64(2) {
									return false, failure
								}
								return row[2] == true || row[2] == false, ctx.Err()
							}
						}
						if phase == "end" {
							locker.writeHook = func(ctx context.Context, name string) error {
								if name == "EndStatement" && locks >= 2 {
									return failure
								}
								return ctx.Err()
							}
						}
					}
					run(fixture.Exec("SAVEPOINT statement_suffix", 0))
					q = "WITH effect AS (INSERT INTO fdw_failure_local VALUES(2) RETURNING id AS effect_id) " + strings.Replace(q, "FROM fdw_data", "FROM fdw_data CROSS JOIN effect", 1)
					o := fdwRowLockFailedQuery(p.Ctx, c, q, prepared)
					if o.Rows != nil || o.Result != nil || o.Err == nil {
						t.Fatalf("failure exposed success: %#v", o)
					}
					if !b.PG && locks < 2 {
						t.Fatalf("failure preceded actual source locks: %d: %v", locks, o.Err)
					}
					if !b.PG && (phase == "refetch" || phase == "recheck" || phase == "end") && !errors.Is(o.Err, failure) {
						t.Fatal("primary callback error lost", o.Err)
					}
					if o.Stmt != nil {
						o.Stmt.Close()
					}
					locker.writeHook = nil
					locker.afterRefetch = nil
					locker.recheck = nil
					run(fixture.Exec("ROLLBACK TO statement_suffix", 0))
					run(fixture.Select("SELECT id FROM fdw_failure_local ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					// The failed statement's later lock is released by real
					// rollback-to, without ending the earlier transaction owner.
					probe, err := source.QueryContext(p.Ctx, "SELECT id,label,flag FROM fdw_source.data WHERE rid=2 FOR UPDATE NOWAIT")
					fdwRowLockValues(t, fixture.RelationOutcome{Rows: probe, Err: err}, [][]any{{int64(2), "two", false}})
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.Select("SELECT id FROM fdw_failure_local", []string{"id"}, []string{"INT4"}, [][]any{}))
					if b.PG || phase == "project" {
						fdwRowLockDiagnostic(t, o, "22012", "division by zero")
					} else if phase == "import" {
						// Refetch imports the full declared tuple without a scan
						// occurrence: there is no column/remote-SQL Context envelope.
						// Assert empty Context explicitly, alongside every other
						// portable field, rather than echoing the returned error.
						fdwRowLockDiagnostic(t, o, "22P02", `invalid input syntax for type integer: "not an integer"`, "")
					}
					// Direct source inspection and mutation prove rollback of actual
					// effects and release of every genuine remote row lock.
					rows, err := source.QueryContext(p.Ctx, "SELECT id,label,flag FROM fdw_source.data ORDER BY id")
					fdwRowLockValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, [][]any{{int64(1), "one", true}, {int64(2), "two", false}})
					fixture.LockExec(t, p.Ctx, source, "DELETE FROM fdw_source.data", 2)
				})
			})
		}
	}
}

func fdwRowLockAcknowledgedCommit(t *testing.T, prepared bool) {
	failure := errors.New("source committed before acknowledgement failed")
	fdwRowLockConfiguredScenario(t, false, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, source *sql.DB, locker *fdwSQLRowLocker, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TABLE fdw_commit_local(id integer)", 0))
		run(fixture.Exec("BEGIN", 0))
		defer fdwRowLockRollback(t, c)
		run(fixture.Exec("INSERT INTO fdw_commit_local VALUES(1)", 1))
		fdwRowLockClose(t, fdwRowLockRead(t, p.Ctx, c, "SELECT id,label,flag FROM fdw_data WHERE id=1 FOR UPDATE", prepared, [][]any{{int64(1), "one", true}}))
		run(fixture.Exec("UPDATE fdw_data SET label='committed-remotely' WHERE id=1", 1))
		if !b.PG {
			locker.writeHook = func(ctx context.Context, name string) error {
				if name == "AfterCommit" {
					return failure
				}
				return ctx.Err()
			}
			o := fixture.RowExecute(p.Ctx, c, "COMMIT", prepared)
			if o.Result != nil || !errors.Is(o.Err, failure) {
				t.Fatalf("unknown commit reported success or lost error: %#v", o)
			}
			locker.writeHook = nil
		} else {
			run(fixture.Exec("COMMIT", 0))
		}
		// A successful source Commit cannot honestly be undone by a later
		// callback/ack error. No compensating SQL or fictitious rollback.
		var label string
		if err := source.QueryRowContext(p.Ctx, "SELECT label FROM fdw_source.data WHERE rid=1").Scan(&label); err != nil || label != "committed-remotely" {
			t.Fatal(label, err)
		}
		want := [][]any{}
		if b.PG {
			want = [][]any{{int64(1)}}
		}
		run(fixture.Select("SELECT id FROM fdw_commit_local", []string{"id"}, []string{"INT4"}, want))
		fixture.LockExec(t, p.Ctx, source, "DELETE FROM fdw_source.data WHERE rid=1", 1)
	})
}
