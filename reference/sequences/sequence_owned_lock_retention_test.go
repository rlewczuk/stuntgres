package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSequenceOwnedTableLockRetention(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, alter := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+map[bool]string{false: "/Create", true: "/Alter"}[alter]+map[bool]string{false: "/Rollback", true: "/Commit"}[commit], func(t *testing.T) {
					p := fixture.NewScenario(t)
					for _, backend := range p.Backends() {
						t.Run(backend.Name, func(t *testing.T) {
							s := fixture.NewTempReferenceSession(t, p, backend.DB, backend.Recorder, prepared)
							s.Exec(s.C, "CREATE TABLE t(c INT)", 0)
							q := "CREATE SEQUENCE s OWNED BY t.c"
							if alter {
								s.Exec(s.C, "CREATE SEQUENCE s", 0)
								q = "ALTER SEQUENCE s OWNED BY t.c"
							}
							tx, err := s.C.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							// runner closes the utility before probing retained transaction AS.
							fixture.RunStep(t, p.Ctx, tx, s.Record, fixture.Step{Query: q, Prepared: prepared})
							peer, err := s.Peer.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer peer.Rollback()
							bounded, cancel := context.WithTimeout(p.Ctx, 100*time.Millisecond)
							if backend.PG {
								s.Exec(peer, "SET LOCAL lock_timeout='100ms'", 0)
								cancel()
								bounded = p.Ctx
							}
							result, err := peer.ExecContext(bounded, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
							cancel()
							if result != nil {
								t.Fatal("conflict exposed result")
							}
							if backend.PG {
								fixture.AssertDiagnostic(t, err, fixture.TempReferenceDiagnostic("55P03", "canceling statement due to lock timeout", "", ""))
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
							peer, err = s.Peer.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer peer.Rollback()
							s.Exec(peer, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0)
							if err := peer.Rollback(); err != nil {
								t.Fatal(err)
							}
							s.Exec(s.Peer, "DROP TABLE t", 0)
							if alter && !commit {
								s.Value(s.Peer, "SELECT nextval('s')", 1)
							} else {
								s.Missing(s.Peer, "s", "last_value", "24")
							}
						})
					}
				})
			}
		}
	}
}
