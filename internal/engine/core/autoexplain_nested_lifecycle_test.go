package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// Reuse the real diagnostics provider's admitted scan/transaction owners. The
// additional callback observes presentation failure before actual scan startup.
type autoExplainNestedProvider struct {
	diagnosticsLiveProvider
	presentationFailure error
}

func (p *autoExplainNestedProvider) ExplainForeignScan(context.Context, catalog.ForeignExplainScanRequest) ([]catalog.ForeignExplainProperty, error) {
	p.observe("explain")
	return nil, p.presentationFailure
}

func autoExplainNestedLifecycle(t *testing.T) {
	for _, outcome := range []string{"success", "provider_error", "cancel", "render_error"} {
		t.Run(outcome, func(t *testing.T) {
			e := engine.New()
			scan := &diagnosticsLiveScan{}
			provider := &autoExplainNestedProvider{diagnosticsLiveProvider: diagnosticsLiveProvider{scan: scan, tx: &plExceptionFaultTx{}}}
			wrapper, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Schema: "public", Name: "ae_nested_owner_handler"}, Wrapper: provider})
			if err != nil {
				t.Fatal(err)
			}
			if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{wrapper}, nil,
				func(_ context.Context, value driver.Value, usage sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
					if value == nil {
						return sqltype.Null(usage.Type), nil
					}
					return sqltype.Integer(usage.Type, value.(int64))
				},
				func(_ context.Context, value sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
					if value.IsNull() {
						return nil, nil
					}
					return value.Integer()
				},
			); err != nil {
				t.Fatal(err)
			}
			owner := enginetest.StartSession(t, e, nil)
			for _, query := range []string{
				"CREATE FOREIGN DATA WRAPPER ae_nested_owner HANDLER ae_nested_owner_handler",
				"CREATE SERVER ae_nested_owner_server FOREIGN DATA WRAPPER ae_nested_owner",
				"CREATE USER MAPPING FOR CURRENT_USER SERVER ae_nested_owner_server",
				"CREATE FOREIGN TABLE ae_nested_owner_data(v integer) SERVER ae_nested_owner_server",
				"CREATE FUNCTION pg_catalog.ae_nested_owner(key integer) RETURNS integer LANGUAGE plpgsql AS $$ DECLARE current_key integer := key; answer integer; BEGIN SELECT current_key INTO answer FROM ae_nested_owner_data; RETURN answer; END $$",
			} {
				enginetest.SessionSQL(t, owner, query)
			}
			autoExplainComposedConfigure(t, owner)
			enginetest.SessionSQL(t, owner, "SET auto_explain.log_nested_statements=on")
			var declaration *catalog.Procedure
			for candidate := range catalog.DefinitionProceduresNamedAt(e.CatalogRead(), nil, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, "ae_nested_owner") {
				declaration = candidate
			}
			if declaration == nil {
				t.Fatal("missing real nested declaration")
			}
			program, syntax := declaration.SQL.PL, declaration.SQL.PLSyntax
			const query = "SELECT pg_catalog.ae_nested_owner(COALESCE(-(7::integer),0)) AS v"
			ast, description := enginetest.PreparedDescription(t, e, owner, query)
			defer e.RetirePreparedDescription(&description)
			analysis, folds := description.Analysis, description.Folds
			resultBacking := &description.Results[0]
			op := enginetest.TransactionOperation(t, owner)
			op.OriginalSQL, op.SQLAvailable = query, true
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			explainExecutionCleanup(t, scope)
			scope.Expected = &description
			ctx, cancel := context.WithCancel(op.Context())
			defer cancel()
			op.Ctx = ctx
			primary := errors.New("nested actual owner failure")
			var frame *proc.PLFrame
			var retired func() bool
			var capturedContext string
			var lexical sqltype.Value
			seenLocal := false
			unlocked := plExceptionUnlocked(t, e, scope)
			observe := func(event string) {
				unlocked()
				if !core.AutoExplainActiveForTest(scope) {
					t.Error("actual nested owner has no observer", event)
					return
				}
				live := core.ScopeDiagnosticsLive(scope)
				if len(live) < 2 || live[0].Query != "SELECT current_key             FROM ae_nested_owner_data" || live[1].Definition != declaration.SQL || live[1].Frame == nil {
					t.Error("missing actual SQL/PL frame owners", event, live)
					return
				}
				if frame == nil {
					frame = live[1].Frame
					capturedContext = core.ScopeDiagnosticsContext(scope)
					_, _, retired = core.AutoExplainWitnessForTest(scope)
				}
				if frame != live[1].Frame || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(scope)) != frame || capturedContext != core.ScopeDiagnosticsContext(scope) {
					t.Error("nested presentation changed live lexical/context owner", event)
				}
				observation, nodes, _ := core.AutoExplainWitnessForTest(scope)
				if observation == nil {
					t.Error("actual nested owner lost its observation", event)
				}
				for _, node := range nodes {
					project, ok := node.(*plan.ProjectNode)
					if !ok {
						continue
					}
					for _, expression := range project.Expressions {
						plan.WalkExpression(expression, func(expression expr.Expr) {
							if local, ok := expression.(plan.PLLocalExpr); ok {
								seenLocal = true
								lexical = local.Value
								for slot, variable := range program.Variables {
									if variable.Name == "current_key" && (local.ResultUsage() != variable.Usage || !sqltype.SameDatum(local.Value, frame.Values[slot])) {
										t.Error("presentation replaced current typed lexical input")
									}
								}
							}
						})
					}
				}
			}
			provider.observe, scan.observe, provider.tx.check = observe, observe, unlocked
			switch outcome {
			case "provider_error":
				scan.primary = primary
			case "cancel":
				scan.cancel = cancel
			case "render_error":
				provider.presentationFailure = primary
			}
			result, executionErr := scope.Execute(ast, 0, nil)
			_, _, outerRetired := core.AutoExplainWitnessForTest(scope)
			if result.Rows != nil {
				if closeErr := result.Rows.Close(); executionErr == nil {
					executionErr = closeErr
				}
			}
			finishErr := scope.Finish(executionErr)
			if outcome == "success" {
				if finishErr != nil || len(op.Notices) != 2 {
					t.Fatal("real nested completion", finishErr, op.Notices)
				}
				if op.Notices[0].Context != capturedContext || op.Notices[1].Context != "" || strings.Count(op.Notices[0].Context, "PL/pgSQL function ") != 1 {
					t.Fatal("detached notice lost or duplicated its actual live context", capturedContext, op.Notices)
				}
			} else {
				cause := primary
				if outcome == "cancel" {
					cause = context.Canceled
				}
				if !errors.Is(finishErr, cause) || len(op.Notices) != 0 {
					t.Fatal("failed nested owner replaced primary or fabricated completion", finishErr, op.Notices)
				}
			}
			if frame == nil || retired == nil || !retired() || !outerRetired() || capturedContext == "" || frame.Values != nil || frame.HandledError() != nil || frame.RowCount() != 0 || core.AutoExplainActiveForTest(scope) || len(core.ScopeDiagnosticsLive(scope)) != 0 || len(core.ScopeDiagnosticsExpressionSites(scope)) != 0 || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(scope)) != nil || core.ScopeActiveSQLRoutineDepth(scope) != 0 {
				t.Fatal("terminal nested frame/value/context ownership retained")
			}
			if outcome == "render_error" {
				if scan.ends != 0 || scan.position != 0 {
					t.Fatal("rendering failure began or disposed an unentered source")
				}
			} else if !seenLocal || lexical.Type() == nil || scan.ends != 1 || scan.rescans != 0 {
				t.Fatal("witness did not reach/release the actual lexical graph and source")
			}
			if description.Analysis != analysis || description.Folds != folds || &description.Results[0] != resultBacking || declaration.SQL.PL != program || declaration.SQL.PLSyntax != syntax || program.ValidateDefinition(declaration.SQL) != nil {
				t.Fatal("nested observer changed retained description/program backing")
			}
			diagnosticsImmutableRecipe(t, declaration.SQL)
			enginetest.LockEmpty(t, e)
		})
	}
}
