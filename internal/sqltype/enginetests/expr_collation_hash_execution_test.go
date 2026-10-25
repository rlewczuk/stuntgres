package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestExprCollationHashExecutionOwner(t *testing.T) {
	t.Run("reuse-fresh-owner-source-immutable", func(t *testing.T) {
		_, q, source := queryJoinsFixture(t, `SELECT k = ANY(SELECT q FROM b WHERE k=3) AS v FROM a`)
		project := q.Root.(*plan.ProjectNode)
		node := project.Expressions[0].(plan.SubqueryExpr)
		if len(node.Membership) != 1 {
			t.Fatal("fixture did not bind real hashed membership")
		}
		rhs := node.QueryPlan.Root.(*plan.ProjectNode)
		usage := rhs.Schema()[0].Usage
		calls := 0
		probe := queryProbe(usage, func(*exec.Context) (sqltype.Value, error) {
			calls++
			return sqltype.Integer(usage.Type, int64(calls))
		}).(queryProbeExpression)
		probe.Property.Volatility = expr.Volatile
		subqueryTestTarget(t, rhs, probe)
		leftUsage, rightUsage := node.Comparisons[0].Left.ResultUsage(), node.Comparisons[0].Right.ResultUsage()
		ref, conflict := node.Comparisons[0].Predicate.InputCollation()
		properties := node.Properties()
		for execution, want := range [][]any{{true, true, nil, false}, {false, false, nil, true}} {
			state := exec.State{Source: source}
			stage, err := exec.OpenQueryProjectStage(context.Background(), &state, project)
			if err != nil {
				t.Fatal(err)
			}
			row := make([]sqltype.Value, 1)
			for _, expected := range want {
				present, err := stage.Next(context.Background(), row)
				if err != nil || !present {
					t.Fatal("outer probe", present, err)
				}
				if expected == nil {
					if !row[0].IsNull() {
						t.Fatal("NULL probe lost UNKNOWN", row[0])
					}
				} else if truth, failure := row[0].Boolean(); failure != nil || truth != expected.(bool) {
					t.Fatal("execution-owned RHS changed across probes", row[0], failure)
				}
				if calls != execution+1 {
					t.Fatal("RHS rebuilt per outer probe or leaked across executions", calls, execution)
				}
			}
			if present, err := stage.Next(context.Background(), row); err != nil || present {
				t.Fatal("end of owner", present, err)
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if stage.Source != nil || stage.Evaluation.Query != nil || stage.Expressions != nil {
				t.Fatal("closed stage retained execution references")
			}
			captured := project.Expressions[0].(plan.SubqueryExpr)
			actualRef, actualConflict := captured.Comparisons[0].Predicate.InputCollation()
			if captured.QueryPlan.Root != rhs || captured.Properties() != properties || captured.Comparisons[0].Left.ResultUsage() != leftUsage || captured.Comparisons[0].Right.ResultUsage() != rightUsage || actualRef != ref || actualConflict != conflict {
				t.Fatal("execution mutated captured operand/provider authority")
			}
			if len(state.Memberships) != 1 {
				t.Fatal("borrowed stage close erased retained membership", len(state.Memberships))
			}
			retained := state.Memberships[exec.SubqueryMembershipKey{Plan: node.QueryPlan, Source: source}]
			if retained == nil || len(retained.Flat) != 1 {
				t.Fatal("owner lost the successfully built tuple")
			}
			state.ReleaseMemberships()
			if state.Memberships != nil || retained.Flat != nil || retained.Scratch != nil || retained.Buckets != nil {
				t.Fatal("true execution completion retained tuple/provider scratch")
			}
		}
	})
	t.Run("repeated-null-evidence", func(t *testing.T) {
		_, q, source := queryJoinsFixture(t, `SELECT k = ANY(SELECT q FROM b WHERE k=3) AS v FROM a`)
		project := q.Root.(*plan.ProjectNode)
		expr := project.Expressions[0].(plan.SubqueryExpr)
		rhs := expr.QueryPlan.Root.(*plan.ProjectNode)
		usage := rhs.Schema()[0].Usage
		calls := 0
		subqueryTestTarget(t, rhs, queryProbe(usage, func(*exec.Context) (sqltype.Value, error) {
			calls++
			return sqltype.Null(usage.Type), nil
		}))
		state := exec.State{Source: source}
		defer state.ReleaseMemberships()
		stage, err := exec.OpenQueryProjectStage(context.Background(), &state, project)
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		row := make([]sqltype.Value, 1)
		for range 4 {
			if present, err := stage.Next(context.Background(), row); err != nil || !present || !row[0].IsNull() || calls != 1 {
				t.Fatal("retained NULL evidence consumed or RHS rebuilt", present, row[0], calls, err)
			}
		}
	})
	for _, mode := range []string{"empty", "rhs-error", "cancel"} {
		t.Run(mode+"-before-left-release", func(t *testing.T) {
			sql := `SELECT k = ANY(SELECT q FROM b WHERE k=3) AS v FROM a`
			if mode == "empty" {
				sql = `SELECT k = ANY(SELECT q FROM b WHERE FALSE) AS v FROM a`
			}
			_, q, source := queryJoinsFixture(t, sql)
			project := q.Root.(*plan.ProjectNode)
			expr := project.Expressions[0].(plan.SubqueryExpr)
			rhs := expr.QueryPlan.Root.(*plan.ProjectNode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			primary := errors.New("demanded RHS target")
			leftCalls, rightCalls := 0, 0
			leftUsage := expr.Comparisons[0].Left.ResultUsage()
			expr.Comparisons[0].Left = queryProbe(leftUsage, func(*exec.Context) (sqltype.Value, error) {
				leftCalls++
				return sqltype.Integer(leftUsage.Type, 1)
			})
			project.Expressions[0] = expr
			usage := rhs.Schema()[0].Usage
			subqueryTestTarget(t, rhs, queryProbe(usage, func(*exec.Context) (sqltype.Value, error) {
				rightCalls++
				if mode == "rhs-error" {
					return sqltype.Value{}, primary
				}
				cancel()
				return sqltype.Integer(usage.Type, 1)
			}))
			state := exec.State{Source: source}
			stage, err := exec.OpenQueryProjectStage(ctx, &state, project)
			if err != nil {
				t.Fatal(err)
			}
			row := make([]sqltype.Value, 1)
			present, err := stage.Next(ctx, row)
			if mode == "empty" {
				truth, failure := row[0].Boolean()
				if err != nil || !present || failure != nil || truth || rightCalls != 0 {
					t.Fatal("empty hashed RHS", present, row[0], rightCalls, err)
				}
			} else if present || rightCalls != 1 || mode == "rhs-error" && err != primary || mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("RHS failure/cancellation precedence", present, rightCalls, err)
			}
			if leftCalls != 0 {
				t.Fatal("LHS evaluated before RHS empty/failure boundary", leftCalls)
			}
			if mode != "empty" && len(state.Memberships) != 0 {
				t.Fatal("failed/canceled partial RHS published membership")
			}
			if mode == "empty" && len(state.Memberships) != 1 {
				t.Fatal("successful empty RHS not retained")
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if stage.Source != nil || stage.Evaluation.Query != nil || stage.Expressions != nil {
				t.Fatal("released stage retained owner references after completion/error/cancel")
			}
			state.ReleaseMemberships()
			if state.Memberships != nil {
				t.Fatal("execution completion retained membership after empty/error/cancel")
			}
		})
	}
	t.Run("actual-frozen-view-source-occurrences", func(t *testing.T) {
		e := engine.New()
		owner := enginetest.StartSession(t, e, nil)
		for _, q := range []string{
			`CREATE TABLE expr_hash_sources(id INT)`,
			`INSERT INTO expr_hash_sources VALUES(1),(2)`,
			`CREATE SEQUENCE expr_hash_source_effect`,
			`CREATE VIEW expr_hash_source_v AS SELECT id,id::TEXT COLLATE "C" = ANY(SELECT nextval('expr_hash_source_effect')::TEXT COLLATE "C") AS hit FROM expr_hash_sources`,
		} {
			enginetest.SessionSQL(t, owner, q)
		}
		bound, scope := exprCollationStoredBinding(t, e, owner, `SELECT l.id+r.id AS total FROM expr_hash_source_v l JOIN expr_hash_source_v r ON l.id<>r.id WHERE l.hit AND r.hit`)
		e.Mu.Lock()
		source, err := e.CaptureQuerySourcesLocked(scope, bound.QuerySources, false)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		defer source.Close()
		if err := exec.SelectQuerySources(context.Background(), source); err != nil {
			t.Fatal(err)
		}
		if len(source.Sources) != 2 || source.Sources[0].NestedInputs == nil || source.Sources[1].NestedInputs == nil || source.Sources[0].NestedInputs == source.Sources[1].NestedInputs {
			t.Fatal("real view aliases did not capture distinct source occurrences")
		}
		state := exec.State{Source: source, Input: scope.Input, Output: scope.Output}
		defer state.ReleaseMemberships()
		stage, err := exec.OpenQueryProjectStage(context.Background(), &state, bound.Query.Root.(*plan.ProjectNode))
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		row := make([]sqltype.Value, 1)
		present, err := stage.Next(context.Background(), row)
		if err != nil || !present {
			t.Fatal("independent view alias providers", present, err)
		}
		total, err := row[0].Integer()
		if err != nil || total != 3 {
			t.Fatal("view aliases shared one hashed RHS", row[0], err)
		}
		if present, err := stage.Next(context.Background(), row); err != nil || present {
			t.Fatal("unexpected alias result cardinality", present, err)
		}
		if len(state.Memberships) != 2 {
			t.Fatal("actual source occurrences did not retain independent tables", len(state.Memberships))
		}
		for key := range state.Memberships {
			if key.Source != source.Sources[0].NestedInputs && key.Source != source.Sources[1].NestedInputs {
				t.Fatal("membership key does not belong to actual captured alias")
			}
		}
	})
}

func queryJoinsFixture(t *testing.T, q string) (*core.Engine, *plan.Query, *exec.Source) {
	t.Helper()
	e, query, source, _ := queryJoinsAdmittedFixture(t, q, false)
	return e, query, source
}

// Replace an actual scheduler-owned target rather than mocking a query result.
func subqueryTestTarget(t *testing.T, project *plan.ProjectNode, replacement expr.Expr) {
	t.Helper()
	for node := project.Child; node != nil; {
		switch n := node.(type) {
		case *plan.EvaluationNode:
			if len(n.Expressions) > 0 {
				n.Expressions[0] = replacement
				return
			}
		case *plan.SortNode:
			if len(n.Outputs) > 0 {
				n.Outputs[0] = replacement
				return
			}
		}
		left, right := plan.QueryInputs(node)
		if right != nil {
			t.Fatal("target scheduler crossed a join")
		}
		node = left
	}
	if len(project.Expressions) == 0 {
		t.Fatal("missing target")
	}
	project.Expressions[0] = replacement
}

// Explicit admission allows immutable captured rows to outlive catalog replacement
// without bypassing the replacement reservation held by implicit statements.
func queryJoinsAdmittedFixture(t *testing.T, q string, explicit bool) (*core.Engine, *plan.Query, *exec.Source, func()) {
	t.Helper()
	ast := enginetest.Statement(t, q)
	e := engine.New()
	for _, sql := range []string{"CREATE TABLE a(l TEXT,k INT,q INT)", "CREATE TABLE b(r TEXT,q INT,k INT)", "CREATE TABLE c(k INT,c TEXT)", "CREATE TABLE wide(k BIGINT)", "CREATE TABLE chars(k VARCHAR(3))", "CREATE TABLE longer(k VARCHAR(5))", "CREATE TABLE same(k VARCHAR(3))", "CREATE TABLE unused(v INT)", "INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)", "INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)", "INSERT INTO c VALUES(1,'c'),(1,'c')", "INSERT INTO wide VALUES(1)", "INSERT INTO chars VALUES('x')", "INSERT INTO longer VALUES('x')", "INSERT INTO same VALUES('x')"} {
		enginetest.Run(t, e, sql)
	}
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var scope *core.StatementScope
	var source *exec.Source
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		if source != nil {
			source.Close()
		}
		if scope != nil && !scope.Finished {
			_ = scope.Finish(context.Canceled)
		}
		done()
	}
	t.Cleanup(release)
	if explicit {
		if _, err = e.Begin(op, session.TxOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var b analyze.Binding
	if err == nil {
		b, err = core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	}
	if err == nil {
		// Standalone stages supply contexts, but still need live admission authority.
		source, err = e.CaptureQuerySourcesForInvokerLocked(scope, b.QuerySources, false, authorization.PrincipalRef{}, plan.RowMark{}, scope.QueryLockContext)
	}
	e.Mu.Unlock()
	if err == nil {
		err = exec.SelectQuerySources(op.Ctx, source)
	}
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	return e, b.Query, source, release
}
