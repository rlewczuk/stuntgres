package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// These owners are local to the statement-timeout pair. In particular, held
// preparation happens at timeout zero, before the incompatible relation lock.
func regexpTimeoutOwners(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool, q string) (*sql.Conn, *sql.Conn, *sql.Stmt) {
	t.Helper()
	holder, conn := fixture.LockConnections(t, p.Ctx, b.DB)
	fixture.LockExec(t, p.Ctx, conn, `SET statement_timeout=0`, 0)
	var stmt *sql.Stmt
	if prepared {
		var err error
		stmt, err = conn.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	return holder, conn, stmt
}

func regexpTimeoutBlock(t *testing.T, p *fixture.Pair, holder, conn *sql.Conn) {
	t.Helper()
	fixture.LockExec(t, p.Ctx, conn, `BEGIN`, 0)
	fixture.LockExec(t, p.Ctx, conn, `SET LOCAL statement_timeout=0`, 0)
	fixture.LockExec(t, p.Ctx, holder, `BEGIN`, 0)
	fixture.LockExec(t, p.Ctx, holder, `LOCK TABLE cancel_regexp IN ACCESS EXCLUSIVE MODE`, 0)
}

func regexpTimeoutState(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, s string, sentinel int64) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select(`SELECT s,p,sentinel FROM cancel_regexp`, []string{"s", "p", "sentinel"}, []string{"TEXT", "TEXT", "INT4"}, [][]any{{s, "", sentinel}}))
}

func regexpTimeoutRecovery(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, physical any) {
	t.Helper()
	regexpTimeoutState(t, p, b, conn, "a", 7)
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, regexpOne(b, `regexp_count('a','a')`, "INT4", int64(1)))
	if fixture.SettingsPhysical(t, conn) != physical {
		t.Fatal("regexp timeout recovery changed physical owner")
	}
}

// The snapshot is callback-owned until LockOperation joins. Never publish a pq
// cursor as completion: drain and close it before signaling finished. Only the
// independently bounded finite rows are retained, including owned raw bytes.
type regexpAdmissionRows struct {
	columns  []string
	types    []*sql.ColumnType
	values   []any
	readErr  error
	rowsErr  error
	closeErr error
}

func (s *regexpAdmissionRows) read(rows *sql.Rows, limit int) error {
	if rows == nil {
		return errors.New("successful admission query returned no cursor")
	}
	defer func() {
		s.rowsErr = rows.Err()
		s.closeErr = rows.Close()
	}()
	s.columns, s.readErr = rows.Columns()
	if s.readErr != nil {
		return s.readErr
	}
	s.types, s.readErr = rows.ColumnTypes()
	if s.readErr != nil {
		return s.readErr
	}
	for rows.Next() {
		if len(s.values) == limit {
			s.readErr = fmt.Errorf("admission cursor exceeded finite bound %d", limit)
			return s.readErr
		}
		var raw any
		if s.readErr = rows.Scan(&raw); s.readErr != nil {
			return s.readErr
		}
		if bytes, ok := raw.([]byte); ok {
			raw = append([]byte(nil), bytes...)
		}
		s.values = append(s.values, raw)
	}
	s.rowsErr = rows.Err()
	return s.rowsErr
}

func (s *regexpAdmissionRows) assert(t *testing.T, pg bool, matches bool) {
	t.Helper()
	if err := errors.Join(s.readErr, s.rowsErr, s.closeErr); err != nil {
		t.Fatal("admission cursor lifecycle", err)
	}
	name, typ, value, count := "s", "TEXT", "a", 1
	scan := reflect.TypeFor[string]()
	length, lengthKnown := int64(math.MaxInt64), true
	if matches {
		name, typ, value, count = "m", "_TEXT", `{""}`, 2
		length, lengthKnown = 0, false
		if pg {
			scan = reflect.TypeFor[any]()
		}
	}
	if !reflect.DeepEqual(s.columns, []string{name}) || len(s.types) != 1 {
		t.Fatal("admission cursor columns/types", s.columns, s.types)
	}
	c := s.types[0]
	if c.Name() != name || c.DatabaseTypeName() != typ || c.ScanType() != scan {
		t.Fatal("admission cursor descriptor", c.Name(), c.DatabaseTypeName(), c.ScanType())
	}
	// Same computed-column contract as regexpMetadata, captured before Close.
	if n, known := c.Nullable(); n || known {
		t.Fatal("admission computed nullability", n, known)
	}
	if n, known := c.Length(); n != length || known != lengthKnown {
		t.Fatal("admission cursor length", n, known)
	}
	if p, scale, known := c.DecimalSize(); p != 0 || scale != 0 || known {
		t.Fatal("admission cursor decimal size", p, scale, known)
	}
	if len(s.values) != count {
		t.Fatalf("admission rows %d want %d", len(s.values), count)
	}
	// All expected rows are identical, so this checks multiplicity without order.
	for _, raw := range s.values {
		fixture.AssertRawValue(t, pg, typ, raw)
		if fixture.Normalize(raw) != value {
			t.Fatalf("admission value %#v want %q", raw, value)
		}
	}
}

func TestReferenceRegexpStatementTimeoutAdmission(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`CREATE TABLE cancel_regexp(s TEXT,p TEXT,sentinel INT)`, 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(`INSERT INTO cancel_regexp VALUES('a','',7)`, 1))
		for _, prepared := range []bool{false, true} {
			for i, c := range regexpCancellationQueries() {
				t.Run(c.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					holder, conn, stmt := regexpTimeoutOwners(t, p, b, prepared, c.q)
					physical := fixture.SettingsPhysical(t, conn)
					holderPID, waiterPID, oid := 0, 0, int64(0)
					if b.PG {
						holderPID = fixture.LockPID(t, p.Ctx, holder)
						waiterPID = fixture.LockPID(t, p.Ctx, conn)
						oid = fixture.LockOID(t, p.Ctx, "cancel_regexp")
					}
					regexpTimeoutBlock(t, p, holder, conn)
					var snapshot regexpAdmissionRows
					finished := make(chan struct{})
					join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(finished)
						var o fixture.RelationOutcome
						if c.cursor {
							var rows *sql.Rows
							if stmt != nil {
								rows, o.Err = stmt.QueryContext(ctx)
							} else {
								rows, o.Err = conn.QueryContext(ctx, c.q)
							}
							if o.Err == nil {
								limit := 1
								if i == 4 {
									limit = 2
								}
								o.Err = snapshot.read(rows, limit)
							} else if rows != nil {
								snapshot.closeErr = rows.Close()
							}
							o.Err = errors.Join(o.Err, snapshot.rowsErr, snapshot.closeErr)
						} else if stmt != nil {
							o.Result, o.Err = stmt.ExecContext(ctx)
						} else {
							o.Result, o.Err = conn.ExecContext(ctx, c.q)
						}
						return o
					})
					mode := "RowExclusiveLock"
					if c.cursor {
						mode = "AccessShareLock"
					}
					fixture.RelationLockPending(t, p, b, waiterPID, holderPID, oid, mode, finished)
					fixture.LockExec(t, p.Ctx, holder, `ROLLBACK`, 0)
					o := join()
					if o.Err != nil {
						t.Fatal(o.Err)
					}
					s, sentinel := "a", int64(7)
					if c.cursor {
						snapshot.assert(t, b.PG, i == 4)
					} else {
						affected := int64(1)
						if i == 1 {
							affected = 0
						} else {
							sentinel = 99
						}
						if i == 2 {
							s = "XaX"
						}
						fixture.LockAffected(t, o.Result, affected)
					}
					regexpTimeoutState(t, p, b, conn, s, sentinel)
					fixture.LockExec(t, p.Ctx, conn, `ROLLBACK`, 0)
					regexpTimeoutRecovery(t, p, b, conn, physical)
				})
			}
		}
	})
}
