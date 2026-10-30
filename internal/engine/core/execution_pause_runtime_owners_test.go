package core_test

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"testing"
	"time"
)

type executionPauseOwnerOutcome struct {
	field session.ProtocolField
	count int64
	err   error
}

func TestExecutionPauseProtocolFunctionDemand(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "cancel"}[canceled], func(t *testing.T) {
			e := engine.New()
			sess := enginetest.StartSession(t, e, nil)
			out := executionPauseActualOwner(t, e, sess, "json.array_length", canceled, func(scope *core.StatementScope) executionPauseOwnerOutcome {
				// Actual fastpath wire input: OID3956, text JSON argument, text result.
				// Catalog identity is json_array_length(json)->integer, not a SQL shim.
				var message []byte
				message = binary.BigEndian.AppendUint32(message, 3956)
				message = binary.BigEndian.AppendUint16(message, 0)
				message = binary.BigEndian.AppendUint16(message, 1)
				message = binary.BigEndian.AppendUint32(message, uint32(len("[1,2]")))
				message = append(message, "[1,2]"...)
				message = binary.BigEndian.AppendUint16(message, 0)
				field, err := scope.CallProtocolFunction(&protocolUnitInput{data: message})
				return executionPauseOwnerOutcome{field: field, err: err}
			})
			if canceled {
				if out.err != context.Canceled || out.field.Null || len(out.field.Bytes) != 0 {
					t.Fatal("fastpath canceled output", out)
				}
			} else if out.err != nil || out.field.Null || string(out.field.Bytes) != "2" {
				t.Fatal("fastpath resumed output", out)
			}
			// The same physical session accepts another genuine fastpath invocation.
			healthy := executionPauseActualOwner(t, e, sess, "json.array_length", false, func(scope *core.StatementScope) executionPauseOwnerOutcome {
				message := binary.BigEndian.AppendUint32(nil, 3956)
				message = binary.BigEndian.AppendUint16(message, 0)
				message = binary.BigEndian.AppendUint16(message, 1)
				message = binary.BigEndian.AppendUint32(message, 2)
				message = append(message, "[]"...)
				message = binary.BigEndian.AppendUint16(message, 0)
				field, err := scope.CallProtocolFunction(&protocolUnitInput{data: message})
				return executionPauseOwnerOutcome{field: field, err: err}
			})
			if healthy.err != nil || healthy.field.Null || string(healthy.field.Bytes) != "0" {
				t.Fatal("fastpath owner recovery", healthy)
			}
		})
	}
}

func TestExecutionPauseProtocolCopyInputDemand(t *testing.T) {
	for _, route := range []string{"predicate", "domain", "default"} {
		for _, canceled := range []bool{false, true} {
			t.Run(route+"/"+map[bool]string{false: "resume", true: "cancel"}[canceled], func(t *testing.T) {
				e := engine.New()
				sess := enginetest.StartSession(t, e, nil)
				query := "COPY execution_pause_input(value) FROM STDIN WHERE value LIKE 'a%'"
				point := "text.like.match"
				if route == "domain" {
					enginetest.SessionSQL(t, sess, "CREATE DOMAIN execution_pause_input_domain AS text CHECK(VALUE LIKE 'a%')")
					enginetest.SessionSQL(t, sess, "CREATE TABLE execution_pause_input(value execution_pause_input_domain)")
					query = "COPY execution_pause_input(value) FROM STDIN"
				} else if route == "default" {
					enginetest.SessionSQL(t, sess, "CREATE TABLE execution_pause_input(value text,n integer DEFAULT json_array_length('[1,2]'))")
					query = "COPY execution_pause_input(value) FROM STDIN"
					point = "json.array_length"
				} else {
					enginetest.SessionSQL(t, sess, "CREATE TABLE execution_pause_input(value text)")
				}
				syntax := enginetest.Statement(t, query).(*parser.Copy)
				copyInput := func(scope *core.StatementScope) executionPauseOwnerOutcome {
					core.SetProtocolScopeCommand(core.ScopeProtocol(scope), true)
					count, err := core.ScopeCopyProtocol(scope, syntax, strings.NewReader("alpha\nalpine\n"), nil, core.CopyProtocolHooks{})
					err = core.ScopeCopyCommandError(scope, err)
					return executionPauseOwnerOutcome{count: count, err: err}
				}
				out := executionPauseActualOwner(t, e, sess, point, canceled, copyInput)
				if canceled {
					if out.err != context.Canceled || out.count != 0 {
						t.Fatal("canceled input published rows", out)
					}
					rows := enginetest.SessionSQL(t, sess, "SELECT value::text FROM execution_pause_input")
					if len(enginetest.Tuples(rows)) != 0 {
						t.Fatal("canceled COPY retained prefix")
					}
					out = executionPauseActualOwner(t, e, sess, point, false, copyInput)
				}
				if out.err != nil || out.count != 2 {
					t.Fatal("COPY input Resume/recovery", out)
				}
				result := enginetest.SessionSQL(t, sess, "SELECT value::text FROM execution_pause_input ORDER BY value::text")
				rows := enginetest.Tuples(result)
				if len(rows) != 2 || rows[0][0] != "alpha" || rows[1][0] != "alpine" {
					t.Fatal("COPY independent rows", rows)
				}
				if route == "default" {
					rows = enginetest.Tuples(enginetest.SessionSQL(t, sess, "SELECT n FROM execution_pause_input ORDER BY value"))
					if len(rows) != 2 || rows[0][0] != int64(2) || rows[1][0] != int64(2) {
						t.Fatal("COPY default values", rows)
					}
				}
			})
		}
	}
}

// Warm the actual decoder, then change to the real protocol-user-cancel input
// projection. The reusable CHECK evaluator must follow the current child, not
// its first caller's now-canceled context. Publication still uses the raw owner.
func TestExecutionPauseDomainDecoderRebind(t *testing.T) {
	e := engine.New()
	sess := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, sess, "CREATE DOMAIN execution_pause_decoder AS text CHECK(VALUE LIKE 'a%')")
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	op, _, err := sess.AcquireCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if !scope.Finished {
			_ = scope.Finish(context.Canceled)
		}
	}()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal("decoder authorization capture", err)
	}
	scope.RoutineAuth = auth
	scope.Domains.InputCatalog = auth.Catalog
	target := domainUnitType(t, e.CatalogRead(), "execution_pause_decoder").SQLType
	candidate := sqltype.String(target.DomainBase().Type, "alpha")
	if err := scope.CheckDomain(util.ActivateExecutionPause(op.Context()), target, candidate); err != nil {
		t.Fatal("initial cached decoder", err)
	}
	op.CopyInput.Active.Store(true)
	op.CopyInput.Decoding.Store(true)
	defer op.StopCopyInput()
	original := op.Context()
	cause := &sqlstate.Error{Code: "57014", Message: "canceling statement due to user request"}
	op.Cancel(cause)
	input := op.Context()
	if input == original || input.Err() != nil || context.Cause(original) != cause {
		t.Fatal("missing actual input projection")
	}
	pause := util.NewExecutionPause("text.like.match")
	defer pause.Resume()
	current := util.ActivateExecutionPause(util.WithExecutionPause(input, pause))
	done := make(chan error, 1)
	joined := false
	defer func() {
		op.StopCopyInput()
		pause.Resume()
		if !joined {
			<-done
		}
	}()
	go func() { done <- scope.CheckDomain(current, target, candidate) }()
	observe, stopObserve := context.WithCancel(ctx)
	admitted := make(chan error, 1)
	go func() { admitted <- pause.Wait(observe) }()
	select {
	case err := <-admitted:
		stopObserve()
		if err != nil {
			t.Fatal("rebound decoder admission", err)
		}
	case err := <-done:
		joined = true
		stopObserve()
		<-admitted
		t.Fatal("cached decoder ignored current effective child", err)
	}
	if !e.Mu.TryLock() {
		t.Fatal("domain decoder held catalog mutex")
	}
	e.Mu.Unlock()
	pause.Resume()
	err = <-done
	joined = true
	if err != nil {
		t.Fatal("masked decoder Resume", err)
	}
	sess.Mu.Lock()
	veto := session.PublicationOperationLocked(e.Catalog.Domain, op)
	sess.Mu.Unlock()
	if veto != context.Canceled || input.Err() != nil || context.Cause(original) != cause || ctx.Err() != nil {
		t.Fatal("decoder weakened publication/cause authority", veto)
	}
	op.StopCopyInput()
	_ = scope.Finish(context.Canceled)
	op.Done()
	executionPauseReleased(t, e, op)
}

// Committed rows precede the partial-index identity. Unlike pre-index private
// writes, these rows are NOT decorated with DDL-produced IndexOutputs. This is
// the exact public DELETE fixture's demand premise, checked against real backing.
func TestExecutionPauseDeleteIndexCache(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "cancel"}[canceled], func(t *testing.T) {
			e := engine.New()
			sess := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, sess, "CREATE TABLE execution_pause_delete(id integer PRIMARY KEY,value text)")
			enginetest.SessionSQL(t, sess, "INSERT INTO execution_pause_delete VALUES(1,'alpha'),(2,'beta'),(3,'alpine')")
			enginetest.SessionSQL(t, sess, "CREATE UNIQUE INDEX execution_pause_partial ON execution_pause_delete(id) WHERE value LIKE 'a%'")
			idx, root := btreeRoot(t, e, "execution_pause_partial")
			if root == nil || len(root.Entries) != 2 {
				t.Fatal("fixture lacks actual partial-index root")
			}
			store := e.Storage.Relations[idx.SQLIndex.Parent]
			if store == nil || len(store.Versions) != 3 {
				t.Fatal("fixture lacks committed original rows")
			}
			for _, row := range store.Versions {
				if row.Born == 0 {
					t.Fatal("index witness accidentally uses private DDL-produced output lane")
				}
				if _, cached := row.IndexOutputs[idx.Address()]; cached {
					t.Fatal("DELETE fixture already caches this partial-index output")
				}
			}
			syntax := enginetest.Statement(t, "DELETE FROM execution_pause_delete WHERE id IN (1,3)")
			work := func(op *session.Operation) executionPauseOwnerOutcome {
				// The ordinary composed entry owns capture, source admission,
				// scoped execution and publication; do not reconstruct its scope.
				result, err := e.Execute(op, syntax, 0, nil)
				if result.Rows != nil {
					result.Rows.Close()
					return executionPauseOwnerOutcome{err: errors.New("DELETE unexpectedly returned rows")}
				}
				return executionPauseOwnerOutcome{count: result.Count, err: err}
			}
			out := executionPauseActualOperation(t, e, sess, "text.like.match", canceled, work)
			if canceled {
				if out.err != context.Canceled || out.count != 0 {
					t.Fatal("canceled key claim", out)
				}
				if e.Storage.Relations[idx.SQLIndex.Parent] != store || e.Storage.IndexRoots[idx.Address()] != root {
					t.Fatal("canceled claim replaced committed backing")
				}
				out = executionPauseActualOperation(t, e, sess, "text.like.match", false, work)
			}
			if out.err != nil || out.count != 2 {
				t.Fatal("genuinely demanded index claim", out)
			}
		})
	}
}

func executionPauseActualOwner(t *testing.T, e *core.Engine, sess *session.Session, point string, canceled bool, work func(*core.StatementScope) executionPauseOwnerOutcome) executionPauseOwnerOutcome {
	t.Helper()
	return executionPauseActualOperation(t, e, sess, point, canceled, func(op *session.Operation) executionPauseOwnerOutcome {
		scope, err := e.StartStatement(op)
		if err != nil {
			return executionPauseOwnerOutcome{err: err}
		}
		out := work(scope)
		out.err = scope.Finish(out.err)
		if scope.Owner.CopyLease != nil {
			out.err = errors.New("actual owner retained COPY lease")
		}
		return out
	})
}

func executionPauseActualOperation(t *testing.T, e *core.Engine, sess *session.Session, point string, canceled bool, work func(*session.Operation) executionPauseOwnerOutcome) executionPauseOwnerOutcome {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	pause := util.NewExecutionPause(point)
	defer pause.Resume()
	op, _, err := sess.AcquireCommand(util.WithExecutionPause(ctx, pause))
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	done := make(chan executionPauseOwnerOutcome, 1)
	joined := false
	defer func() {
		op.Cancel(context.Canceled)
		pause.Resume()
		if !joined {
			<-done
		}
	}()
	go func() { done <- work(op) }()
	observe, stopObserve := context.WithCancel(ctx)
	admitted := make(chan error, 1)
	go func() { admitted <- pause.Wait(observe) }()
	select {
	case err := <-admitted:
		stopObserve()
		if err != nil {
			t.Fatal("actual owner admission", point, err)
		}
	case out := <-done:
		joined = true
		stopObserve()
		<-admitted
		t.Fatal("actual owner completed before provider admission", point, out.err)
	}
	if !e.Mu.TryLock() {
		t.Fatal("actual provider pause held catalog mutex")
	}
	e.Mu.Unlock()
	if !sess.Mu.TryLock() {
		t.Fatal("actual provider pause held session mutex")
	}
	sess.Mu.Unlock()
	if canceled {
		op.Cancel(context.Canceled)
	} else {
		pause.Resume()
	}
	out := <-done
	joined = true
	if canceled && (!errors.Is(out.err, context.Canceled) || context.Cause(op.Context()) != context.Canceled || ctx.Err() != nil) {
		t.Fatal("actual owner cancellation authority", out.err, context.Cause(op.Context()), ctx.Err())
	}
	if op.CopyInput.Active.Load() || op.CopyInput.Decoding.Load() {
		t.Fatal("actual owner retained COPY admission")
	}
	op.Done()
	executionPauseReleased(t, e, op)
	return out
}
