package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

type preparedQueryProvider struct {
	fdwUnitWrapper
	failure error
	begins  int
}

func (p *preparedQueryProvider) GetForeignJoinPaths(_ context.Context, r catalog.ForeignQueryRequest) ([]catalog.ForeignQueryPath, error) {
	return []catalog.ForeignQueryPath{{TotalCost: 1, Compatibility: catalog.ForeignQueryCompatibility{Expressions: true, Collations: true, StatementVisibility: true}, Plan: catalog.ForeignQueryPlan{Slots: append([]catalog.ForeignQuerySlot{}, r.Stage.Slots...)}}}, nil
}
func (p *preparedQueryProvider) BeginForeignQueryScan(_ context.Context, r catalog.ForeignQueryRequest, _ catalog.ForeignQueryPlan) (catalog.ForeignScan, error) {
	p.begins++
	row := make(catalog.ForeignRow, len(r.Stage.Slots))
	for i, slot := range r.Stage.Slots {
		if slot.Presence != 0 {
			row[i] = true
		} else {
			row[i] = int64(1)
		}
	}
	return &fdwDispatchPartition{rows: []catalog.ForeignRow{row}}, p.failure
}

func preparedQueryFixture(t *testing.T) (*core.Engine, *session.Session, *preparedQueryProvider) {
	t.Helper()
	e := engine.New()
	provider := &preparedQueryProvider{}
	definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "unit_query_handler"}, Wrapper: provider})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition}, nil, func(_ context.Context, value driver.Value, usage sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
		if value == nil {
			return sqltype.Null(usage.Type), nil
		}
		return sqltype.Integer(usage.Type, value.(int64))
	}, func(_ context.Context, value sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
		if value.IsNull() {
			return nil, nil
		}
		return value.Integer()
	}); err != nil {
		t.Fatal(err)
	}
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE FOREIGN DATA WRAPPER unit_query HANDLER unit_query_handler", "CREATE SERVER unit_query_server FOREIGN DATA WRAPPER unit_query", "CREATE USER MAPPING FOR CURRENT_USER SERVER unit_query_server", "CREATE FOREIGN TABLE query_left(k integer) SERVER unit_query_server", "CREATE FOREIGN TABLE query_right(k integer) SERVER unit_query_server"} {
		enginetest.SessionSQL(t, s, q)
	}
	return e, s, provider
}

func TestFDWQueryPreparedArtifactIsolation(t *testing.T) {
	e, s, provider := preparedQueryFixture(t)
	ast, description := sequencePreparedDescription(t, e, s, "SELECT COALESCE(-(7::integer),0),l.k FROM query_left l JOIN query_right r ON l.k=r.k")
	preparedFoldExecute(t, e, s, ast, &description)
	owner := description.Folds
	accepted := owner.Current()
	analysis := description.Analysis
	planGeneration := description.PlanGeneration
	if len(accepted.Entries) == 0 || provider.begins != 1 {
		t.Fatal("missing real prepared fold/composite owner")
	}
	entries := &accepted.Entries[0]
	failure := errors.New("execution-private query Begin failed")
	provider.failure = failure
	result, err := sequencePreparedExecute(e, s, ast, &description)
	if result.Rows != nil {
		result.Rows.Close()
		t.Fatal("failed execution published rows")
	}
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	current := owner.Current()
	if description.Folds != owner || description.Analysis != analysis || description.PlanGeneration != planGeneration || current.Generation != accepted.Generation || len(current.Entries) != len(accepted.Entries) || &current.Entries[0] != entries {
		t.Fatal("execution-private failure changed accepted prepared ownership/backing")
	}
	provider.failure = nil
	preparedFoldExecute(t, e, s, ast, &description)
	current = owner.Current()
	if current.Generation != accepted.Generation || &current.Entries[0] != entries || provider.begins != 3 {
		t.Fatal("successful replacement did not reuse immutable prepared artifacts")
	}
}

func TestFDWQueryProtocolArtifactIsolation(t *testing.T) {
	e, s, provider := preparedQueryFixture(t)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("query_definition", "SELECT COALESCE(-(7::integer),0),l.k FROM query_left l JOIN query_right r ON l.k=r.k", nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("query_definition")
	for _, name := range []string{"successful_query", "failed_query"} {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "query_definition", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		binding := registry.Binding(name)
		if binding == nil || binding.Folds == nil || binding.Resource == nil || binding.Resource.Root == nil {
			t.Fatal("missing accepted definition/binding/resource")
		}
		root := binding.Resource.Root
		child := root.Child
		expression := root.Expressions[0]
		schema := &root.QuerySchema[0]
		boundFolds := *binding.Folds
		accepted := definition.Description.Folds.Current()
		if len(accepted.Entries) == 0 || len(boundFolds.Entries) == 0 {
			t.Fatal("missing immutable prepared backing")
		}
		failure := errors.New("private protocol Begin failure")
		if name == "failed_query" {
			provider.failure = failure
		}
		err := preparedFoldProtocolTry(t, e, s, cycle, false, func(scope *core.StatementScope) error {
			_, err := scope.ExecuteProtocolPortal(name, 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
			return err
		})
		if name == "failed_query" {
			if !errors.Is(err, failure) {
				t.Fatal(err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		current := definition.Description.Folds.Current()
		if registry.Lookup("query_definition") != definition || current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] || root.Child != child || !reflect.DeepEqual(root.Expressions[0], expression) || &root.QuerySchema[0] != schema {
			t.Fatal("private execution mutated definition generation/graph/backing")
		}
		if binding.Resource.Root != nil && binding.Resource.Root != root {
			t.Fatal("terminal cleanup installed a replacement retained graph")
		}
		if boundFolds.Generation != accepted.Generation || &boundFolds.Entries[0] != &accepted.Entries[0] {
			t.Fatal("accepted binding did not borrow original immutable fold backing")
		}
	}
	if provider.begins != 2 {
		t.Fatal("protocol did not execute both optional query owners", provider.begins)
	}
}
