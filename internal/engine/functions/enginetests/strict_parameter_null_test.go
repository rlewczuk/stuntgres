package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Bind actual supported providers under genuine catalog/statement authority.
// SQL creates incidental state; assertions exercise private planning and original
// typed frames, not public SQL response witnesses or synthetic probe providers.
func strictNullUnitFixture(t *testing.T) (*core.Engine, *session.Operation, *core.StatementScope) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	for _, query := range []string{
		"CREATE TABLE strict_null_rows(v integer)",
		"CREATE SEQUENCE strict_null_steps START 1",
		"CREATE DOMAIN strict_null_checked AS integer CONSTRAINT strict_null_present CHECK(VALUE IS NOT NULL)",
	} {
		enginetest.RoleExecute(t, e, op, query)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	return e, op, scope
}

func strictNullUnitBind(t *testing.T, e *core.Engine, op *session.Operation, scope *core.StatementScope, text string) (expr.Expr, *analyze.Binder, exec.Context) {
	t.Helper()
	statements, err := parser.Parse("SELECT " + text)
	if err != nil {
		t.Fatal("actual provider parsing", text, err)
	}
	if len(statements) != 1 {
		t.Fatal("expected one provider statement", text, len(statements))
	}
	raw := statements[0].(*parser.Select).Targets[0]
	ref := enginetest.RoleTableRef(t, e, "strict_null_rows")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b := core.NewExpressionBinder(context.Background(), e, scope.Input, op, nil)
	b.Query.Auth, b.Target.Ref = &auth, ref
	b.Target.Relation = auth.Catalog.MustRelation(ref.Address)
	bindTestTargetNamespace(b)
	b.Clause.AggregateClause = "SELECT"
	node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	e.Mu.Unlock()
	if err != nil {
		t.Fatal("actual provider binding", text, err)
	}
	return node, b, exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
}

// Snapshot each lexical occurrence independently: a shallow root copy alone
// would share Math.Args and miss writes into the analyzed operand backing.
func strictNullSourceUnchanged(t *testing.T, root expr.Expr) func() {
	t.Helper()
	type occurrence struct {
		node     expr.Expr
		info     expr.Info
		children []expr.Expr
		backing  *expr.Expr
	}
	var before []occurrence
	plan.WalkExpression(root, func(node expr.Expr) {
		saved := occurrence{node: node, info: expr.InfoOf(node)}
		plan.ExpressionChildren(node, func(child expr.Expr) {
			saved.children = append(saved.children, child)
		})
		if math, ok := node.(functions.Math); ok && len(math.Args) != 0 {
			saved.backing = &math.Args[0]
		}
		before = append(before, saved)
	})
	return func() {
		t.Helper()
		index := 0
		plan.WalkExpression(root, func(node expr.Expr) {
			if index >= len(before) {
				t.Fatal("source gained an occurrence")
			}
			saved := before[index]
			index++
			if !reflect.DeepEqual(node, saved.node) || expr.InfoOf(node) != saved.info {
				t.Fatal("source provider, Info or authority changed", index)
			}
			var children []expr.Expr
			plan.ExpressionChildren(node, func(child expr.Expr) { children = append(children, child) })
			if !reflect.DeepEqual(children, saved.children) {
				t.Fatal("source lexical children changed", index)
			}
			if math, ok := node.(functions.Math); ok && len(math.Args) != 0 && &math.Args[0] != saved.backing {
				t.Fatal("source operand backing changed", index)
			}
		})
		if index != len(before) {
			t.Fatal("source lost an occurrence", index, len(before))
		}
	}
}

func TestStrictParameterNullPlanning(t *testing.T) {
	planners := []struct {
		name string
		run  func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error)
	}{{"preflight", exec.PreflightCoercionsWithPolicy}, {"conditional", exec.SimplifyConditional}}
	for _, planner := range planners {
		t.Run(planner.name, func(t *testing.T) {
			e, op, scope := strictNullUnitFixture(t)
			for _, expression := range []string{"abs($1::integer)", "abs(CASE WHEN TRUE THEN $1 ELSE v END)", "abs(($1::smallint)::integer)"} {
				t.Run(expression, func(t *testing.T) {
					node, b, evaluation := strictNullUnitBind(t, e, op, scope, expression)
					evaluation.Parameters = []sqltype.Value{sqltype.Null(b.Params.Parameters[1].Type)}
					planned, changed, err := planner.run(&evaluation, node, true)
					if err != nil || !changed {
						t.Fatal("known NULL did not prune", changed, err)
					}
					value, err := planned.Eval(&evaluation)
					if err != nil || !value.IsNull() || value.Type() != node.ResultUsage().Type {
						t.Fatal("typed strict result", value, err)
					}
					if planned.ResultUsage() != node.ResultUsage() || planned.Properties() != node.Properties() {
						t.Fatal("lost conservative result/parameter/row authority")
					}
					// Reusing the original analyzed expression with a non-NULL
					// frame must still execute the original selected provider.
					input, err := sqltype.Integer(b.Params.Parameters[1].Type, -7)
					if err != nil {
						t.Fatal(err)
					}
					evaluation.Parameters[0] = input
					again, _, err := planner.run(&evaluation, node, true)
					if err != nil {
						t.Fatal(err)
					}
					value, err = again.Eval(&evaluation)
					if err != nil || value.IsNull() {
						t.Fatal("original binding was mutated", value, err)
					}
					got, err := value.Integer()
					if err != nil || got != 7 {
						t.Fatal("non-NULL original invocation", got, err)
					}
				})
			}
			// A real volatile sibling is never evaluated merely to discover
			// strict NULL. The sequence supplies an observable effect boundary.
			node, b, evaluation := strictNullUnitBind(t, e, op, scope, "($1::integer)+nextval('strict_null_steps'::regclass)::integer")
			evaluation.Parameters = []sqltype.Value{sqltype.Null(b.Params.Parameters[1].Type)}
			sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "strict_null_steps")
			planned, changed, err := planner.run(&evaluation, node, true)
			if err != nil || !changed {
				t.Fatal("volatile sibling strict pruning", changed, err)
			}
			if value, err := planned.Eval(&evaluation); err != nil || !value.IsNull() {
				t.Fatal(value, err)
			}
			e.Mu.Lock()
			called := e.Storage.Sequences[sequence.Address].Called
			e.Mu.Unlock()
			if called {
				t.Fatal("NULL proof evaluated a genuine volatile provider")
			}
			// Ordinary immutable argument failures still precede this rule.
			node, b, evaluation = strictNullUnitBind(t, e, op, scope, "($1::integer)+(1/0)")
			evaluation.Parameters = []sqltype.Value{sqltype.Null(b.Params.Parameters[1].Type)}
			_, _, err = planner.run(&evaluation, node, true)
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
			// Imported parameters are available to this demanded arithmetic
			// sibling even though the analyzed recipe remains parameter-dependent.
			// Planning must report its error before pruning the known-NULL parent,
			// and fresh frames must neither reuse its error nor mutate the graph.
			node, b, evaluation = strictNullUnitBind(t, e, op, scope, "($1::integer)+(10/$2::integer)")
			for _, tc := range []struct {
				null        bool
				left, right int64
				fail        bool
				want        any
			}{
				{true, 0, 0, true, nil}, {true, 0, 2, false, nil},
				{false, 7, 2, false, int64(12)}, {true, 0, 0, true, nil},
			} {
				left, err := sqltype.Integer(b.Params.Parameters[1].Type, tc.left)
				if err != nil {
					t.Fatal(err)
				}
				if tc.null {
					left = sqltype.Null(b.Params.Parameters[1].Type)
				}
				right, err := sqltype.Integer(b.Params.Parameters[2].Type, tc.right)
				if err != nil {
					t.Fatal(err)
				}
				evaluation.Parameters = []sqltype.Value{left, right}
				planned, _, err := planner.run(&evaluation, node, true)
				if tc.fail {
					nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
				} else {
					if err != nil {
						t.Fatal(err)
					}
					value, err := planned.Eval(&evaluation)
					if got := enginetest.Export(value); err != nil || got != tc.want {
						t.Fatal("fresh arithmetic frame", got, tc.want, err)
					}
				}
				var numbers []int
				plan.WalkExpression(node, func(child expr.Expr) {
					if p, ok := child.(plan.ParameterExpr); ok {
						numbers = append(numbers, p.Number)
					}
				})
				if len(numbers) != 2 || numbers[0] != 1 || numbers[1] != 2 {
					t.Fatal("NULL planning substituted original parameters", numbers)
				}
			}
			// Definition/readiness probes call the NULL proof itself without
			// ordinary preflight, which separately requires initialized inputs.
			var arithmetic expr.Expr
			plan.WalkExpression(node, func(child expr.Expr) {
				if _, ok := child.(plan.ArithmeticExpr); ok && arithmetic == nil {
					arithmetic = child
				}
			})
			if arithmetic == nil {
				t.Fatal("bound arithmetic provider absent")
			}
			evaluation.Parameters = []sqltype.Value{sqltype.Null(b.Params.Parameters[1].Type), {}}
			if _, folded, err := exec.FoldStrictParameterNull(&evaluation, arithmetic); err != nil || !folded {
				t.Fatal("uninitialized parameter entered arithmetic planning", folded, err)
			}
			// Generic/imported planning must not use the execution frame to
			// prune demand or execute parameter arithmetic.
			evaluation.Parameters[1], err = sqltype.Integer(b.Params.Parameters[2].Type, 0)
			if err != nil {
				t.Fatal(err)
			}
			evaluation.PreserveParameterDemand = true
			if _, folded, err := exec.FoldStrictParameterNull(&evaluation, arithmetic); err != nil || folded {
				t.Fatal("generic planning used parameter demand", folded, err)
			}
		})
	}
}

func TestStrictParameterNullComparisonInputs(t *testing.T) {
	for _, planner := range []struct {
		name string
		run  func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error)
	}{{"preflight", exec.PreflightCoercionsWithPolicy}, {"conditional", exec.SimplifyConditional}} {
		t.Run(planner.name, func(t *testing.T) {
			e, op, scope := strictNullUnitFixture(t)
			for _, tc := range []struct {
				expression  string
				parameters  []int
				denominator int
			}{
				{"pg_backup_start(NULL::text,1/$1::integer=0)", []int{1}, 1},
				{"pg_backup_start(NULL::text,0=1/$1::integer)", []int{1}, 1},
				{"pg_backup_start($1::text,1/$2::integer=0)", []int{1, 2}, 2},
				{"pg_backup_start(fast=>1/$2::integer=0,label=>$1::text)", []int{2, 1}, 2},
				{"pg_backup_start(NULL::text,abs(1/$1::integer)=0)", []int{1}, 1},
				{"pg_backup_start(NULL::text,0=abs(1/$1::integer))", []int{1}, 1},
				{"pg_backup_start(NULL::text,abs(abs(1/$1::integer))=0)", []int{1}, 1},
				{"pg_backup_start($1::text,abs(1/$2::integer)=0)", []int{1, 2}, 2},
				{"pg_backup_start(fast=>abs(1/$2::integer)=0,label=>$1::text)", []int{2, 1}, 2},
			} {
				t.Run(tc.expression, func(t *testing.T) {
					node, b, evaluation := strictNullUnitBind(t, e, op, scope, tc.expression)
					unchanged := strictNullSourceUnchanged(t, node)
					// Reuse the actual immutable recipe across failing and valid
					// custom frames. The comparison must not hide its arithmetic
					// error merely because a different formal is known NULL.
					for _, denominator := range []int64{0, 2, 0} {
						frame := make([]sqltype.Value, tc.denominator)
						for i := range frame {
							frame[i] = sqltype.Null(b.Params.Parameters[i+1].Type)
						}
						value, err := sqltype.Integer(b.Params.Parameters[tc.denominator].Type, denominator)
						if err != nil {
							t.Fatal(err)
						}
						frame[tc.denominator-1] = value
						evaluation.Parameters = frame
						planned, changed, err := planner.run(&evaluation, node, true)
						if denominator == 0 {
							nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
						} else {
							if err != nil || !changed {
								t.Fatal("valid comparison did not preserve strict NULL folding", changed, err)
							}
							literal, ok := planned.(plan.LiteralExpr)
							if !ok || !literal.Value.IsNull() || literal.Value.Type().ID() != sqltype.PgLSN ||
								planned.ResultUsage() != node.ResultUsage() || planned.Properties() != node.Properties() {
								t.Fatal("lost folded scalar NULL type or conservative properties", planned)
							}
						}
						unchanged()
						var numbers []int
						plan.WalkExpression(node, func(child expr.Expr) {
							if parameter, ok := child.(plan.ParameterExpr); ok {
								numbers = append(numbers, parameter.Number)
							}
						})
						if len(numbers) != len(tc.parameters) {
							t.Fatal("NULL planning changed original parameter occurrences", numbers)
						}
						for i, number := range numbers {
							if number != tc.parameters[i] {
								t.Fatal("NULL planning changed original parameter order", numbers)
							}
						}
					}
				})
			}
			// The shared proof can still discard a sibling with no ready
			// arithmetic; missing labels, unlike missing denominators, cannot
			// prove the parent's NULL. Probe directly, without input preflight.
			for _, expression := range []string{
				"pg_backup_start($1::text,1/$2::integer=0)",
				"pg_backup_start($1::text,abs(1/$2::integer)=0)",
			} {
				node, b, evaluation := strictNullUnitBind(t, e, op, scope, expression)
				unchanged := strictNullSourceUnchanged(t, node)
				zero, err := sqltype.Integer(b.Params.Parameters[2].Type, 0)
				if err != nil {
					t.Fatal(err)
				}
				label := sqltype.Null(b.Params.Parameters[1].Type)
				for _, tc := range []struct {
					name       string
					frame      []sqltype.Value
					generic    bool
					wantFolded bool
				}{
					{"missing frame", nil, false, false},
					{"uninitialized frame", make([]sqltype.Value, 2), false, false},
					{"missing label", []sqltype.Value{{}, zero}, false, false},
					{"missing denominator", []sqltype.Value{label}, false, true},
					{"uninitialized denominator", []sqltype.Value{label, {}}, false, true},
					{"generic", []sqltype.Value{label, zero}, true, false},
				} {
					local := evaluation
					local.Parameters, local.PreserveParameterDemand = tc.frame, tc.generic
					if _, folded, err := exec.FoldStrictParameterNull(&local, node); err != nil || folded != tc.wantFolded {
						t.Fatal(tc.name, "incorrect arithmetic readiness/NULL proof", folded, err)
					}
					unchanged()
				}
			}
			// Row NULL is not a planning fact, and a RECORD call is never
			// erased by this scalar frontier even with a known NULL formal.
			for _, expression := range []string{"pg_backup_start(v::text,1/$1::integer=0)", "pg_backup_start(v::text,abs(1/$1::integer)=0)", "pg_backup_stop($1::boolean)"} {
				node, b, evaluation := strictNullUnitBind(t, e, op, scope, expression)
				evaluation.Parameters = []sqltype.Value{sqltype.Null(b.Params.Parameters[1].Type)}
				if _, folded, err := exec.FoldStrictParameterNull(&evaluation, node); err != nil || folded {
					t.Fatal("runtime or RECORD invocation was erased", expression, folded, err)
				}
			}
		})
	}
}

func TestStrictParameterNullBoundaries(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, b, evaluation := strictNullUnitBind(t, e, op, scope, "abs($1::integer)")
	for _, frame := range [][]sqltype.Value{nil, make([]sqltype.Value, 1)} {
		evaluation.Parameters = frame
		if _, folded, err := exec.FoldStrictParameterNull(&evaluation, node); err != nil || folded {
			t.Fatal("uninitialized parameter became a NULL constant", folded, err)
		}
	}
	// Runtime row NULL is not an execution-known parameter constant.
	row, _, rowEvaluation := strictNullUnitBind(t, e, op, scope, "abs(v)")
	rowEvaluation.Row = []sqltype.Value{sqltype.Null(b.Params.Parameters[1].Type)}
	rowEvaluation.Parameters = []sqltype.Value{sqltype.Null(b.Params.Parameters[1].Type)}
	if _, folded, err := exec.FoldStrictParameterNull(&rowEvaluation, row); err != nil || folded {
		t.Fatal("row demand was pruned", folded, err)
	}
	// A strict SRF initializes even with NULL parameters: it is not scalar.
	set, setBinder, setEvaluation := strictNullUnitBind(t, e, op, scope, "generate_series($1::integer,$2::integer)")
	setEvaluation.Parameters = []sqltype.Value{sqltype.Null(setBinder.Params.Parameters[1].Type), sqltype.Null(setBinder.Params.Parameters[2].Type)}
	if _, folded, err := exec.FoldStrictParameterNull(&setEvaluation, set); err != nil || folded {
		t.Fatal("SRF initialization was pruned", folded, err)
	}
	// A real domain conversion cannot be replaced by the physical NULL of its
	// input; its CHECK truth remains an actual later consumption boundary.
	domain, domainBinder, domainEvaluation := strictNullUnitBind(t, e, op, scope, "abs(($1::integer)::strict_null_checked)")
	domainEvaluation.Parameters = []sqltype.Value{sqltype.Null(domainBinder.Params.Parameters[1].Type)}
	if _, folded, err := exec.FoldStrictParameterNull(&domainEvaluation, domain); err != nil || folded {
		t.Fatal("domain barrier was discarded", folded, err)
	}
	_, err := domain.Eval(&domainEvaluation)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" || diagnostic.Constraint != "strict_null_present" {
		t.Fatal("domain CHECK truth was suppressed", err)
	}
}

func TestStrictParameterNullCompoundSemantics(t *testing.T) {
	for _, planner := range []struct {
		name string
		run  func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error)
	}{{"preflight", exec.PreflightCoercionsWithPolicy}, {"conditional", exec.SimplifyConditional}} {
		t.Run(planner.name, func(t *testing.T) {
			e, op, scope := strictNullUnitFixture(t)
			for _, tc := range []struct {
				expression string
				want       any
			}{
				{"1 IS DISTINCT FROM $1::integer", true},
				{"NULL::integer IS NOT DISTINCT FROM $1::integer", true},
				{"NULLIF(1,$1::integer)", int64(1)},
				{"GREATEST(1,$1::integer)", int64(1)},
				{"LEAST(1,$1::integer)", int64(1)},
				{"1 IN (1,$1::integer)", true},
				{"1 NOT IN (1,$1::integer)", false},
				{"$1::integer = ANY(ARRAY[]::integer[])", false},
				{"$1::integer = ALL(ARRAY[]::integer[])", true},
			} {
				t.Run(tc.expression, func(t *testing.T) {
					node, b, evaluation := strictNullUnitBind(t, e, op, scope, tc.expression)
					evaluation.Parameters = []sqltype.Value{sqltype.Null(b.Params.Parameters[1].Type)}
					planned, _, err := planner.run(&evaluation, node, true)
					if err != nil {
						t.Fatal(err)
					}
					value, err := planned.Eval(&evaluation)
					if got := enginetest.Export(value); err != nil || got != tc.want {
						t.Fatalf("compound NULL semantics got=%v, err=%v; want %v", got, err, tc.want)
					}
				})
			}
		})
	}
}

type strictNullXMLNotices struct{ events []sqlstate.Error }

func (n *strictNullXMLNotices) XMLInputDiagnostic(e sqlstate.Error) sqlstate.Error { return e }
func (n *strictNullXMLNotices) XMLInputWarning(e sqlstate.Error) {
	n.events = append(n.events, e)
}

func TestStrictParameterNullArithmeticDescendantBarriers(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, b, evaluation := strictNullUnitBind(t, e, op, scope, "pg_backup_start(NULL::text,abs(1/$1::integer)=0)")
	zero, err := sqltype.Integer(b.Params.Parameters[1].Type, 0)
	if err != nil {
		t.Fatal(err)
	}
	evaluation.Parameters = []sqltype.Value{zero}
	captures := exec.FoldCaptureCandidate{}
	evaluation.FoldCaptures = &captures
	unchanged := strictNullSourceUnchanged(t, node)
	// Copied real provider recipes carry conservative enclosing properties.
	// Descendants themselves remain available and would raise division by zero
	// if discovery skipped the whole-argument eligibility check.
	for _, tc := range []struct {
		name  string
		guard expr.Properties
	}{
		{"row", expr.Properties{RowDependent: true}},
		{"stage", expr.Properties{StageDependent: true}},
		{"domain", expr.Properties{DomainDependent: true}},
		{"runtime domain", expr.Properties{RuntimeDomainConversion: true}},
		{"volatile", expr.Properties{Volatility: expr.Volatile}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guarded := node.(functions.BackupControl)
			info := expr.InfoOf(guarded.Args[1])
			info.Property = expr.MergeProperties(info.Property, tc.guard)
			switch argument := guarded.Args[1].(type) {
			case plan.SelectedOperatorExpr:
				argument.Info = info
				guarded.Args[1] = argument
			case plan.ComparisonExpr:
				argument.Info = info
				guarded.Args[1] = argument
			default:
				t.Fatal("expected actual comparison provider", guarded.Args[1])
			}
			still := strictNullSourceUnchanged(t, guarded)
			if exec.OperatorExecutionConstant(guarded.Args[1], &evaluation) {
				t.Fatal("guard fixture remained execution-constant")
			}
			planned, folded, err := exec.FoldStrictParameterNull(&evaluation, guarded)
			if err != nil || !folded {
				t.Fatal("descendant bypassed enclosing guard", folded, err)
			}
			literal, ok := planned.(plan.LiteralExpr)
			if !ok || !literal.Value.IsNull() || literal.Value.Type().ID() != sqltype.PgLSN {
				t.Fatal("guarded scalar result lost typed NULL", planned)
			}
			still()
			unchanged()
			if len(captures.Entries) != 0 {
				t.Fatal("guarded demand published a constant", captures)
			}
		})
	}
	// No execution context can compute imported arithmetic. Use a literal
	// label: proving a parameter label would itself need typed frame access.
	definition := evaluation
	definition.Context = nil
	for _, context := range []*exec.Context{nil, &definition} {
		if _, folded, err := exec.FoldStrictParameterNull(context, node); err != nil || !folded {
			t.Fatal("definition NULL proof computed a descendant", folded, err)
		}
		unchanged()
	}

	// Conditional carriers own their demanded branches. The private frontier
	// must not turn lexical children into an eager catch-all traversal.
	for _, expression := range []string{
		"pg_backup_start(NULL::text,CASE WHEN FALSE THEN abs(1/$1::integer) ELSE 0 END=0)",
		"pg_backup_start(NULL::text,COALESCE(1,abs(1/$1::integer))=0)",
		"pg_backup_start(NULL::text,FALSE AND abs(1/$1::integer)=0)",
	} {
		root, binder, local := strictNullUnitBind(t, e, op, scope, expression)
		value, err := sqltype.Integer(binder.Params.Parameters[1].Type, 0)
		if err != nil {
			t.Fatal(err)
		}
		local.Parameters = []sqltype.Value{value}
		still := strictNullSourceUnchanged(t, root)
		if _, folded, err := exec.FoldStrictParameterNull(&local, root); err != nil || !folded {
			t.Fatal("conditional branch ownership crossed", expression, folded, err)
		}
		still()
	}

	// A non-ready enclosing arithmetic tree may still contain a ready demanded
	// descendant. Conversely, readiness discovery must not execute abs itself.
	for _, tc := range []struct {
		expression  string
		denominator int64
		division    bool
		overflow    bool
	}{
		{"pg_backup_start(NULL::text,(abs(1 / $1::integer)) + 1 = 0)", 0, true, false},
		{"pg_backup_start(NULL::text,abs('-2147483648'::integer + (0 / $1::integer)) = 0)", 2, false, true},
		{"pg_backup_start(NULL::text,abs($1::integer)=0)", -2147483648, false, true},
	} {
		root, binder, local := strictNullUnitBind(t, e, op, scope, tc.expression)
		value, err := sqltype.Integer(binder.Params.Parameters[1].Type, tc.denominator)
		if err != nil {
			t.Fatal(err)
		}
		local.Parameters, local.FoldCaptures = []sqltype.Value{value}, &captures
		still := strictNullSourceUnchanged(t, root)
		planned, folded, err := exec.FoldStrictParameterNull(&local, root)
		if tc.division {
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22012", Message: "division by zero"})
		} else {
			literal, ok := planned.(plan.LiteralExpr)
			if err != nil || !folded || !ok || !literal.Value.IsNull() ||
				literal.Value.Type().ID() != sqltype.PgLSN ||
				planned.ResultUsage() != root.ResultUsage() || planned.Properties() != root.Properties() {
				t.Fatal("discovery executed an enclosing immutable provider", planned, folded, err)
			}
		}
		still()
		if len(captures.Entries) != 0 {
			t.Fatal("parameter arithmetic published a constant", captures)
		}
		if tc.overflow {
			var wrapper expr.Expr
			plan.WalkExpression(root, func(child expr.Expr) {
				if _, ok := child.(functions.Math); ok && wrapper == nil {
					wrapper = child
				}
			})
			if wrapper == nil {
				t.Fatal("real immutable wrapper absent")
			}
			_, err := wrapper.Eval(&local)
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "22003", Message: "integer out of range"})
			still()
		}
	}

	// A real execution-demand provider in the same supplied argument fences
	// the whole descendant search, not just evaluation of that provider.
	for _, document := range []string{`<?xml version="1.1"?><a/>`, `<a>`} {
		expression := "pg_backup_start(NULL::text,abs(1/$1::integer)=length(XMLSERIALIZE(DOCUMENT XMLPARSE(DOCUMENT '" + document + "') AS text)))"
		root, binder, local := strictNullUnitBind(t, e, op, scope, expression)
		value, err := sqltype.Integer(binder.Params.Parameters[1].Type, 0)
		if err != nil {
			t.Fatal(err)
		}
		notices := &strictNullXMLNotices{}
		local.Parameters, local.FoldCaptures, local.Input.XMLInput = []sqltype.Value{value}, &captures, notices
		argument := root.(functions.BackupControl).Args[1]
		if !exec.OperatorExecutionConstant(argument, &local) {
			t.Fatal("XML fixture does not isolate execution-demand barrier")
		}
		var runtime expr.Expr
		plan.WalkExpression(argument, func(child expr.Expr) {
			if _, ok := child.(functions.XMLSerialize); ok {
				runtime = child
			}
		})
		if runtime == nil {
			t.Fatal("real XML execution provider absent")
		}
		if _, ok := runtime.(expr.ExecutionDemand); !ok {
			t.Fatal("XML fixture lost execution demand")
		}
		still := strictNullSourceUnchanged(t, root)
		for range 2 {
			planned, folded, err := exec.FoldStrictParameterNull(&local, root)
			literal, ok := planned.(plan.LiteralExpr)
			if err != nil || !folded || !ok || !literal.Value.IsNull() ||
				len(notices.events) != 0 || len(captures.Entries) != 0 {
				t.Fatal("NULL demand crossed transitive XML barrier", folded, err, notices.events, captures)
			}
			still()
		}
		// Prove these are live runtime diagnostics, not an inert test recipe.
		_, err = runtime.Eval(&local)
		if document == `<a>` {
			if err == nil {
				t.Fatal("malformed XML runtime diagnostic disappeared")
			}
		} else if err != nil || len(notices.events) != 2 {
			t.Fatal("XML runtime warnings disappeared", err, notices.events)
		}
		still()
	}
}
