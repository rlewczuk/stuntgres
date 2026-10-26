package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateSnapshots(t *testing.T) {
	for _, operation := range []string{"TRUNCATE", "DELETE", "ROLLBACK"} {
		t.Run(operation, func(t *testing.T) {
			truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
				truncateSeed(t, f)
				tx := truncateBegin(t, p, f.A, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
				truncateRows(t, f, tx, "q", 7) // establishes frontier without target AS
				if operation == "ROLLBACK" {
					writer := truncateBegin(t, p, f.B, nil)
					truncateExec(t, f, writer, "TRUNCATE p", 0)
					writer.Rollback()
				} else if operation == "DELETE" {
					truncateExec(t, f, f.B, "DELETE FROM p", 2)
				} else {
					truncateExec(t, f, f.B, "TRUNCATE p", 0)
				}
				if operation == "TRUNCATE" {
					truncateRows(t, f, tx, "p")
				} else {
					truncateRows(t, f, tx, "p", 1, 2)
				}
				truncateExec(t, f, f.B, "INSERT INTO p VALUES(9)", 1)
				if operation == "TRUNCATE" {
					truncateRows(t, f, tx, "p")
				} else {
					truncateRows(t, f, tx, "p", 1, 2)
				}
				tx.Rollback()
				if operation == "ROLLBACK" {
					truncateRows(t, f, f.A, "p", 1, 2, 9)
				} else {
					truncateRows(t, f, f.A, "p", 9)
				}
			})
		})
	}
	t.Run("InheritedIncarnation", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			truncateSeed(t, f)
			truncateExec(t, f, f.A, "CREATE TABLE c() INHERITS(p)", 0)
			truncateExec(t, f, f.A, "INSERT INTO c VALUES(3)", 1)
			tx := truncateBegin(t, p, f.A, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			truncateRows(t, f, tx, "q", 7)
			truncateExec(t, f, f.B, "TRUNCATE ONLY c", 0)
			truncateExec(t, f, f.B, "INSERT INTO c VALUES(9)", 1)
			// The inherited bag combines an unchanged frozen parent prefix with
			// the child's new incarnation filtered at the original frontier.
			truncateRows(t, f, tx, "p", 1, 2)
			truncateExec(t, f, tx, "INSERT INTO c VALUES(4)", 1)
			truncateRows(t, f, tx, "p", 1, 2, 4)
			tx.Rollback()
			truncateRows(t, f, f.A, "p", 1, 2, 9)
			truncateRows(t, f, f.A, "c", 9)
		})
	})
	t.Run("PriorReadBlocks", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			truncateSeed(t, f)
			var holder, waiter int
			var oid int64
			if b.PG {
				holder = fixture.LockPID(t, p.Ctx, f.A)
				waiter = fixture.LockPID(t, p.Ctx, f.B)
				oid = fixture.LockOID(t, p.Ctx, "p")
			}
			tx := truncateBegin(t, p, f.A, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			truncateRows(t, f, tx, "p", 1, 2)
			if b.PG {
				join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
					r, e := f.B.ExecContext(ctx, "TRUNCATE p")
					return fixture.RelationOutcome{Result: r, Err: e}
				})
				fixture.LockWaitBehind(t, p.Ctx, waiter, holder, oid, "AccessExclusiveLock")
				truncateRows(t, f, tx, "p", 1, 2)
				tx.Rollback()
				o := join()
				if o.Err != nil {
					t.Fatal(o.Err)
				}
				fixture.LockAffected(t, o.Result, 0)
			} else {
				truncateExec(t, f, f.B, "SET lock_timeout='10ms'", 0)
				truncateError(t, f, f.B, "TRUNCATE p", truncateWant("55P03", "canceling statement due to lock timeout"))
				truncateRows(t, f, tx, "p", 1, 2)
				tx.Rollback()
				truncateExec(t, f, f.B, "TRUNCATE p", 0)
			}
			truncateRows(t, f, f.A, "p")
			// An empty query still touches its relation and retains AS.
			tx = truncateBegin(t, p, f.A, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			truncateRows(t, f, tx, "p")
			truncateExec(t, f, f.B, "SET lock_timeout='10ms'", 0)
			truncateError(t, f, f.B, "TRUNCATE p", truncateWant("55P03", "canceling statement due to lock timeout"))
			tx.Rollback()
		})
	})
	t.Run("OwnRewrite", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			truncateSeed(t, f)
			tx := truncateBegin(t, p, f.A, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			truncateRows(t, f, tx, "q", 7)
			truncateExec(t, f, tx, "SAVEPOINT s", 0)
			truncateExec(t, f, tx, "TRUNCATE p", 0)
			truncateRows(t, f, tx, "p")
			truncateExec(t, f, tx, "INSERT INTO p VALUES(4)", 1)
			truncateRows(t, f, tx, "p", 4)
			truncateExec(t, f, tx, "ROLLBACK TO s", 0)
			truncateRows(t, f, tx, "p", 1, 2)
			tx.Rollback()
		})
	})
	t.Run("NativeControlFence", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			if b.PG {
				return
			} // process-local Reset epoch has no PostgreSQL counterpart
			truncateSeed(t, f)
			tx := truncateBegin(t, p, f.A, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
			truncateRows(t, f, tx, "q", 7)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			rows, err := tx.QueryContext(p.Ctx, "SELECT id FROM p")
			if rows != nil {
				rows.Close()
			}
			fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
		})
	})
}
func TestReferenceTruncateLifetime(t *testing.T) {
	t.Run("OwnedRows", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			truncateExec(t, f, f.A, "CREATE TABLE buffer_t(id INT,note TEXT,payload BYTEA)", 0)
			truncateRun(t, f, f.A, fixture.Exec("INSERT INTO buffer_t VALUES(1,'one',$1),(2,'two',$2)", 2, []byte{0, 1, 255}, []byte{2, 3}))
			rows, err := f.A.QueryContext(p.Ctx, "SELECT id,note,payload FROM buffer_t ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(columns, []string{"id", "note", "payload"}) {
				t.Fatalf("labels %v: %v", columns, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for i, want := range []string{"INT4", "TEXT", "BYTEA"} {
				if types[i].DatabaseTypeName() != want {
					t.Fatalf("type %d: %s", i, types[i].DatabaseTypeName())
				}
			}
			truncateExec(t, f, f.B, "TRUNCATE buffer_t", 0)
			for i, want := range []struct {
				id      int64
				note    string
				payload []byte
			}{{1, "one", []byte{0, 1, 255}}, {2, "two", []byte{2, 3}}} {
				if !rows.Next() {
					t.Fatalf("retained row %d missing: %v", i, rows.Err())
				}
				var id int64
				var note string
				var payload []byte
				if err := rows.Scan(&id, &note, &payload); err != nil || id != want.id || note != want.note || !reflect.DeepEqual(payload, want.payload) {
					t.Fatalf("row %d: %d %q %v: %v", i, id, note, payload, err)
				}
				if i == 0 {
					payload[0] = 99
				} // caller-owned scanned bytes must not mutate storage
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("retained tail: %v", rows.Err())
			}
			rows.Close()
			truncateRows(t, f, f.B, "buffer_t")
			// Metadata captured before publication remains an owned description.
			if !reflect.DeepEqual(columns, []string{"id", "note", "payload"}) || types[2].DatabaseTypeName() != "BYTEA" {
				t.Fatal("captured metadata mutated")
			}
		})
	})
	for _, source := range []string{"direct", "descendant", "cascade"} {
		for _, exhausted := range []bool{false, true} {
			name := source
			if exhausted {
				name += "Exhausted"
			}
			t.Run(name, func(t *testing.T) {
				truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
					truncateExec(t, f, f.A, "CREATE TABLE p(id INT PRIMARY KEY)", 0)
					target := "p"
					q := "TRUNCATE p"
					if source == "descendant" {
						truncateExec(t, f, f.A, "CREATE TABLE child() INHERITS(p)", 0)
						target = "child"
					} else if source == "cascade" {
						truncateExec(t, f, f.A, "CREATE TABLE child(id INT REFERENCES p)", 0)
						target = "child"
						q = "TRUNCATE p CASCADE"
					}
					truncateExec(t, f, f.A, "INSERT INTO p VALUES(1)", 1)
					if target != "p" {
						truncateExec(t, f, f.A, "INSERT INTO child VALUES(1)", 1)
					}
					tx := truncateBegin(t, p, f.A, nil)
					truncateExec(t, f, tx, "DECLARE active NO SCROLL CURSOR FOR SELECT id FROM "+target+" ORDER BY id", 0)
					if exhausted {
						truncateRun(t, f, tx, fixture.Select("FETCH ALL FROM active", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
						truncateRun(t, f, tx, fixture.Select("FETCH ALL FROM active", []string{"id"}, []string{"INT4"}, nil))
					}
					truncateExec(t, f, tx, "SAVEPOINT s", 0)
					truncateError(t, f, tx, q, truncateWant("55006", `cannot TRUNCATE "`+target+`" because it is being used by active queries in this session`))
					if source == "cascade" {
						truncateNotices(t, f, "child")
					}
					truncateExec(t, f, tx, "ROLLBACK TO s", 0)
					truncateExec(t, f, tx, "CLOSE active", 0)
					truncateExec(t, f, tx, q, 0)
					if source == "cascade" {
						truncateNotices(t, f, "child")
					}
					truncateRows(t, f, tx, target)
					tx.Rollback()
					truncateRows(t, f, f.A, target, 1)
				})
			})
		}
	}
	t.Run("ExportedHold", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			truncateSeed(t, f)
			truncateExec(t, f, f.A, "BEGIN", 0)
			truncateExec(t, f, f.A, "DECLARE held NO SCROLL CURSOR WITH HOLD FOR SELECT id FROM p ORDER BY id", 0)
			truncateExec(t, f, f.A, "COMMIT", 0)
			truncateExec(t, f, f.A, "TRUNCATE p", 0)
			truncateRun(t, f, f.A, fixture.Select("FETCH ALL FROM held", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			truncateExec(t, f, f.A, "CLOSE held", 0)
			truncateRows(t, f, f.A, "p")
		})
	})
	t.Run("FailedPortalNoPin", func(t *testing.T) {
		truncateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
			truncateSeed(t, f)
			tx := truncateBegin(t, p, f.A, nil)
			truncateExec(t, f, tx, "DECLARE broken NO SCROLL CURSOR FOR SELECT 1/(id-1) AS n FROM p ORDER BY id", 0)
			truncateExec(t, f, tx, "SAVEPOINT s", 0)
			s := fixture.ErrorDiagnostic("FETCH ALL FROM broken", truncateWant("22012", "division by zero"))
			s.SelectRows = true
			truncateRun(t, f, tx, s)
			truncateExec(t, f, tx, "ROLLBACK TO s", 0)
			truncateExec(t, f, tx, "TRUNCATE p", 0)
			truncateRows(t, f, tx, "p")
			tx.Rollback()
		})
	})
}
