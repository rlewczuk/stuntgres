package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddConstraintLocks(t *testing.T) {
	addConstraintLockFences(t)
	for _, tc := range []struct {
		name, clause, mode string
		read               bool
	}{
		{"check", "CONSTRAINT ck CHECK(a>0)", "AccessExclusiveLock", false},
		{"not_null", "CONSTRAINT nn NOT NULL a NOT VALID", "AccessExclusiveLock", false},
		{"unique", "CONSTRAINT uq UNIQUE(a)", "AccessExclusiveLock", false},
		{"primary_key", "CONSTRAINT pk PRIMARY KEY(a)", "AccessExclusiveLock", false},
		{"exclude", "CONSTRAINT ex EXCLUDE(a WITH =)", "AccessExclusiveLock", false},
		{"foreign_key", "CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id)", "ShareRowExclusiveLock", true},
		{"foreign_key_invalid", "CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id) NOT VALID", "ShareRowExclusiveLock", true},
		{"mixed", "CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id), ADD CONSTRAINT ck CHECK(a>0)", "AccessExclusiveLock", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES(1,'one')", 1))
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				pid := 0
				if b.PG {
					pid = fixture.LockPID(t, p.Ctx, c)
				}
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Prepared(fixture.Exec("ALTER TABLE t ADD "+tc.clause, 0)))
				if b.PG {
					var granted bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='t'::regclass AND mode=$2 AND granted)", pid, tc.mode).Scan(&granted); err != nil || !granted {
						t.Fatal("missing real child lock", tc.mode, granted, err)
					}
					if tc.name == "foreign_key" || tc.name == "foreign_key_invalid" || tc.name == "mixed" {
						if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation='p'::regclass AND mode='ShareRowExclusiveLock' AND granted)", pid).Scan(&granted); err != nil || !granted {
							t.Fatal("missing real FK provider hold", granted, err)
						}
					}
				}
				probe := func(table, mode string, success bool) {
					probe, err := peer.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer probe.Rollback()
					q := "LOCK TABLE " + table + " IN " + mode + " MODE NOWAIT"
					if success {
						fixture.RunStep(t, p.Ctx, probe, b.Recorder, fixture.Exec(q, 0))
					} else {
						fixture.RunStep(t, p.Ctx, probe, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on relation "`+table+`"`)))
					}
				}
				probe("t", "ACCESS SHARE", tc.read)
				probe("t", "ROW EXCLUSIVE", false)
				if tc.read {
					probe("p", "ACCESS SHARE", true)
					probe("p", "ROW EXCLUSIVE", false)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				probe("t", "ACCESS EXCLUSIVE", true)
			})
		})
	}
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel_ax", true: "cancel_fk_provider"}[foreign], func(t *testing.T) {
			addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := addConstraintSetup(t, p, b, c)
				run(fixture.Exec("INSERT INTO t VALUES(1,'one')", 1))
				holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
				q, table, mode := "ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0)", "t", "AccessExclusiveLock"
				if foreign {
					q, table, mode = "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p(id) NOT VALID", "p", "ShareRowExclusiveLock"
				}
				held := addConstraintHeld(t, p, b, c, q)
				tx, err := holder.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE "+table+" IN ROW EXCLUSIVE MODE", 0))
				workerPID, holderPID := 0, 0
				if b.PG {
					workerPID, holderPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
				}
				ctx, cancel := context.WithCancel(p.Ctx)
				defer cancel()
				done := make(chan error, 1)
				go func() { _, err := held.ExecContext(ctx, q); done <- err }()
				joined := false
				defer func() {
					cancel()
					if !joined {
						select {
						case <-done:
						case <-p.Ctx.Done():
							t.Error("constraint worker did not join", p.Ctx.Err())
						}
					}
				}()
				if b.PG {
					fixture.LockWaitBehind(t, p.Ctx, workerPID, holderPID, fixture.LockOID(t, p.Ctx, table), mode)
				} else {
					// Compatible holders let the public NOWAIT probe observe queued strict admission.
					for {
						select {
						case err := <-done:
							joined = true
							t.Fatal("ADD completed while provider held", err)
						case <-p.Ctx.Done():
							t.Fatal(p.Ctx.Err())
						default:
						}
						probe, err := observer.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						probeMode := "ACCESS SHARE"
						if foreign {
							probeMode = "ROW EXCLUSIVE"
						}
						_, err = probe.ExecContext(p.Ctx, "LOCK TABLE "+table+" IN "+probeMode+" MODE NOWAIT")
						if rollback := probe.Rollback(); rollback != nil {
							t.Fatal(rollback)
						}
						if err != nil {
							fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: `could not obtain lock on relation "` + table + `"`})
							break
						}
						runtime.Gosched()
					}
				}
				if b.PG {
					var ok bool
					if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&ok); err != nil || !ok {
						t.Fatal(ok, err)
					}
				} else {
					cancel()
				}
				select {
				case err := <-done:
					joined = true
					if b.PG {
						fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
					} else if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				case <-p.Ctx.Done():
					t.Fatal("constraint cancellation did not join", p.Ctx.Err())
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
				run(addConstraintRows([]any{int64(1), "one"}))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
			})
		})
	}
	t.Run("lock_timeout_no_fragments", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			holder, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			tx, err := holder.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("LOCK TABLE t IN ACCESS SHARE MODE", 0))
			run(fixture.Exec("SET lock_timeout='10ms'", 0))
			run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t ADD CONSTRAINT uq UNIQUE(a)", fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout"))))
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("SET lock_timeout=0", 0))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='uq'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT uq UNIQUE(a)", 0))
		})
	})
}
