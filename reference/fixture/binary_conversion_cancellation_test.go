package fixture

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres"
)

// These witnesses exercise the embedding API through real SQL, not a synthetic
// context worker. PostgreSQL retains independent SQL rows in the same scenario;
// request work-credit controls are an explicit native-only phase.
func TestReferenceFixtureProviderPollContext(t *testing.T) {
	p := NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			RunStep(t, p.Ctx, c, b.Recorder, Exec("CREATE TABLE admission_input(v TEXT)", 0))
			RunStep(t, p.Ctx, c, b.Recorder, Exec("INSERT INTO admission_input VALUES('hello')", 1))
			const query = "SELECT encode(convert_to(v,'LATIN1'),'hex') AS v FROM admission_input"
			held, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if !b.PG {
				for _, mode := range []string{"cancel", "cause", "deadline", "late grant"} {
					t.Run(mode, func(t *testing.T) {
						type key struct{}
						cause := errors.New("consumer cancellation cause")
						parent := context.WithValue(p.Ctx, key{}, "request value")
						base, stop := context.WithTimeout(parent, 5*time.Second)
						defer stop()
						ctx, cancel := context.WithCancelCause(base)
						defer cancel(nil)
						if mode == "deadline" {
							var deadlineCancel context.CancelFunc
							ctx, deadlineCancel = context.WithDeadlineCause(ctx, time.Now().Add(250*time.Millisecond), cause)
							defer deadlineCancel()
						}
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
							if mode == "late grant" {
								return nil
							}
							return effective.Err()
						})
						deadline, ok := controlled.Deadline()
						wantDeadline, wantOK := ctx.Deadline()
						if deadline != wantDeadline || ok != wantOK || controlled.Done() != ctx.Done() || controlled.Value(key{}) != "request value" || controlled.Err() != nil {
							t.Fatal("policy attachment changed context delegation")
						}
						done := admissionSQLWorker(t, controlled, func(ctx context.Context) RelationOutcome {
							rows, err := held.QueryContext(ctx)
							return RelationOutcome{Rows: rows, Err: err}
						}, func() { cancel(nil) })
						var effective context.Context
						select {
						case effective = <-entered:
						case out := <-done:
							admissionClose(out)
							t.Fatal("SQL escaped admission", out.Err)
						case <-base.Done():
							t.Fatal("SQL admission deadline", base.Err())
						}
						gotDeadline, gotOK := effective.Deadline()
						if effective.Value(key{}) != "request value" || gotDeadline != deadline || gotOK != ok {
							t.Fatal("effective context lost value/deadline")
						}
						switch mode {
						case "cancel", "late grant":
							cancel(nil)
						case "cause":
							cancel(cause)
						}
						out := admissionSQLJoin(t, done)
						if out.Rows != nil || out.Result != nil {
							admissionClose(out)
							t.Fatal("canceled SQL published output")
						}
						wantErr, wantCause := error(context.Canceled), error(context.Canceled)
						if mode == "cause" {
							wantCause = cause
						}
						if mode == "deadline" {
							wantErr, wantCause = context.DeadlineExceeded, cause
						}
						LexicalOrdinary(t, out.Err, wantErr)
						if context.Cause(effective) != wantCause || context.Cause(controlled) != wantCause {
							t.Fatal("effective cancellation cause lost", context.Cause(effective))
						}
					})
				}
			}
			RunStep(t, p.Ctx, HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"68656c6c6f"}}))
		})
	}
}

func TestReferenceFixtureProviderPollAuthority(t *testing.T) {
	p := NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			RunStep(t, p.Ctx, c, b.Recorder, Exec("CREATE TABLE admission_authority(v TEXT)", 0))
			RunStep(t, p.Ctx, c, b.Recorder, Exec("INSERT INTO admission_authority VALUES('hello')", 1))
			const query = "SELECT encode(convert_to(v,'LATIN1'),'hex') AS v FROM admission_authority"
			if !b.PG {
				refusal := errors.New("work credit refused")
				wrong := stuntgres.WithWorkAdmission(p.Ctx, func(ctx context.Context, class stuntgres.WorkClass) error {
					if class == stuntgres.WorkJSONArrayCount {
						return refusal
					}
					return nil
				})
				RunStep(t, wrong, c, b.Recorder, Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"68656c6c6f"}}))
				refuse := stuntgres.WithWorkAdmission(p.Ctx, func(ctx context.Context, class stuntgres.WorkClass) error {
					if class == stuntgres.WorkEncodingConvert {
						return refusal
					}
					return nil
				})
				masked := stuntgres.WithWorkAdmission(refuse, nil)
				RunStep(t, masked, c, b.Recorder, Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"68656c6c6f"}}))
				preCanceled, cancel := context.WithCancel(refuse)
				cancel()
				rows, canceledErr := c.QueryContext(preCanceled, query)
				if rows != nil {
					rows.Close()
					t.Fatal("precanceled request published cursor")
				}
				LexicalOrdinary(t, canceledErr, context.Canceled)
				const mutation = "UPDATE admission_authority SET v=encode(convert_to(v,'LATIN1'),'hex')"
				for _, prepared := range []bool{false, true} {
					var result sql.Result
					var err error
					var held *sql.Stmt
					if prepared {
						var prepareErr error
						held, prepareErr = c.PrepareContext(p.Ctx, mutation)
						if prepareErr != nil {
							t.Fatal(prepareErr)
						}
						defer held.Close()
						result, err = held.ExecContext(refuse)
					} else {
						result, err = c.ExecContext(refuse, mutation)
					}
					if result != nil || err != refusal {
						t.Fatal("refusal identity/atomic output", result, err)
					}
					RunStep(t, p.Ctx, c, b.Recorder, Select("SELECT v FROM admission_authority", []string{"v"}, []string{"TEXT"}, [][]any{{"hello"}}))
					if held != nil {
						RunStep(t, p.Ctx, HeldStatement{Executor: c, Statement: held, SQL: mutation}, b.Recorder, Exec(mutation, 1))
						RunStep(t, p.Ctx, c, b.Recorder, Select("SELECT v FROM admission_authority", []string{"v"}, []string{"TEXT"}, [][]any{{"68656c6c6f"}}))
						RunStep(t, p.Ctx, c, b.Recorder, Exec("UPDATE admission_authority SET v='hello'", 1))
					}
				}
				// Operand failure wins over the later parent transformation class.
				parent := stuntgres.WithWorkAdmission(p.Ctx, func(ctx context.Context, class stuntgres.WorkClass) error {
					if class == stuntgres.WorkArrayTransform {
						return refusal
					}
					return nil
				})
				rows, err := c.QueryContext(parent, "SELECT array_to_string(ARRAY[1/(length(v)-5)],',') FROM admission_authority")
				if rows != nil {
					rows.Close()
					t.Fatal("failed operand published cursor")
				}
				want := ControlsReferenceDiagnostic("22012", "division by zero")
				AssertDiagnostic(t, err, &want)
			}
			RunStep(t, p.Ctx, c, b.Recorder, Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"68656c6c6f"}}))
		})
	}
}

// Callers nest worker subtests inside handle ownership so cleanup joins first.
// Every receive has a bound, including cleanup when an assertion fails early.
func admissionSQLWorker(t *testing.T, ctx context.Context, operation func(context.Context) RelationOutcome, cancel func()) <-chan RelationOutcome {
	t.Helper()
	done := make(chan RelationOutcome, 1)
	go func() { done <- operation(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case out := <-done:
			admissionClose(out)
		case <-time.After(5 * time.Second):
			t.Error("admission worker cleanup deadline")
		}
	})
	return done
}

func admissionSQLJoin(t *testing.T, done <-chan RelationOutcome) RelationOutcome {
	t.Helper()
	select {
	case out := <-done:
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("admission worker join deadline")
		return RelationOutcome{}
	}
}

func admissionClose(out RelationOutcome) {
	if out.Rows != nil {
		out.Rows.Close()
	}
}

func providerPollWorker(ctx context.Context, statement *sql.Stmt, done chan<- RelationOutcome) {
	rows, err := statement.QueryContext(ctx, `{"a":1}`)
	done <- RelationOutcome{Rows: rows, Err: err}
}

// Closed one-shot pauses remain distinct from incoming work-credit callbacks.
func TestReferenceFixtureExecutionPauseContext(t *testing.T) {
	p := NewScenario(t)
	statement, err := p.Native.PrepareContext(p.Ctx, "SELECT * FROM jsonb_each($1::jsonb)")
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	pause := stuntgres.NewExecutionPause("json.each.next")
	defer pause.Resume()
	type key struct{}
	source, stop := context.WithTimeout(context.WithValue(p.Ctx, key{}, "owned value"), 5*time.Second)
	defer stop()
	scoped := stuntgres.WithExecutionPause(source, pause)
	if scoped.Done() != source.Done() || scoped.Value(key{}) != "owned value" {
		t.Fatal("changed context delegation")
	}
	got, gotOK := scoped.Deadline()
	want, wantOK := source.Deadline()
	if got != want || gotOK != wantOK {
		t.Fatal("changed deadline")
	}
	child, cancel := context.WithCancelCause(scoped)
	defer cancel(nil)
	done := make(chan RelationOutcome, 1)
	joined := false
	defer func() {
		cancel(nil)
		pause.Resume()
		if !joined {
			admissionClose(LockResult(t, done))
		}
	}()
	go providerPollWorker(child, statement, done)
	if err := pause.Wait(source); err != nil {
		t.Fatal("real child admission", err)
	}
	stacks := make([]byte, 64<<10)
	for {
		observed, next := nativeOperationStack(stacks, []byte("sqltype.(*JSONEachCursor).Next"), []byte("fixture.providerPollWorker"))
		stacks = next
		if observed {
			break
		}
		if source.Err() != nil {
			t.Fatal("provider frame not reached", source.Err())
		}
		runtime.Gosched()
	}
	cause := errors.New("owned request end")
	cancel(cause)
	result := LockResult(t, done)
	joined = true
	if result.Rows != nil {
		result.Rows.Close()
		t.Fatal("canceled child published rows")
	}
	LexicalOrdinary(t, result.Err, context.Canceled)
	if context.Cause(child) != cause || source.Err() != nil {
		t.Fatal("child cancellation lost ownership")
	}
}

func TestReferenceFixtureExecutionPauseAuthority(t *testing.T) {
	p := NewScenario(t)
	statement, err := p.Native.PrepareContext(p.Ctx, "SELECT * FROM jsonb_each($1::jsonb)")
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	pause := stuntgres.NewExecutionPause("json.each.next")
	defer pause.Resume()
	ctx, cancel := context.WithTimeout(stuntgres.WithExecutionPause(p.Ctx, pause), 5*time.Second)
	defer cancel()
	done := make(chan RelationOutcome, 1)
	joined := false
	defer func() {
		cancel()
		pause.Resume()
		if !joined {
			admissionClose(LockResult(t, done))
		}
	}()
	go providerPollWorker(ctx, statement, done)
	if err := pause.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	for _, symbols := range [][]string{
		{"fixture.absentProvider", "fixture.providerPollWorker"},
		{"sqltype.(*JSONEachCursor).Next", "fixture.absentWorker"},
		{"sqltype.(*JSONEachCursor).Next", "fixture.TestReferenceFixtureExecutionPauseAuthority"},
	} {
		if found, _ := nativeOperationStack(make([]byte, 64<<10), []byte(symbols[0]), []byte(symbols[1])); found {
			t.Fatal("unrelated or creation-only frames admitted", symbols)
		}
	}
	if ctx.Err() != nil {
		t.Fatal("unrelated context observer blocked")
	}
	pause.Resume()
	result := LockResult(t, done)
	joined = true
	if result.Err != nil {
		t.Fatal("resumed provider", result.Err)
	}
	if result.Rows == nil {
		t.Fatal("resumed provider returned no rows")
	}
	defer result.Rows.Close()
	if !result.Rows.Next() {
		t.Fatal("resumed provider lost row", result.Rows.Err())
	}
	var key string
	var value int
	if err := result.Rows.Scan(&key, &value); err != nil || key != "a" || value != 1 {
		t.Fatal("resumed result", key, value, err)
	}
	if result.Rows.Next() || result.Rows.Err() != nil {
		t.Fatal("unexpected resumed rows", result.Rows.Err())
	}
}
