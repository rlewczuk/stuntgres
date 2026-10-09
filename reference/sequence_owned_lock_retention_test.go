package reference_test

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReferenceSequenceOwnedTableLockRetention(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter]+map[bool]string{false: "/Rollback", true: "/Commit"}[commit], func(t *testing.T) {
					p := newReferenceScenario(t)
					for _, backend := range p.backends() {
						t.Run(backend.name, func(t *testing.T) {
							s := newTempReferenceSession(t, p, backend.db, backend.recorder, prepared)
							s.exec(s.c, "CREATE TABLE t(c INT)", 0)
							q := "CREATE SEQUENCE s OWNED BY t.c"
							if alter {
								s.exec(s.c, "CREATE SEQUENCE s", 0)
								q = "ALTER SEQUENCE s OWNED BY t.c"
							}
							tx, err := s.c.BeginTx(p.ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							// runner closes the utility before probing retained transaction AS.
							referenceRunStep(t, p.ctx, tx, s.record, referenceStep{query: q, prepared: prepared})
							peer, err := s.peer.BeginTx(p.ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer peer.Rollback()
							bounded, cancel := context.WithTimeout(p.ctx, 100*time.Millisecond)
							if backend.pg {
								s.exec(peer, "SET LOCAL lock_timeout='100ms'", 0)
								cancel()
								bounded = p.ctx
							}
							result, err := peer.ExecContext(bounded, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
							cancel()
							if result != nil {
								t.Fatal("conflict exposed result")
							}
							if backend.pg {
								referenceAssertDiagnostic(t, err, tempReferenceDiagnostic("55P03", "canceling statement due to lock timeout", "", ""))
							} else if !errors.Is(err, context.DeadlineExceeded) {
								t.Fatal("ownership did not retain AS", err)
							}
							if err := peer.Rollback(); err != nil {
								t.Fatal(err)
							}
							if commit {
								err = tx.Commit()
							} else {
								err = tx.Rollback()
							}
							if err != nil {
								t.Fatal(err)
							}
							peer, err = s.peer.BeginTx(p.ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer peer.Rollback()
							s.exec(peer, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
							if err := peer.Rollback(); err != nil {
								t.Fatal(err)
							}
							s.exec(s.peer, "DROP TABLE t", 0)
							if alter && !commit {
								s.value(s.peer, "SELECT nextval('s')", 1)
							} else {
								s.missing(s.peer, "s", "last_value", "24")
							}
						})
					}
				})
			}
		}
	}
}
