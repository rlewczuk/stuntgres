package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"time"
)

// A successful command's assertions are never an expiry signal. In particular,
// COMMIT closes the adaptation fence before even inspecting its Result.
func (f *timeoutReferenceFixture) completedTransactionRows() {
	f.t.Helper()
	ctx, cancel := f.transactionSetupContext()
	defer cancel()
	type observation struct {
		attempt       int
		query         string
		before, after time.Time
		err           error
	}
	var history []*observation
	record := func(attempt int, query string, operation func() error) (o *observation) {
		o = &observation{attempt: attempt, query: query, before: time.Now()}
		history = append(history, o)
		defer func() { o.after = time.Now() }()
		o.err = operation()
		return o
	}
	f.t.Cleanup(func() {
		if f.t.Failed() {
			for _, o := range history {
				f.t.Logf("completion backend=%s attempt=%d phase/query=%s before=%v after=%v err=%v", f.b.Name, o.attempt, o.query, o.before, o.after, o.err)
			}
		}
	})
	healthy := func() {
		if ctx.Err() != nil {
			f.t.Fatal("completion setup allowance expired", ctx.Err())
		}
	}
	var previous any
	for attempt := 1; attempt <= 3; attempt++ {
		healthy()
		identity := fixture.SettingsPhysical(f.t, f.owner)
		if identity == previous {
			f.t.Fatal("completion replacement reused physical owner")
		}
		previous = identity
		record(attempt, "peer exact7 before setup", func() error { f.rows(f.peer, 7); return nil })
		f.t.Logf("completion backend=%s attempt=%d owner=%p PID=%d incarnation=%s configured=250ms", f.b.Name, attempt, identity, f.ownerPID, f.ownerSessionID)
		var begin *observation
		var retained *sql.Rows
		var check func()
		committed := false
		expired := false
		queries := []string{"SET transaction_timeout='250ms'", "SELECT v FROM timeout_sentinel", "SHOW transaction_timeout", "BEGIN", "INSERT INTO timeout_sentinel VALUES(8)", "COMMIT"}
		for i, q := range queries {
			healthy()
			if i == 2 {
				record(attempt, "idle350 (no owner SQL)", func() error { timeoutReferenceIdle(f.t, ctx, 350*time.Millisecond); return nil })
				healthy()
			}
			o := record(attempt, q, func() error {
				switch i {
				case 1:
					var err error
					retained, err = f.owner.QueryContext(ctx, q)
					if err != nil {
						if retained != nil {
							retained.Close()
							f.t.Fatal("failed completion SELECT published Rows", err)
						}
						return err
					}
					if retained == nil {
						f.t.Fatal("successful completion SELECT omitted Rows")
					}
					owned := retained
					f.t.Cleanup(func() { owned.Close() })
					check = settingsTimeoutCursor(f.t, retained, f.b.PG)
					return nil
				case 2:
					return f.transactionSetupText(ctx, q, "transaction_timeout", "250ms", false)
				case 5:
					result, err := f.owner.ExecContext(ctx, q)
					if err == nil {
						committed = true
					} // Irreversible, including malformed successful Results.
					if err != nil {
						if result != nil {
							f.t.Fatal("failed completion COMMIT published Result", err)
						}
						return err
					}
					if result == nil {
						f.t.Fatal("successful completion COMMIT omitted Result")
					}
					if n, err := result.RowsAffected(); err != nil || n != 0 {
						f.t.Fatal("completion COMMIT affected count", n, err)
					}
					if _, err := result.LastInsertId(); err == nil {
						f.t.Fatal("completion COMMIT LastInsertId supported")
					}
					return nil
				default:
					count := int64(0)
					if i == 4 {
						count = 1
					}
					return f.transactionSetupExec(ctx, q, count)
				}
			})
			if i == 3 {
				begin = o
			}
			if o.err != nil {
				if committed {
					f.t.Fatal("completion error after commit fence", o.err)
				}
				healthy()
				if check != nil && !f.b.PG {
					check()
					check = nil
				}
				earliest := o.before.Add(250 * time.Millisecond)
				latest := o.after.Add(250 * time.Millisecond)
				if i >= 4 {
					earliest, latest = begin.before.Add(250*time.Millisecond), begin.after.Add(250*time.Millisecond)
				}
				f.t.Logf("completion expiry candidate attempt=%d query=%s before=%v after=%v BEGIN=%+v earliest=%v latest-start-bracket=%v err=%v", attempt, q, o.before, o.after, begin, earliest, latest, o.err)
				record(attempt, "exact fatal/dead owner", func() error { f.completionSetupExpiry(ctx, o.err, o.after, earliest, i == 4); return nil })
				record(attempt, "peer exact7 rollback/release", func() error { f.rows(f.peer, 7); return nil })
				healthy()
				f.t.Logf("completion accepted pre-commit expiry attempt=%d; not a passing witness", attempt)
				// Dispose even the last exhausted attempt; never replay committed8.
				_ = f.owner.Raw(func(any) error { return driver.ErrBadConn })
				_ = f.owner.Close()
				expired = true
				break
			}
			if committed {
				f.t.Logf("completion irreversible COMMIT fence attempt=%d before=%v after=%v BEGIN=%+v", attempt, o.before, o.after, begin)
				break
			}
			healthy()
			if i == 2 {
				if !f.b.PG {
					check()
					check = nil
				}
				if fixture.SettingsPhysical(f.t, f.owner) != identity {
					f.t.Fatal("completion implicit witness changed physical owner")
				}
			}
		}
		if expired {
			if attempt == 3 {
				f.t.Fatal("completion exhausted three full fresh-owner attempts")
			}
			f.replaceTimeoutOwner()
			continue
		}
		if !committed {
			f.t.Fatal("completion missing successful COMMIT")
		}
		// Semantic assertions use the parent context, outside all adaptation.
		record(attempt, "post-COMMIT idle350 (no owner SQL)", func() error { timeoutReferenceIdle(f.t, f.p.Ctx, 350*time.Millisecond); return nil })
		record(attempt, "same-owner exact7,8", func() error { f.rows(f.owner, 7, 8); return nil })
		if fixture.SettingsPhysical(f.t, f.owner) != identity {
			f.t.Fatal("completion changed physical owner")
		}
		record(attempt, "post-COMMIT SHOW250", func() error {
			if err := f.transactionSetupText(f.p.Ctx, "SHOW transaction_timeout", "transaction_timeout", "250ms", false); err != nil {
				f.t.Fatal("completed owner SHOW failed", err)
			}
			return nil
		})
		return
	}
}

func (f timeoutReferenceFixture) completionSetupExpiry(ctx context.Context, err error, observed, earliest time.Time, insert bool) {
	f.t.Helper()
	if !insert {
		f.transactionSetupExpiry(ctx, err, observed, earliest)
		return
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || observed.Before(earliest) {
		f.t.Fatal("completion INSERT is not bracket-proven configured expiry", ctx.Err(), observed, earliest, err)
	}
	if !f.b.PG {
		want := timeoutReferenceWant("25P04")
		atTarget := want
		atTarget.Position = "13"
		got := fixture.ExtractDiagnostic(f.t, err)
		if got != want && got != atTarget {
			f.t.Fatalf("completion INSERT diagnostic = %#v, want %#v or %#v", got, want, atTarget)
		}
	} else {
		if !errors.Is(err, driver.ErrBadConn) {
			f.t.Fatal("completion INSERT transport", err)
		}
		prefix := fmt.Sprintf("timeout-oracle|%d|%s|", f.ownerPID, f.ownerSessionID)
		exact := prefix + "25P04|FATAL:  terminating connection due to transaction timeout"
		atTarget := exact + " at character 13"
		if strings.Contains(f.logBaseline, exact) || strings.Contains(f.logBaseline, atTarget) {
			f.t.Fatal("completion INSERT fatal predates arming")
		}
		logCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		found := false
		for !found {
			for _, line := range strings.Split(f.oracleLogs(logCtx), "\n") {
				at := strings.Index(line, prefix)
				if at < 0 {
					continue
				}
				line = strings.TrimSuffix(line[at:], "\r")
				if !strings.Contains(line, "|FATAL:") {
					continue
				}
				if line != exact && line != atTarget {
					f.t.Fatal("unexpected completion INSERT server fatal", line)
				}
				found = true
			}
			if !found {
				timeoutReferenceIdle(f.t, logCtx, 10*time.Millisecond)
			}
		}
	}
	f.dead()
}
