package core_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestClientEncodingOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE ce_resources(id int, v text)")
	enginetest.SessionSQL(t, s, "INSERT INTO ce_resources VALUES (1,'é'),(2,'€')")
	enginetest.SessionSQL(t, s, "SET client_encoding='LATIN1'")
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(f func(*core.StatementScope) error) error {
		op, _, err := s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartProtocolCommand(op, cycle)
		if err != nil {
			t.Fatal(err)
		}
		defer scope.Finish(context.Canceled)
		err = f(scope)
		return scope.Finish(err)
	}
	query := "SELECT v FROM ce_resources ORDER BY id"
	if err := command(func(scope *core.StatementScope) error { _, err := scope.ParseProtocol("s", query, nil); return err }); err != nil {
		t.Fatal(err)
	}
	if err := command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("p", "s", &protocolUnitInput{data: make([]byte, 6)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	portal := core.ProtocolPortalOf(core.PayloadOf(s).PreparedSQL.Binding("p"))
	if portal == nil {
		t.Fatal("actual Bind did not publish portal")
	}
	calls := 0
	var retained []byte
	if err := command(func(scope *core.StatementScope) error {
		result, err := scope.ExecuteProtocolPortal("p", 1, func(fields []session.ProtocolField) error {
			calls++
			if len(fields) != 1 || fields[0].Null || !bytes.Equal(fields[0].Bytes, []byte{0xe9}) {
				t.Fatal("first encoded field", fields)
			}
			retained = bytes.Clone(fields[0].Bytes)
			return nil
		}, nil, nil, core.CopyProtocolHooks{})
		if !result.Suspended {
			t.Fatal("first actual demand did not suspend")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	producer := core.PortalProducer(portal)
	if calls != 1 || producer == nil || producer.Root == nil || producer.Source == nil || core.CursorStage(producer) == nil {
		t.Fatal("no live producer at suspended frontier")
	}
	err := command(func(scope *core.StatementScope) error {
		_, err := scope.ExecuteProtocolPortal("p", 0, func([]session.ProtocolField) error { calls++; return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22P05" || calls != 1 || !bytes.Equal(retained, []byte{0xe9}) {
		t.Fatal("conversion published a partial second datum or changed retained output", err, calls, retained)
	}
	if core.PortalProducer(portal) != nil || core.PortalRows(portal) != nil || core.PortalRow(portal) != nil || core.PortalScratch(portal) != nil || producer.Root != nil || producer.Source != nil || core.CursorStage(producer) != nil {
		t.Fatal("conversion terminal retained actual producer graph/frame/buffers")
	}
	if core.PortalContext(portal).Source != query {
		t.Fatal("executor cleanup erased surviving diagnostic recipe")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
	// The same failure while writing COPY must release its accepted copy owner
	// and preparation state, while preserving only already complete row bytes.
	var output bytes.Buffer
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	core.SetProtocolScopeCommand(core.ScopeProtocol(scope), true)
	count, copyErr := core.ScopeCopyProtocol(scope, enginetest.Statement(t, "COPY (SELECT v FROM ce_resources ORDER BY id) TO STDOUT").(*parser.Copy), nil, &output, core.CopyProtocolHooks{})
	if !errors.As(copyErr, &diagnostic) || diagnostic.Code != "22P05" || count != 0 || !bytes.Equal(output.Bytes(), []byte{0xe9, '\n'}) {
		t.Fatal("COPY output terminal", count, copyErr, output.Bytes())
	}
	if core.ProtocolScopeBind(core.ScopeProtocolOrZero(scope)) != nil || core.ForeignTableScopeCursor(core.ScopeForeignTablesOrZero(scope)) != nil {
		t.Fatal("COPY output retained preparation")
	}
	// copyProtocol keeps the reservation through statement completion so Finish
	// can dispose accepted resources before waking another owner.
	outputDone := scope.Owner.CopyDone
	if scope.Owner.CopyLease == nil || outputDone == nil {
		t.Fatal("COPY output lost its pre-Finish reservation")
	}
	if finishErr := scope.Finish(copyErr); !errors.Is(finishErr, copyErr) {
		t.Fatal("COPY output cleanup replaced conversion failure", finishErr)
	}
	op.Done()
	if scope.Owner.CopyLease != nil || scope.Owner.CopyDone != nil {
		t.Fatal("COPY output Finish retained accepted owner")
	}
	select {
	case <-outputDone:
	default:
		t.Fatal("COPY output Finish did not wake reservation waiters")
	}
	enginetest.SessionSQL(t, s, "SET client_encoding='SJIS'")
	op, _, err = s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err = e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	core.SetProtocolScopeCommand(core.ScopeProtocol(scope), true)
	count, copyErr = core.ScopeCopyProtocol(scope, enginetest.Statement(t, "COPY ce_resources(v) FROM STDIN").(*parser.Copy), strings.NewReader("\x83\x5c\n\x82"), nil, core.CopyProtocolHooks{})
	if !errors.As(copyErr, &diagnostic) || diagnostic.Code != "22021" || count != 0 || len(scope.Stage.Rows) != 0 {
		t.Fatal("COPY input conversion applied prefix rows", count, copyErr)
	}
	if core.ProtocolScopeBind(core.ScopeProtocolOrZero(scope)) != nil || core.ForeignTableScopeCursor(core.ScopeForeignTablesOrZero(scope)) != nil {
		t.Fatal("COPY input retained preparation")
	}
	inputDone := scope.Owner.CopyDone
	if scope.Owner.CopyLease == nil || inputDone == nil {
		t.Fatal("COPY input lost its pre-Finish reservation")
	}
	if finishErr := scope.Finish(copyErr); !errors.Is(finishErr, copyErr) {
		t.Fatal("COPY input cleanup replaced conversion failure", finishErr)
	}
	op.Done()
	if scope.Owner.CopyLease != nil || scope.Owner.CopyDone != nil {
		t.Fatal("COPY input Finish retained accepted owner")
	}
	select {
	case <-inputDone:
	default:
		t.Fatal("COPY input Finish did not wake reservation waiters")
	}
}
