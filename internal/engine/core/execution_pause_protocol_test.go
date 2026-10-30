package core_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/util"
)

type executionPauseProtocolResult struct {
	execution core.ProtocolExecution
	rows      [][2]string
	context   context.Context
	err       error
}

func TestExecutionPauseProtocolCopyOutput(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "cancel"}[canceled], func(t *testing.T) {
			e := engine.New()
			sess := enginetest.StartSession(t, e, nil)
			pause := util.NewExecutionPause("json.each.next")
			defer pause.Resume()
			source, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			op, _, err := sess.AcquireCommand(util.WithExecutionPause(source, pause))
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			core.SetProtocolScopeCommand(core.ScopeProtocol(scope), true)
			syntax := enginetest.Statement(t, `COPY (SELECT key,value::text FROM jsonb_each('{"a":1,"b":2}'::jsonb)) TO STDOUT`).(*parser.Copy)
			var output bytes.Buffer
			type outcome struct {
				count int64
				err   error
			}
			done := make(chan outcome, 1)
			joined := false
			defer func() {
				op.Cancel(context.Canceled)
				pause.Resume()
				if !joined {
					<-done
				}
			}()
			go func() {
				count, err := core.ScopeCopyProtocol(scope, syntax, nil, &output, core.CopyProtocolHooks{Start: func(meta core.CopyProtocolMetadata) error {
					if meta.From || meta.Format != 0 || len(meta.ColumnFormats) != 2 || meta.ColumnFormats[0] != 0 || meta.ColumnFormats[1] != 0 {
						return errors.New("unexpected actual COPY output metadata")
					}
					return nil
				}})
				err = core.ScopeCopyCommandError(scope, err)
				err = scope.Finish(err)
				done <- outcome{count, err}
			}()
			admitted := make(chan error, 1)
			observe, stopObserve := context.WithCancel(source)
			go func() { admitted <- pause.Wait(observe) }()
			select {
			case err := <-admitted:
				stopObserve()
				if err != nil {
					t.Fatal("COPY output provider admission", err)
				}
			case out := <-done:
				joined = true
				stopObserve()
				<-admitted
				t.Fatal("COPY output completed before provider admission", out)
			}
			if output.Len() != 0 {
				t.Fatal("COPY published output before first demanded row")
			}
			if !e.Mu.TryLock() {
				t.Fatal("COPY provider pause held catalog mutex")
			}
			e.Mu.Unlock()
			if canceled {
				op.Cancel(context.Canceled)
			} else {
				pause.Resume()
			}
			out := <-done
			joined = true
			if canceled {
				if out.count != 0 || !errors.Is(out.err, context.Canceled) || output.Len() != 0 {
					t.Fatal("canceled COPY published output", out, output.String())
				}
			} else if out.err != nil || out.count != 2 || output.String() != "a\t1\nb\t2\n" {
				t.Fatal("resumed COPY output", out, output.String())
			}
			if core.ProtocolScopeBind(core.ScopeProtocolOrZero(scope)) != nil || core.ForeignTableScopeCursor(core.ScopeForeignTablesOrZero(scope)) != nil || scope.Owner.CopyLease != nil {
				t.Fatal("COPY output retained borrowed producer or reservation")
			}
			op.Done()
			executionPauseReleased(t, e, op)
		})
	}
}

func TestExecutionPauseProtocolCursor(t *testing.T) {
	e := engine.New()
	sess := enginetest.StartSession(t, e, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	op, _, err := sess.AcquireCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Begin(op, session.TxOptions{})
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	cycle := sess.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, sess, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("pause_definition", `SELECT key,value::text AS value FROM jsonb_each('{"a":1,"b":2,"c":3}'::jsonb)`, nil)
		return err
	})
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "pause_definition", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	bind("pause_portal")
	portal := core.ProtocolPortalOf(core.PayloadOf(sess).PreparedSQL.Binding("pause_portal"))
	if portal == nil {
		t.Fatal("actual retained portal missing")
	}
	producer := core.PortalProducer(portal)
	if producer == nil || producer.Root == nil || producer.Source == nil {
		t.Fatal("actual retained producer missing")
	}
	var previous context.Context
	for i, want := range [][2]string{{"a", "1"}, {"b", "2"}, {"c", "3"}} {
		limit := uint32(1)
		if i == 2 {
			limit = 0
		}
		out := executionPauseProtocolDemand(t, e, sess, cycle, "pause_portal", limit, false)
		if out.err != nil || out.execution.Count != 1 || len(out.rows) != 1 || out.rows[0] != want || out.execution.Suspended != (i != 2) {
			t.Fatal("actual protocol demand", i, out, want)
		}
		if previous != nil && (out.context == previous || previous.Err() != context.Canceled) {
			t.Fatal("retained producer reused prior operation authority")
		}
		previous = out.context
		if i != 2 && (core.PortalProducer(portal) != producer || producer.Root == nil || producer.Source == nil) {
			t.Fatal("suspension replaced retained producer")
		}
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("pause_portal") })
	if producer.Root != nil || producer.Source != nil || core.CursorStage(producer) != nil {
		t.Fatal("terminal portal close retained producer graph")
	}

	bind("pause_cancel")
	canceledPortal := core.ProtocolPortalOf(core.PayloadOf(sess).PreparedSQL.Binding("pause_cancel"))
	if canceledPortal == nil {
		t.Fatal("actual cancellation portal missing")
	}
	canceledProducer := core.PortalProducer(canceledPortal)
	if canceledProducer == nil {
		t.Fatal("actual cancellation producer missing")
	}
	first := executionPauseProtocolDemand(t, e, sess, cycle, "pause_cancel", 1, false)
	if first.err != nil || !first.execution.Suspended || len(first.rows) != 1 || first.rows[0] != ([2]string{"a", "1"}) {
		t.Fatal("cancellation owner initial suspension", first)
	}
	out := executionPauseProtocolDemand(t, e, sess, cycle, "pause_cancel", 1, true)
	if !errors.Is(out.err, context.Canceled) || out.execution.Count != 0 || len(out.rows) != 0 {
		t.Fatal("canceled retained Execute published output", out)
	}
	if core.PortalProducer(canceledPortal) != nil || canceledProducer.Root != nil || canceledProducer.Source != nil || core.CursorStage(canceledProducer) != nil {
		t.Fatal("failed Execute retained its actual producer")
	}
}

func executionPauseProtocolDemand(t *testing.T, e *core.Engine, sess *session.Session, cycle *session.ProtocolCycle, name string, limit uint32, canceled bool) executionPauseProtocolResult {
	t.Helper()
	pause := util.NewExecutionPause("json.each.next")
	defer pause.Resume()
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	source := util.WithExecutionPause(ctx, pause)
	op, _, err := sess.AcquireCommand(source)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartProtocolCommand(op, cycle)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan executionPauseProtocolResult, 1)
	joined := false
	defer func() {
		op.Cancel(context.Canceled)
		pause.Resume()
		if !joined {
			<-done
		}
	}()
	go func() {
		out := executionPauseProtocolResult{context: op.Context()}
		out.execution, out.err = scope.ExecuteProtocolPortal(name, limit, func(fields []session.ProtocolField) error {
			if len(fields) != 2 || fields[0].Null || fields[1].Null {
				return errors.New("unexpected protocol pause output shape")
			}
			out.rows = append(out.rows, [2]string{string(fields[0].Bytes), string(fields[1].Bytes)})
			return nil
		}, nil, nil, core.CopyProtocolHooks{})
		out.err = scope.Finish(out.err)
		done <- out
	}()
	admitted := make(chan error, 1)
	observe, stopObserve := context.WithCancel(ctx)
	go func() { admitted <- pause.Wait(observe) }()
	select {
	case err := <-admitted:
		stopObserve()
		if err != nil {
			t.Fatal("protocol provider admission", name, err)
		}
	case out := <-done:
		joined = true
		stopObserve()
		<-admitted
		t.Fatal("protocol demand completed before pause admission", name, out.err)
	}
	if canceled {
		op.Cancel(context.Canceled)
	} else {
		pause.Resume()
	}
	out := <-done
	joined = true
	return out
}
