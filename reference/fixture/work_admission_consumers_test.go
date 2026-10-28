package fixture

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestReferenceWorkAdmissionSharedCredits(t *testing.T) {
	p := NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, peer := LockConnections(t, p.Ctx, b.DB)
			RunStep(t, p.Ctx, a, b.Recorder, Exec("CREATE TABLE credit_input(v TEXT)", 0))
			RunStep(t, p.Ctx, a, b.Recorder, Exec("INSERT INTO credit_input VALUES('hello')", 1))
			const query = "SELECT encode(convert_to(v,'LATIN1'),'hex') AS v FROM credit_input"
			held, err := a.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if !b.PG {
				t.Run("native shared workload credits", func(t *testing.T) {
					type requestKey struct{}
					credits := make(chan struct{}, 64)
					waiting := make(chan int, 8)
					var refuse atomic.Bool
					refusal := errors.New("completed request policy must not survive")
					ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
					defer cancel()
					policy := stuntgres.WorkAdmission(func(effective context.Context, class stuntgres.WorkClass) error {
						if refuse.Load() {
							return refusal
						}
						if class != stuntgres.WorkEncodingConvert {
							return nil
						}
						// Reusing the supplied context must start a fresh independent
						// operation, without inheriting the caller's active scope.
						request := effective.Value(requestKey{})
						deadline, hasDeadline := effective.Deadline()
						wantDeadline, wantHasDeadline := ctx.Deadline()
						if (request != 1 && request != 2) || deadline != wantDeadline || hasDeadline != wantHasDeadline || effective.Err() != nil {
							return errors.New("supplied context lost request facts before reentry")
						}
						independent, err := b.DB.Conn(effective)
						if err != nil {
							return err
						}
						defer independent.Close()
						// A SQL failure belongs to this new operation, not the
						// supplied parent operation or the independent session.
						var healthy int
						err = independent.QueryRowContext(effective, "SELECT 1/0").Scan(&healthy)
						var diagnostic *stuntgres.Error
						if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" || diagnostic.Message != "division by zero" {
							return errors.New("independent operation lost division diagnostic")
						}
						if err := independent.QueryRowContext(effective, "SELECT 7").Scan(&healthy); err != nil {
							return err
						}
						afterDeadline, afterHasDeadline := effective.Deadline()
						if healthy != 7 || effective.Value(requestKey{}) != request || afterDeadline != deadline || afterHasDeadline != hasDeadline || effective.Err() != nil || context.Cause(effective) != nil {
							return errors.New("independent operation changed supplied context or session")
						}
						if err := independent.Close(); err != nil {
							return err
						}
						select {
						case waiting <- effective.Value(requestKey{}).(int):
						default:
						}
						select {
						case <-credits:
							return nil
						case <-effective.Done():
							return effective.Err()
						}
					})
					values := make(chan string, 2)
					start := func(id int, conn *sql.Conn, statement *sql.Stmt) <-chan RelationOutcome {
						controlled := stuntgres.WithWorkAdmission(context.WithValue(ctx, requestKey{}, id), policy)
						return admissionSQLWorker(t, controlled, func(ctx context.Context) RelationOutcome {
							var value string
							var err error
							if statement != nil {
								err = statement.QueryRowContext(ctx).Scan(&value)
							} else {
								err = conn.QueryRowContext(ctx, query).Scan(&value)
							}
							if err == nil {
								values <- value
							}
							return RelationOutcome{Err: err}
						}, cancel)
					}
					first, second := start(1, a, held), start(2, peer, nil)
					seen := map[int]bool{}
					for len(seen) != 2 {
						select {
						case id := <-waiting:
							seen[id] = true
						case out := <-first:
							t.Fatal("first SQL escaped shared credits", out.Err)
						case out := <-second:
							t.Fatal("second SQL escaped shared credits", out.Err)
						case <-ctx.Done():
							t.Fatal("shared credit admission deadline", ctx.Err())
						}
					}
					select {
					case value := <-values:
						t.Fatal("withheld credit published output", value)
					default:
					}
					// An unrelated request has no inherited controller while both
					// controlled requests are live and blocked on the same pool.
					RunStep(t, p.Ctx, b.DB, b.Recorder, Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"68656c6c6f"}}))
					for range cap(credits) {
						credits <- struct{}{}
					}
					for _, done := range []<-chan RelationOutcome{first, second} {
						if out := admissionSQLJoin(t, done); out.Err != nil {
							t.Fatal("granted SQL failed", out.Err)
						}
					}
					for range 2 {
						if value := <-values; value != "68656c6c6f" {
							t.Fatal("granted SQL rows", value)
						}
					}
					refuse.Store(true)
					RunStep(t, p.Ctx, HeldStatement{Executor: a, Statement: held, SQL: query}, b.Recorder, Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"68656c6c6f"}}))
				})
			}
			RunStep(t, p.Ctx, HeldStatement{Executor: a, Statement: held, SQL: query}, b.Recorder, Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"68656c6c6f"}}))
		})
	}
}

func TestReferenceWorkAdmissionEffectiveTimeouts(t *testing.T) {
	for _, fatal := range []bool{false, true} {
		t.Run(map[bool]string{false: "statement", true: "session fatal"}[fatal], func(t *testing.T) {
			p := NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					c, peer := LockConnections(t, p.Ctx, b.DB)
					RunStep(t, p.Ctx, c, b.Recorder, Exec("CREATE TABLE effective_input(v TEXT)", 0))
					RunStep(t, p.Ctx, c, b.Recorder, Exec("INSERT INTO effective_input VALUES('hello')", 1))
					query := "SELECT encode(convert_to(v,'LATIN1'),'hex') FROM effective_input"
					if b.PG {
						query = "SELECT pg_sleep(5)"
					}
					held, err := c.PrepareContext(p.Ctx, query)
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					setting := "SET statement_timeout='100ms'"
					if fatal {
						setting = "SET transaction_timeout='250ms'"
					}
					var fatalWire *pgconn.PgConn
					if b.PG && fatal {
						// pq's held-statement FATAL boundary exposes sql.ErrConnDone.
						// Use the existing pinned oracle's public extended protocol
						// to assert the complete fatal diagnostic, not a transport proxy.
						fatalWire, err = pgconn.Connect(p.Ctx, DSN)
						if err != nil {
							t.Fatal(err)
						}
						defer fatalWire.Close(p.Ctx)
						if _, err := fatalWire.Prepare(p.Ctx, "admission_timeout", query, nil); err != nil {
							t.Fatal(err)
						}
						if _, err := fatalWire.Exec(p.Ctx, setting).ReadAll(); err != nil {
							t.Fatal(err)
						}
					} else {
						RunStep(t, p.Ctx, c, b.Recorder, Exec(setting, 0))
					}
					// Register worker cleanup in a nested phase, before either held
					// statement or connection can close. No Raw on the busy owner.
					t.Run("effective operation", func(t *testing.T) {
						ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
						defer cancel()
						entered := make(chan context.Context, 1)
						controlled := stuntgres.WithWorkAdmission(ctx, func(effective context.Context, class stuntgres.WorkClass) error {
							if class != stuntgres.WorkEncodingConvert {
								return nil
							}
							select {
							case entered <- effective:
							default:
							}
							<-effective.Done()
							return effective.Err()
						})
						done := admissionSQLWorker(t, controlled, func(ctx context.Context) RelationOutcome {
							if fatalWire != nil {
								result := fatalWire.ExecPrepared(ctx, "admission_timeout", nil, nil, nil).Read()
								if len(result.Rows) != 0 {
									return RelationOutcome{Err: errors.New("fatal operation published rows")}
								}
								return RelationOutcome{Err: result.Err}
							}
							rows, err := held.QueryContext(ctx)
							return RelationOutcome{Rows: rows, Err: err}
						}, cancel)
						out := admissionSQLJoin(t, done)
						if out.Rows != nil || out.Result != nil {
							admissionClose(out)
							t.Fatal("timeout published output")
						}
						var effective context.Context
						if !b.PG {
							// The SQL diagnostic ends this short timeout probe; retain
							// the real callback context rather than racing an observer.
							select {
							case effective = <-entered:
							default:
								t.Fatal("timeout missed actual provider", out.Err)
							}
						}
						want := &Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to statement timeout"}
						if fatal {
							want = &Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "25P04", Message: "terminating connection due to transaction timeout"}
						}
						AssertDiagnostic(t, out.Err, want)
						if !b.PG && (effective.Err() == nil || context.Cause(effective) == nil || ctx.Err() != nil) {
							t.Fatal("operation timeout failed effective context ownership")
						}
					})
					RunStep(t, p.Ctx, peer, b.Recorder, Select("SELECT v FROM effective_input", []string{"v"}, []string{"TEXT"}, [][]any{{"hello"}}))
				})
			}
		})
	}
}
