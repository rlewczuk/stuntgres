package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type explainBoundaryWrapper struct {
	fdwUnitWrapper
	entered bool
}

var explainProviderExecuted = errors.New("EXPLAIN entered a foreign data callback")

func (w *explainBoundaryWrapper) GetForeignRelSize(context.Context, catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	w.entered = true
	return catalog.ForeignRelSize{}, explainProviderExecuted
}
func (w *explainBoundaryWrapper) GetForeignPaths(context.Context, catalog.ForeignRequest, catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	w.entered = true
	return nil, explainProviderExecuted
}
func (w *explainBoundaryWrapper) GetForeignPlan(context.Context, catalog.ForeignRequest, catalog.ForeignPath) (catalog.ForeignPlan, error) {
	w.entered = true
	return catalog.ForeignPlan{}, explainProviderExecuted
}
func (w *explainBoundaryWrapper) BeginForeignScan(context.Context, catalog.ForeignRequest, catalog.ForeignPlan) (catalog.ForeignScan, error) {
	w.entered = true
	return nil, explainProviderExecuted
}

func TestExplainNonExecutionBoundary(t *testing.T) {
	e := engine.New()
	wrapper := &explainBoundaryWrapper{}
	definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Name: "explain_boundary_handler"}, Wrapper: wrapper})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition}, nil,
		func(ctx context.Context, value driver.Value, usage sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
			wrapper.entered = true
			if err := ctx.Err(); err != nil {
				return sqltype.Value{}, err
			}
			if value == nil {
				return sqltype.Null(usage.Type), nil
			}
			return sqltype.Integer(usage.Type, value.(int64))
		},
		func(ctx context.Context, value sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
			wrapper.entered = true
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return enginetest.Export(value), nil
		}); err != nil {
		t.Fatal(err)
	}
	s := enginetest.StartSession(t, e, nil)
	for _, sql := range []string{
		"CREATE FOREIGN DATA WRAPPER explain_boundary_fdw HANDLER explain_boundary_handler",
		"CREATE SERVER explain_boundary_server FOREIGN DATA WRAPPER explain_boundary_fdw",
		"CREATE USER MAPPING FOR CURRENT_USER SERVER explain_boundary_server",
		"CREATE FOREIGN TABLE explain_boundary_foreign(id integer) SERVER explain_boundary_server",
		"CREATE SEQUENCE explain_boundary_s",
		"CREATE TABLE explain_boundary_d(id integer DEFAULT nextval('explain_boundary_s'))",
	} {
		enginetest.SessionSQL(t, s, sql)
	}
	// Ordinary executable definitions retain publication rights; an EXPLAIN
	// definition must not acquire those rights merely by sharing its binder.
	_, ordinary := sequencePreparedDescription(t, e, s, "SELECT abs(-7::integer),id FROM explain_boundary_foreign")
	if ordinary.Folds == nil || !ordinary.Folds.Live() || ordinary.Folds.Current().Generation != 0 || len(ordinary.Folds.Current().Entries) != 0 || ordinary.FoldPlanning != nil || wrapper.entered {
		t.Fatal("ordinary Describe lost its unevaluated fold-publication owner")
	}
	e.RetirePreparedDescription(&ordinary)
	for _, query := range []string{
		"EXPLAIN SELECT id FROM explain_boundary_foreign WHERE id=1 ORDER BY id",
		"EXPLAIN SELECT abs(-7::integer), nextval('explain_boundary_s') FROM explain_boundary_foreign",
		"EXPLAIN INSERT INTO explain_boundary_d DEFAULT VALUES RETURNING id",
		"EXPLAIN WITH w AS (INSERT INTO explain_boundary_d VALUES(99) RETURNING id) SELECT * FROM w",
		"EXPLAIN SELECT id FROM explain_boundary_d FOR UPDATE",
	} {
		ast, description := sequencePreparedDescription(t, e, s, query)
		if description.Folds == nil || description.FoldPlanning != nil || description.Folds.Live() {
			t.Fatal("Describe admitted child execution folds", query)
		}
		owner, generation := description.Folds, description.Folds.Current().Generation
		analysis := description.Analysis
		for range 2 {
			result, err := sequencePreparedExecute(e, s, ast, &description)
			if err != nil {
				t.Fatal(query, err)
			}
			if result.Rows == nil || result.Count != 0 || result.CommandTag != "EXPLAIN" {
				t.Fatal("EXPLAIN used child result/count", query, result.Count)
			}
			result.Rows.Close()
			if description.Folds != owner || description.Analysis != analysis || owner.Current().Generation != generation || owner.Live() || len(owner.Current().Entries) != 0 || description.FoldPlanning != nil {
				t.Fatal("EXPLAIN published/retained a child execution fold group", query)
			}
			if wrapper.entered {
				t.Fatal("planning invoked a foreign data callback", query)
			}
		}
	}
	explainProtocolBoundary(t, e, s, wrapper)
	// A real source open reaches the registered provider, distinguishing this
	// capability from an unused registration or an always-successful fake scan.
	op, cleanup, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = enginetest.ExecuteFrame(e, op, enginetest.Statement(t, "SELECT * FROM explain_boundary_foreign"), nil)
	cleanup()
	if !errors.Is(err, explainProviderExecuted) || !wrapper.entered {
		t.Fatal("real source did not reach installed capability", err, wrapper.entered)
	}
	// Cancellation inside rendering must release ordinary statement ownership.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, cleanup, err = enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	renderContext := &explainRenderCancelContext{Context: op.Ctx, cancel: cancel}
	op.Ctx = renderContext
	_, err = e.Execute(op, enginetest.Statement(t, "EXPLAIN SELECT id FROM explain_boundary_d"), 0, nil)
	cleanup()
	if !errors.Is(err, context.Canceled) || !renderContext.observed {
		t.Fatal("canceled explanation lost context error", err)
	}
	enginetest.SessionSQL(t, s, "DROP TABLE explain_boundary_d")
}

func TestExplainPresentation(t *testing.T) {
	explainAliasOwnership(t)
	// The real bound graph proves stage/occurrence ownership independently of
	// rendered strings; the public reference suite owns SQL-only presentation.
	e, query, _ := queryCoreFixture(t, `SELECT CASE WHEN "odd alias".id=2 THEN 'it''s' ELSE NULL END FROM t AS "odd alias" WHERE "odd alias".id=2 ORDER BY "odd alias".id DESC`)
	project, ok := query.Root.(*plan.ProjectNode)
	if !ok || len(project.Expressions) != 1 || project.Frame == 0 {
		t.Fatal("missing actual bound projection owner")
	}
	ordered, ok := project.Child.(*plan.SortNode)
	if !ok || len(ordered.Keys) != 1 || !ordered.Keys[0].Desc {
		t.Fatal("missing actual bound ordering")
	}
	filter, ok := ordered.Child.(*plan.FilterNode)
	if !ok || filter.Predicate == nil {
		t.Fatal("missing actual bound predicate")
	}
	source, ok := filter.Child.(*plan.SourceNode)
	if !ok || source.RangeName != "odd alias" || source.Ref.Domain() == nil {
		t.Fatal("lost original bound source identity", source)
	}
	before := source.Ref
	result := enginetest.Run(t, e, `EXPLAIN (VERBOSE, COSTS false) SELECT CASE WHEN "odd alias".id=2 THEN 'it''s' ELSE NULL END FROM t AS "odd alias" WHERE "odd alias".id=2 ORDER BY "odd alias".id DESC`)
	rows := enginetest.Tuples(result)
	var lines []string
	for _, row := range rows {
		if len(row) != 1 {
			t.Fatal(row)
		}
		text, ok := row[0].(string)
		if !ok {
			t.Fatal("not owned plan text", row)
		}
		lines = append(lines, text)
	}
	text := strings.Join(lines, "\n")
	for _, fragment := range []string{`"odd alias".id`, "CASE", "'it''s'", "NULL", "DESC", "Output:", "public.t"} {
		if !strings.Contains(text, fragment) {
			t.Errorf("missing independently expected %q: %s", fragment, text)
		}
	}
	if source.Ref != before || !reflect.DeepEqual(project.Schema(), query.Root.Schema()) {
		t.Fatal("rendering retargeted/mutated the borrowed bound graph")
	}
}
