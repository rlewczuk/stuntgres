package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestExprCollationHashExecutionOwner(t *testing.T) {
	t.Run("reuse-fresh-owner-source-immutable", func(t *testing.T) {
		_, q, source := queryJoinsFixture(t, `SELECT k = ANY(SELECT q FROM b WHERE k=3) AS v FROM a`)
		project := q.Root.(*projectNode)
		expr := project.expressions[0].(subqueryExpression)
		if len(expr.membership) != 1 {
			t.Fatal("fixture did not bind real hashed membership")
		}
		rhs := expr.queryPlan.Root.(*projectNode)
		usage := rhs.Schema()[0].Usage
		calls := 0
		probe := queryProbe(usage, func(*EvalContext) (sqltype.Value, error) {
			calls++
			return sqltype.Integer(usage.Type, int64(calls))
		}).(queryProbeExpression)
		probe.Property.Volatility = Volatile
		subqueryTestTarget(t, rhs, probe)
		leftUsage, rightUsage := expr.comparisons[0].left.ResultUsage(), expr.comparisons[0].right.ResultUsage()
		ref, conflict := expr.comparisons[0].predicate.InputCollation()
		properties := expr.Properties()
		for execution, want := range [][]any{{true, true, nil, false}, {false, false, nil, true}} {
			state := queryState{source: source}
			stage, err := openQueryProjectStage(context.Background(), &state, project)
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
			if stage.source != nil || stage.evaluation.query != nil || stage.expressions != nil {
				t.Fatal("closed stage retained execution references")
			}
			captured := project.expressions[0].(subqueryExpression)
			actualRef, actualConflict := captured.comparisons[0].predicate.InputCollation()
			if captured.queryPlan.Root != rhs || captured.Properties() != properties || captured.comparisons[0].left.ResultUsage() != leftUsage || captured.comparisons[0].right.ResultUsage() != rightUsage || actualRef != ref || actualConflict != conflict {
				t.Fatal("execution mutated captured operand/provider authority")
			}
			if len(state.memberships) != 1 {
				t.Fatal("borrowed stage close erased retained membership", len(state.memberships))
			}
			retained := state.memberships[subqueryMembershipKey{expr.queryPlan, source}]
			if retained == nil || len(retained.flat) != 1 {
				t.Fatal("owner lost the successfully built tuple")
			}
			state.releaseMemberships()
			if state.memberships != nil || retained.flat != nil || retained.scratch != nil || retained.buckets != nil {
				t.Fatal("true execution completion retained tuple/provider scratch")
			}
		}
	})
	t.Run("repeated-null-evidence", func(t *testing.T) {
		_, q, source := queryJoinsFixture(t, `SELECT k = ANY(SELECT q FROM b WHERE k=3) AS v FROM a`)
		project := q.Root.(*projectNode)
		expr := project.expressions[0].(subqueryExpression)
		rhs := expr.queryPlan.Root.(*projectNode)
		usage := rhs.Schema()[0].Usage
		calls := 0
		subqueryTestTarget(t, rhs, queryProbe(usage, func(*EvalContext) (sqltype.Value, error) {
			calls++
			return sqltype.Null(usage.Type), nil
		}))
		state := queryState{source: source}
		defer state.releaseMemberships()
		stage, err := openQueryProjectStage(context.Background(), &state, project)
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
			project := q.Root.(*projectNode)
			expr := project.expressions[0].(subqueryExpression)
			rhs := expr.queryPlan.Root.(*projectNode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			primary := errors.New("demanded RHS target")
			leftCalls, rightCalls := 0, 0
			leftUsage := expr.comparisons[0].left.ResultUsage()
			expr.comparisons[0].left = queryProbe(leftUsage, func(*EvalContext) (sqltype.Value, error) {
				leftCalls++
				return sqltype.Integer(leftUsage.Type, 1)
			})
			project.expressions[0] = expr
			usage := rhs.Schema()[0].Usage
			subqueryTestTarget(t, rhs, queryProbe(usage, func(*EvalContext) (sqltype.Value, error) {
				rightCalls++
				if mode == "rhs-error" {
					return sqltype.Value{}, primary
				}
				cancel()
				return sqltype.Integer(usage.Type, 1)
			}))
			state := queryState{source: source}
			stage, err := openQueryProjectStage(ctx, &state, project)
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
			if mode != "empty" && len(state.memberships) != 0 {
				t.Fatal("failed/canceled partial RHS published membership")
			}
			if mode == "empty" && len(state.memberships) != 1 {
				t.Fatal("successful empty RHS not retained")
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if stage.source != nil || stage.evaluation.query != nil || stage.expressions != nil {
				t.Fatal("released stage retained owner references after completion/error/cancel")
			}
			state.releaseMemberships()
			if state.memberships != nil {
				t.Fatal("execution completion retained membership after empty/error/cancel")
			}
		})
	}
	t.Run("actual-frozen-view-source-occurrences", func(t *testing.T) {
		e := New()
		owner := sessionStart(t, e, nil)
		for _, q := range []string{
			`CREATE TABLE expr_hash_sources(id INT)`,
			`INSERT INTO expr_hash_sources VALUES(1),(2)`,
			`CREATE SEQUENCE expr_hash_source_effect`,
			`CREATE VIEW expr_hash_source_v AS SELECT id,id::TEXT COLLATE "C" = ANY(SELECT nextval('expr_hash_source_effect')::TEXT COLLATE "C") AS hit FROM expr_hash_sources`,
		} {
			sessionSQL(t, owner, q)
		}
		bound, scope := exprCollationStoredBinding(t, e, owner, `SELECT l.id+r.id AS total FROM expr_hash_source_v l JOIN expr_hash_source_v r ON l.id<>r.id WHERE l.hit AND r.hit`)
		e.mu.Lock()
		source, err := e.captureQuerySourcesLocked(scope, bound.querySources, false)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		defer source.close()
		if err := selectQuerySources(context.Background(), source); err != nil {
			t.Fatal(err)
		}
		if len(source.sources) != 2 || source.sources[0].nestedInputs == nil || source.sources[1].nestedInputs == nil || source.sources[0].nestedInputs == source.sources[1].nestedInputs {
			t.Fatal("real view aliases did not capture distinct source occurrences")
		}
		state := queryState{source: source, input: scope.input, output: scope.output}
		defer state.releaseMemberships()
		stage, err := openQueryProjectStage(context.Background(), &state, bound.query.Root.(*projectNode))
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
		if len(state.memberships) != 2 {
			t.Fatal("actual source occurrences did not retain independent tables", len(state.memberships))
		}
		for key := range state.memberships {
			if key.source != source.sources[0].nestedInputs && key.source != source.sources[1].nestedInputs {
				t.Fatal("membership key does not belong to actual captured alias")
			}
		}
	})
}
