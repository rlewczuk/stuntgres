package engine

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// This reader exercises staged engine consumption, not SQL or a fake backend.
type protocolUnitInput struct {
	data     []byte
	position int
}

func (r *protocolUnitInput) Bytes(n int) ([]byte, error) {
	if n < 0 || n > len(r.data)-r.position {
		return nil, sqlError("08P01", "insufficient data left in message")
	}
	p := r.data[r.position : r.position+n]
	r.position += n
	return p, nil
}
func (r *protocolUnitInput) Uint16() (uint16, error) {
	p, e := r.Bytes(2)
	if e != nil {
		return 0, e
	}
	return binary.BigEndian.Uint16(p), nil
}
func (r *protocolUnitInput) Int32() (int32, error) {
	p, e := r.Bytes(4)
	if e != nil {
		return 0, e
	}
	return int32(binary.BigEndian.Uint32(p)), nil
}
func (r *protocolUnitInput) End() error {
	if r.position != len(r.data) {
		return sqlError("08P01", "invalid message format")
	}
	return nil
}

func TestProtocolPreparedRegistry(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	r := a.preparedSQL
	entry := &sqlPreparedStatement{body: &parser.Select{}}
	p := &protocolPortal{entry: *entry, context: ProtocolContext{Source: "original"}}
	r.entries["s"] = entry
	r.portals = map[string]*protocolPortal{"p": p}
	delete(r.entries, "s")
	if p.entry.body != entry.body || p.context.Source != "original" {
		t.Fatal("definition removal lost retained portal recipe")
	}
	if err := r.EndSession(b.token, CleanClose); err == nil || p.retired {
		t.Fatal("foreign retirement modified registry", err)
	}
	if err := r.EndSession(a.token, CleanClose); err != nil || !p.retired || len(r.portals) != 0 {
		t.Fatal("owned retirement retained portal", err)
	}
	if protocolHasRows(&parser.Select{}, nil) != true || protocolHasRows(&parser.CreateTable{}, nil) {
		t.Fatal("zero-column rows confused with NoData")
	}
}

func TestProtocolValueCodecs(t *testing.T) {
	t.Run("deferred-record-input", func(t *testing.T) {
		e := New()
		op, done, err := testOperation(e, context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer done()
		for _, retained := range []bool{false, true} {
			record := sqltype.DeferredRecord()
			description := StatementDescription{Parameters: analyze.ParameterInfo{Count: 1, Types: []sqltype.Usage{record.Usage()}}}
			e.mu.Lock()
			var expected *StatementDescription
			if retained {
				expected = &description
			}
			binder := newExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, expected)
			if !retained {
				binder.SeedParameters(description.Parameters.Types)
			}
			node, bindErr := binder.Bind(parser.ParameterExpr{Number: 1}, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			formal, formalErr := binder.Finish()
			e.mu.Unlock()
			if bindErr != nil || formalErr != nil {
				t.Fatal(bindErr, formalErr)
			}
			if node.ResultUsage().Type != record || formal.Types[0].Type != record {
				t.Fatal("parameter rebound occurrence codec to lookup-only RECORD")
			}
			value := sqltype.Null(formal.Types[0].Type)
			if !value.IsNull() || value.Type() != record {
				t.Fatal("deferred NULL lost occurrence identity")
			}
			_, inputErr := formal.Types[0].Type.InputWithEnvironment(op.Context(), "(1)", -1, sqltype.InputEnvironment{})
			var diagnostic *sqlstate.Error
			if !errors.As(inputErr, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "input of anonymous composite types is not implemented" {
				t.Fatal("actual RECORD input boundary lost", inputErr)
			}
		}
	})
	t.Run("captured-role-output", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		op, _, err := s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		role := testUsage(e, sqltype.Regrole).Type
		value, err := role.InputWithEnvironment(op.Context(), "10", -1, sqltype.InputEnvironment{})
		if err != nil {
			_ = scope.Finish(err)
			t.Fatal(err)
		}
		rows := exec.NewFinalRows(nil, 1)
		for _, quoted := range []bool{false, true, false} {
			scope.output.QuoteAllIdentifiers = quoted
			err = scope.captureProtocolOutput(rows, []sqltype.Value{value}, e.CatalogRead())
			if err != nil {
				break
			}
			var text string
			text, err = role.OutputWithEnvironment(op.Context(), value, rows.OutputEnvironment())
			want := "postgres"
			if quoted {
				want = `"postgres"`
			}
			if err == nil && text != want {
				t.Errorf("borrowed role reader retained earlier presentation: got %q, want %q", text, want)
			}
			if err != nil {
				break
			}
		}
		if err = scope.Finish(err); err != nil {
			t.Fatal(err)
		}
	})
	e := New()
	usage := testUsage(e, sqltype.Text)
	empty := sqltype.String(usage.Type, "")
	large := sqltype.String(usage.Type, string(bytes.Repeat([]byte{'x'}, 4096)))
	values := []sqltype.Value{empty, large, sqltype.Null(usage.Type)}
	fields, scratch, err := encodeProtocolRow(context.Background(), values, []int16{0}, sqltype.OutputEnvironment{}, nil, make([]byte, 0, 1))
	if err != nil || len(fields) != 3 || fields[0].Null || len(fields[0].Bytes) != 0 || !bytes.Equal(fields[1].Bytes, bytes.Repeat([]byte{'x'}, 4096)) || !fields[2].Null {
		t.Fatal("scratch growth or NULL/empty identity lost", fields, err)
	}
	fields, scratch, err = encodeProtocolRow(context.Background(), []sqltype.Value{large, empty}, []int16{1}, sqltype.OutputEnvironment{}, fields, scratch)
	if err != nil || fields[0].Null || len(fields[0].Bytes) != 4096 || fields[1].Null || len(fields[1].Bytes) != 0 || len(scratch) != 4096 {
		t.Fatal("reused output scratch lost field boundaries", err)
	}
	// Invalid result selectors are used only when there is an actual column.
	input := &protocolUnitInput{data: []byte{0, 1, 0, 9}}
	if got, err := protocolResultFormats(input, 0); err != nil || len(got) != 0 {
		t.Fatal("unused result selector was evaluated", got, err)
	}
}

func TestProtocolCycleRetention(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	cycle := s.NewProtocolCycle()
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartProtocolCommand(op, cycle)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	if scope.message || !s.transactions.retainsTransaction() {
		t.Fatal("cycle borrowed simple-message admission")
	}
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	op.Done()
	// A subsequent independent operation must acquire admission, not borrow the
	// ended operation. The token itself must own neither lease nor scope.
	next, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	nextScope, err := e.StartProtocolCommand(next, cycle)
	if err != nil {
		next.Done()
		t.Fatal(err)
	}
	if nextScope.op == scope.op || nextScope == scope {
		t.Fatal("protocol message reused ended operation")
	}
	nextScope.ProtocolSimpleQuery(1)
	if cycle.active {
		t.Fatal("Parse/Bind-only simple boundary retained implicit block")
	}
	if err = nextScope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	next.Done()
	foreign := sessionStart(t, e, nil)
	foreignOp, _, err := foreign.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer foreignOp.Done()
	if _, err = e.StartProtocolCommand(foreignOp, cycle); err == nil {
		t.Fatal("foreign physical session accepted cycle")
	}
}

func TestProtocolNotificationPoll(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	_, available, wake, err := s.PollProtocolNotification()
	if err != nil || available || wake == nil {
		t.Fatal(available, wake, err)
	}
	s.mu.Lock()
	leases := s.timeouts.sqlLeases
	s.mu.Unlock()
	if leases != 0 {
		t.Fatal("idle poll acquired SQL activity lease")
	}
	e.mu.Lock()
	s.notifications.state = &notificationState{ready: notificationFIFO{}.append([]Notification{{PID: 42, Channel: "c", Payload: "first"}, {PID: 42, Channel: "c", Payload: "second"}})}
	s.notifications.signalLocked()
	e.mu.Unlock()
	select {
	case <-wake:
	default:
		t.Fatal("atomic poll wake lost publication")
	}
	for _, want := range []string{"first", "second"} {
		got, available, _, err := s.PollProtocolNotification()
		if err != nil || !available || got.Payload != want || got.PID != 42 {
			t.Fatal(got, available, err)
		}
	}
	_, available, wake, err = s.PollProtocolNotification()
	if err != nil || available || wake == nil {
		t.Fatal("drained queue lacked new wake", err)
	}
	if err = s.notifications.EndSession(s.token, CleanClose); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake:
	default:
		t.Fatal("retirement did not wake idle observer")
	}
	if _, _, _, err = s.PollProtocolNotification(); err == nil {
		t.Fatal("retired observer accepted poll")
	}
}

func TestProtocolFunctionInvocation(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	// Foreign/missing physical authority never consumes even the OID field.
	s.transactions.mu.Lock()
	s.transactions.state = TxAborted
	s.transactions.mu.Unlock()
	input := &protocolUnitInput{}
	_, err = scope.CallProtocolFunction(input)
	if err == nil || input.position != 0 {
		t.Fatal("aborted gate consumed fastpath input", err)
	}
	if err = scope.Finish(err); err == nil {
		t.Fatal("failed invocation lost primary diagnostic")
	}
}

func TestProtocolCopyHooks(t *testing.T) {
	t.Run("terminal-veto", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(v INT)")
		op, _, err := s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		sentinel := errors.New("protocol terminal failure")
		called := false
		e.mu.Lock()
		frontier := e.visibility.CommitSeq()
		e.mu.Unlock()
		count, err := scope.CopyProtocol(copyUnitSyntax(), bytes.NewBufferString("1\n\\.\nignored"), nil, CopyProtocolHooks{FinishInput: func() error {
			called = true
			if !s.transactions.admission.TryLock() {
				t.Fatal("terminal hook held owner admission")
			}
			s.transactions.admission.Unlock()
			if len(scope.stage.rows) != 0 {
				t.Fatal("input rows applied before terminal veto")
			}
			return sentinel
		}})
		if count != 0 || !called || !errors.Is(err, sentinel) || len(scope.stage.rows) != 0 {
			t.Fatal("terminal failure applied staged input", count, called, err)
		}
		if err = scope.Finish(err); !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
		e.mu.Lock()
		published := e.visibility.CommitSeq() != frontier
		e.mu.Unlock()
		if published {
			t.Fatal("terminal veto published rows")
		}
	})
	e := New()
	s := sessionStart(t, e, nil)
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	// A real reservation is required; optional nil hook does not create one.
	sentinel := errors.New("start veto")
	if err = scope.startProtocolCopy(CopyProtocolHooks{}, true, copyOptions{}, 2); err != nil {
		t.Fatal(err)
	}
	if err = scope.reserveCopy(); err != nil {
		t.Fatal(err)
	}
	called := false
	err = scope.startProtocolCopy(CopyProtocolHooks{Start: func(meta CopyProtocolMetadata) error {
		called = true
		if !meta.From || meta.Format != 1 || len(meta.ColumnFormats) != 2 || meta.ColumnFormats[0] != 1 || meta.ColumnFormats[1] != 1 {
			t.Fatal(meta)
		}
		if !s.transactions.admission.TryLock() {
			t.Fatal("COPY Start held owner admission")
		}
		s.transactions.admission.Unlock()
		return sentinel
	}}, true, copyOptions{format: copyFormatBinary}, 2)
	if !called || !errors.Is(err, sentinel) {
		t.Fatal("start callback primary error lost", err)
	}
	if finish := scope.Finish(err); !errors.Is(finish, sentinel) {
		t.Fatal(finish)
	}
}
