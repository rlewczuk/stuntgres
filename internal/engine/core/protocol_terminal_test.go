package core_test

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestProtocolFailedExecutorOwnership(t *testing.T) {
	e := engine.New()
	sess := sessionStart(t, e, nil)
	sessionSQL(t, sess, "BEGIN")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, _, err := sess.AcquireCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartProtocolCommand(op, sess.NewProtocolCycle())
	if err != nil {
		t.Fatal(err)
	}
	primary := errors.New("row consumer stopped")
	defer func() {
		if err := scope.Finish(primary); !errors.Is(err, primary) {
			t.Error("terminal cleanup replaced primary error", err)
		}
	}()
	typ := enginetest.Usage(e, sqltype.Int4)
	value, err := typ.Type.Input(context.Background(), "1", -1)
	if err != nil {
		t.Fatal(err)
	}
	columns := []catalog.Column{{Name: "v", Usage: typ}}
	rows := exec.NewFinalRows(columns, 1)
	rows.Values = []sqltype.Value{value}
	disposals := 0
	p := core.NewPortal(core.PortalFields{Tx: scope.TxRef, Born: 1, Entry: *core.NewPreparedStatement(core.PreparedStatementFields{Body: &parser.Select{}}), Columns: columns, Started: true, Context: session.ProtocolContext{Source: "retained source"}})
	core.SetPortalRows(p, protocolDisposalProbe{RowResult: rows, close: func() error {
		disposals++
		if op.Context().Err() == nil {
			t.Error("disposal did not exercise canceled operation")
		}
		if !scope.Owner.Admission.TryLock() {
			t.Error("failed executor disposal held admission")
		} else {
			scope.Owner.Admission.Unlock()
		}
		if !e.Mu.TryLock() {
			t.Error("failed executor disposal held engine lock")
		} else {
			e.Mu.Unlock()
		}
		if !e.ReplacementMu.TryLock() {
			t.Error("failed executor disposal held replacement fence")
		} else {
			e.ReplacementMu.Unlock()
		}
		return rows.Close()
	}})
	core.SetPreparedRegistryPortals(core.PayloadPreparedSQL(core.PayloadOf(sess)), map[string]*core.ProtocolPortal{"p": p})
	e.ReplacementMu.RLock()
	scope.ReplacementRead = true
	_, err = scope.ExecuteProtocolPortal("p", 0, func(fields []session.ProtocolField) error {
		if len(fields) != 1 || string(fields[0].Bytes) != "1" {
			t.Error("actual store row was not delivered", fields)
		}
		cancel()
		return primary
	}, nil, nil, core.CopyProtocolHooks{})
	if !errors.Is(err, primary) || disposals != 1 || !rows.Closed {
		t.Fatal("failed executor lost primary or retained resources", err, disposals)
	}
	if core.PortalRetired(p) || core.PreparedRegistryPortals(core.PayloadPreparedSQL(core.PayloadOf(sess)))["p"] != p || core.PortalContext(p).Source != "retained source" || len(core.PortalColumns(p)) != 1 || core.PortalRows(p) != nil || core.PortalProducer(p) != nil || core.PortalRow(p) != nil || core.PortalScratch(p) != nil {
		t.Fatal("failed resource release removed identity or retained executor buffers")
	}
	// Restoring a frontier that includes this owner keeps its identity, without
	// reacquiring resources or repeating terminal callbacks.
	scope.Owner.Mu.Lock()
	core.RetireProtocolPortalsAfter(scope.Owner, 1)
	scope.Owner.Mu.Unlock()
	if err := core.DrainCursorCleanup(scope.Owner, op.Context(), scope); err != nil || core.PreparedRegistryPortals(core.PayloadPreparedSQL(core.PayloadOf(sess)))["p"] != p || disposals != 1 {
		t.Fatal("older failed identity did not survive frontier", err, disposals)
	}
	scope.Owner.Mu.Lock()
	core.RetireProtocolPortalsAfter(scope.Owner, 0)
	scope.Owner.Mu.Unlock()
	if err := core.DrainCursorCleanup(scope.Owner, op.Context(), scope); err != nil || core.PreparedRegistryPortals(core.PayloadPreparedSQL(core.PayloadOf(sess)))["p"] != nil || disposals != 1 {
		t.Fatal("retirement leaked identity or repeated executor disposal", err, disposals)
	}
}

func TestProtocolCopyCancellationContext(t *testing.T) {
	for _, wire := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded", true: "wire"}[wire], func(t *testing.T) {
			e := engine.New()
			session := sessionStart(t, e, nil)
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			op, _, err := session.AcquireCommand(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			core.SetProtocolScopeCommand(core.ScopeProtocol(scope), wire)
			defer scope.Finish(context.Canceled)
			cause := &sqlstate.Error{Code: "57014", Message: "canceling statement due to user request"}
			cancel(cause)
			// Line 1 is "1", read whole and valid, as the cursor sees it mid-COPY.
			in := copyfmt.NewLineReader(copyfmt.Options{}, bufio.NewReader(strings.NewReader("1\n")), nil)
			if present, err := in.ReadLine(); !present || err != nil {
				t.Fatal("line reader setup", present, err)
			}
			cursor := dml.NewCopyCursor(scope.WriteHost(), analyze.Binding{Relation: &catalog.Relation{ObjectName: "t"}}, in, nil)
			got := cursor.Diagnostic(context.Canceled)
			if !wire {
				if got != context.Canceled {
					t.Fatal("embedded caller cancellation changed identity", got)
				}
				return
			}
			var diagnostic *sqlstate.Error
			if !errors.As(got, &diagnostic) || diagnostic == cause || diagnostic.Code != "57014" || diagnostic.Context != "COPY t, line 1: \"1\"" || cause.Context != "" {
				t.Fatal("wire COPY lost real cursor context or mutated shared cause", got, cause)
			}
			primary := &sqlstate.Error{Code: "22012", Message: "division by zero", Context: "existing primary"}
			joined := errors.Join(primary, context.Canceled)
			if got := cursor.Diagnostic(joined); got != joined || primary.Context != "existing primary" || cause.Context != "" {
				t.Fatal("pending cancellation replaced or mutated primary diagnostic", got)
			}
		})
	}
}

func TestProtocolEndRequestedWake(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "observed_before_request", true: "observed_after_request"}[late], func(t *testing.T) {
			e := engine.New()
			sess := sessionStart(t, e, nil)
			op, err := sess.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			var wake <-chan struct{}
			if !late {
				wake = sess.EndRequested()
				select {
				case <-wake:
					t.Fatal("live session reported end request")
				default:
				}
			}
			sess.RequestEnd(session.CleanClose)
			if late {
				wake = sess.EndRequested()
			}
			select {
			case <-wake:
			default:
				t.Fatal("end request did not wake observer while lease remained")
			}
			if err := sess.Check(context.Background()); err == nil {
				t.Fatal("request wake preceded authoritative ending state")
			}
			select {
			case <-sess.Completed:
				t.Fatal("completion overtook outstanding operation lease")
			default:
			}
			sess.RequestEnd(session.CleanClose)
			if sess.EndRequested() != wake {
				t.Fatal("late/repeated observation lost the request lifetime")
			}
			op.Done()
			if err := sess.WaitEnd(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
