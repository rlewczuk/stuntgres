package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type protocolDisposalProbe struct {
	exec.RowResult
	close func() error
}

func (p protocolDisposalProbe) Close() error { return p.close() }

func TestProtocolSimpleDisposalFence(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	sessionSQL(t, session, "BEGIN")
	op, _, err := session.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	scope.ProtocolSimpleQuery(1)
	if err := scope.gate(&parser.TransactionControl{Kind: parser.KindRollback}); err != nil {
		t.Fatal(err)
	}
	// This is the canonical non-retained control path: gate has not attached
	// an incarnation. Disposal must borrow, not replace, the existing owner.
	if scope.txRef != (ids.TxRef{}) {
		t.Fatal("control unexpectedly admitted a transaction")
	}
	owner := session.transactions
	ref, command := owner.ref, owner.command
	e.replacementMu.RLock()
	scope.replacementRead = true
	rows := exec.NewFinalRows(nil, 0)
	calls := 0
	session.preparedSQL.portals = map[string]*protocolPortal{"": {tx: ref, rows: protocolDisposalProbe{RowResult: rows, close: func() error {
		calls++
		if !owner.admission.TryLock() {
			t.Error("disposal held admission")
		} else {
			owner.admission.Unlock()
		}
		if !e.mu.TryLock() {
			t.Error("disposal held engine lock")
		} else {
			e.mu.Unlock()
		}
		if !e.replacementMu.TryLock() {
			t.Error("disposal held replacement fence")
		} else {
			e.replacementMu.Unlock()
		}
		return rows.Close()
	}}}}
	if err := scope.beginProtocolSimplePortal(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !rows.Closed || scope.txRef != ref || scope.command != command || owner.ref != ref || owner.command != command || !scope.replacementRead || owner.copyLease != nil {
		t.Fatal("disposal changed incarnation, lost fence or retained reservation", calls)
	}
	// The same attached owner must support subsequent real output reservation;
	// no stale reservation or replay may survive portal replacement.
	if err := scope.protocolSimpleIO(func() error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := scope.beginProtocolSimplePortal(); err != nil || calls != 2 {
		t.Fatal("replacement replayed disposal or prevented reservation reuse", calls, err)
	}
}

func TestProtocolSimplePlanningBounds(t *testing.T) {
	t.Run("closed_error", func(t *testing.T) {
		_, query, source := queryCoreFixture(t, "SELECT 1/(2-2) FROM t")
		defer source.Close()
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), PlanStaticOperators: true}}
		_, err := preflightQuery(&evaluation, query.Root)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
			t.Fatal("closed immutable error escaped planning", err)
		}
	})
	t.Run("planning_and_live_demand", func(t *testing.T) {
		_, query, source := queryCoreFixture(t, "SELECT id, 12/(2+1) FROM t ORDER BY id")
		original := query.Root.(*plan.ProjectNode)
		variable := original.Expressions[0]
		calls := 0
		probe := queryProbe(variable.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
			calls++
			return variable.Eval(c)
		}).(queryProbeExpression)
		probe.Property.Volatility = expr.Volatile
		original.Expressions[0] = probe
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), PlanStaticOperators: true}}
		prepared, err := preflightQuery(&evaluation, original)
		if err != nil {
			source.Close()
			t.Fatal(err)
		}
		root := prepared.(*plan.ProjectNode)
		if calls != 0 {
			source.Close()
			t.Fatal("planning demanded volatile target", calls)
		}
		state := exec.State{Source: source, HashFilter: exec.HashQueryFilter(root)}
		stage, err := exec.OpenQueryProjectStage(context.Background(), &state, root)
		if err != nil {
			source.Close()
			t.Fatal(err)
		}
		p := &protocolPortal{producer: &sqlCursor{root: root, source: source, state: state, stage: &stage}}
		defer p.close(context.Background())
		row := make([]sqltype.Value, 2)
		for i := range 4 {
			present, err := p.next(context.Background(), row)
			if err != nil || !present || calls != i+1 {
				t.Fatal("planned graph replayed or looked ahead", present, calls, err)
			}
			text, err := row[1].Type().Output(context.Background(), row[1])
			if err != nil || text != "4" {
				t.Fatal("expression result changed during live demand", text, err)
			}
		}
	})
	t.Run("parameter_barrier", func(t *testing.T) {
		original := preflightCast(t, "SELECT 1/$1::int4 FROM t").(plan.ArithmeticExpr)
		typ := original.Right.ResultUsage().Type
		value, err := typ.Input(context.Background(), "0", -1)
		if err != nil {
			t.Fatal(err)
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Parameters: []sqltype.Value{value}, PlanStaticOperators: true}}
		mapped, _, err := preflightCoercions(&evaluation, original)
		if err != nil || !mapped.Properties().ParameterDependent {
			t.Fatal("planning crossed imported parameter barrier", err)
		}
		_, err = mapped.Eval(&evaluation)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
			t.Fatal("runtime parameter demand changed", err)
		}
	})
	t.Run("exists_presence_demand", func(t *testing.T) {
		for _, tc := range []struct {
			name, sql, code string
		}{
			{"unused_target", "SELECT 1/(2-2) FROM t", ""},
			{"required_predicate", "SELECT 1 FROM t WHERE 1/(2-2)>0", "22012"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				e, query, source := queryCoreFixture(t, tc.sql)
				defer source.Close()
				plan, err := analyze.PresenceQueryPlan(query)
				if err != nil {
					t.Fatal(err)
				}
				exists := subqueryTestExpression(t, e, plan, parser.SubqueryExists)
				planning := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), PlanStaticOperators: true}}
				prepared, _, err := preflightCoercions(&planning, exists)
				if tc.code != "" {
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
						t.Fatal("EXISTS skipped required predicate preparation", err)
					}
					return
				}
				if err != nil {
					t.Fatal("EXISTS demanded its discarded target", err)
				}
				parent := exec.State{Source: source}
				evaluation := parent.Evaluation(context.Background())
				evaluation.Frame = 99
				value, err := prepared.Eval(&evaluation)
				present, booleanErr := value.Boolean()
				if err != nil || booleanErr != nil || !present {
					t.Fatal("prepared presence execution lost its input", present, err, booleanErr)
				}
			})
		}
	})
}
