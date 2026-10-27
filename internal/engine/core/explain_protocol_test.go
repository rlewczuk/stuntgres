package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func explainProtocolBoundary(t *testing.T, e *core.Engine, s *session.Session, wrapper *explainBoundaryWrapper) {
	t.Helper()
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) {
		preparedFoldProtocolCommand(t, e, s, cycle, fn)
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("explain_definition", "EXPLAIN SELECT abs(-7::integer),nextval('explain_boundary_s'),id FROM explain_boundary_foreign WHERE id=$1", nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("explain_definition")
	owner := definition.Description.Folds
	if owner == nil || owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse entered child execution folding")
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("explain_portal", "explain_definition", &protocolUnitInput{data: []byte{0, 0, 0, 1, 0, 0, 0, 1, '2', 0, 0}})
		return err
	})
	binding := registry.Binding("explain_portal")
	if binding == nil || binding.Entry.Description.Folds != owner || binding.Resource == nil || binding.Resource.Root != nil || owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 || wrapper.entered {
		t.Fatal("Bind retained/opened/folded the child graph")
	}
	command(func(scope *core.StatementScope) error {
		description, err := scope.DescribeProtocolPortal("explain_portal")
		if err == nil && (len(description.Columns) != 1 || description.Columns[0].Name != "QUERY PLAN" || description.Columns[0].Usage.Type.ID() != sqltype.Text) {
			t.Fatal("portal described child columns", description.Columns)
		}
		return err
	})
	rows := 0
	command(func(scope *core.StatementScope) error {
		result, err := scope.ExecuteProtocolPortal("explain_portal", 0, func(fields []session.ProtocolField) error {
			if len(fields) != 1 {
				t.Fatal("child row reached protocol output", fields)
			}
			rows++
			return nil
		}, nil, nil, core.CopyProtocolHooks{})
		if err == nil && (result.CommandTag != "EXPLAIN" || rows == 0) {
			t.Fatal("wrong utility completion", result.CommandTag, rows)
		}
		return err
	})
	if wrapper.entered || binding.Resource.Root != nil || owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Execute published/opened/retained child execution state")
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("explain_portal") })
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("explain_definition") })
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
