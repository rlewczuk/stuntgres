package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSepgsqlSimulationInitializationException(t *testing.T) {
	for _, cancellation := range []bool{false, true} {
		name := "caught_sql_error"
		if cancellation {
			name = "canceled_failed_suffix"
		}
		t.Run(name, func(t *testing.T) {
			e := sepgsqlInitializationEngine(t)
			op, scope, project, evaluation := sepgsqlInitializationBinding(t, e, "SELECT abs(v),abs(v),abs(v),1/v FROM init_owner_rows")
			initialize := func(i int) {
				t.Helper()
				if err := exec.AdmitExpression(evaluation, project.Expressions[i], evaluation.Catalog); err != nil {
					t.Fatal(err)
				}
			}
			initialize(0)
			a := sepgsqlInitializationSite(t, evaluation, project.Expressions[0])
			before := scope.MarkSepgsqlObservations()
			checkpoint, err := scope.BeginPLException()
			if err != nil {
				t.Fatal(err)
			}
			disposed := false
			defer func() {
				if !disposed {
					scope.EndPLException(checkpoint, true, nil)
				}
			}()
			initialize(1)
			initialize(0)
			b := sepgsqlInitializationSite(t, evaluation, project.Expressions[1])
			after := scope.MarkSepgsqlObservations()
			if after.Observations != before.Observations || after.Initializations <= before.Initializations {
				t.Fatal("actual exception suffix did not share earlier observation", before, after)
			}
			value, err := sqltype.Integer(project.Expressions[0].ResultUsage().Type, 0)
			if err != nil {
				t.Fatal(err)
			}
			evaluation.Row = []sqltype.Value{value}
			var primary error
			savedContext, savedOperation := evaluation.Context, op.Ctx
			if cancellation {
				canceled, cancel := context.WithCancel(evaluation.Context)
				cancel()
				evaluation.Context, op.Ctx = canceled, canceled
				_, primary = project.Expressions[0].Eval(evaluation)
				if !errors.Is(primary, context.Canceled) {
					t.Fatal("actual canceled kernel", primary)
				}
			} else {
				initialize(3)
				_, primary = project.Expressions[3].Eval(evaluation)
				var diagnostic *sqlstate.Error
				if !errors.As(primary, &diagnostic) || diagnostic.Code != "22012" || diagnostic.Message != "division by zero" {
					t.Fatal("actual failed SQL body", primary)
				}
			}
			newest := scope.MarkSepgsqlObservations().Initializations
			err = scope.EndPLException(checkpoint, true, primary)
			disposed = true
			evaluation.Context, op.Ctx = savedContext, savedOperation
			if err != nil {
				t.Fatal("actual exception rollback cleanup", err)
			}
			host := core.HostOf(scope)
			if _, err = host.ValidateRoutineInitialization(evaluation.RoutineInitialization, a); err != nil {
				t.Fatal("caught rollback removed genuine earlier reused A", err)
			}
			if _, err = host.ValidateRoutineInitialization(evaluation.RoutineInitialization, b); err == nil {
				t.Fatal("caught rollback retained genuine failed-action B")
			}
			if got := scope.MarkSepgsqlObservations(); got.Observations != before.Observations || got.Initializations != newest {
				t.Fatal("rollback changed old observations or rewound sequence", before, got)
			}
			initialize(2)
			if scope.MarkSepgsqlObservations().Initializations <= newest {
				t.Fatal("post-catch C did not receive newer completion")
			}
			if _, err = host.ValidateRoutineInitialization(evaluation.RoutineInitialization, b); err == nil {
				t.Fatal("post-catch C resurrected B")
			}
			if op.Session.Sepgsql.Current() != "A" {
				t.Fatal("failed/canceled exception changed caller subject")
			}
		})
	}
}
