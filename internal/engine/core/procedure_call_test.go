package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineProcedureCallOwnership(t *testing.T) {
	e := engine.New()
	session := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, session, "CREATE PROCEDURE procedure_inner() LANGUAGE SQL AS 'SELECT 1'")
	enginetest.SessionSQL(t, session, "CREATE PROCEDURE procedure_owned(IN x integer,OUT answer integer) LANGUAGE SQL AS 'CALL procedure_inner();SELECT 7/x'")
	ast, description := sequencePreparedDescription(t, e, session, "CALL procedure_owned(COALESCE(-(7::integer),0),1/0)")
	if description.Analysis == nil || description.Folds == nil || len(description.Folds.Current().Entries) != 0 || len(description.Results) != 1 || description.Results[0].Name != "answer" {
		t.Fatal("CALL Describe ran producers or lost tuple metadata")
	}
	op, err := session.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		op.Done()
		t.Fatal(err)
	}
	binding, err := analyze.Describe(op.Context(), e.AnalysisHost(op), ast, core.AuthorityOf(auth), ids.ObjectRef{}, sqltype.InputEnvironment{}, analyze.Expected{}, nil, nil, false)
	e.Mu.Unlock()
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	call, ok := binding.Expressions[0][0].(plan.SQLRoutineExpr)
	if !ok || len(call.Children()) != 1 || call.WrittenCount != 1 || len(call.ChildSlots) != 1 || call.ChildSlots[0] != 0 || len(call.InputMap) != 1 || call.InputMap[0] != (plan.RoutineInput{Start: 0, Count: 1}) || call.Usage.Type != call.Procedure.SQL.Result.Type || !call.Procedure.SQL.DerivedResult() {
		t.Fatal("OUT survived executable projection", call)
	}
	if binding.Parameters.Count != 0 || len(ast.(*parser.Call).Function.Args) != 2 {
		t.Fatal("binding changed source actuals")
	}
	operandOnly := false
	for _, dependency := range binding.Dependencies {
		operandOnly = operandOnly || dependency.Purpose == catalog.OperandDependency
	}
	if !operandOnly {
		t.Fatal("OUT analysis dependencies vanished or entered executable purpose")
	}
	_, parameters := sequencePreparedDescription(t, e, session, "CALL procedure_owned($1,$2)")
	if parameters.Parameters.Count != 2 || parameters.Parameters.Types[0].Type != call.Procedure.SQL.Inputs[0].Type || parameters.Parameters.Types[1].Type != call.Procedure.SQL.Formals[1].Usage.Type {
		t.Fatal("OUT protocol type inference lost with execution projection")
	}
	rebuilt, err := call.Rebuild(call.Info, call.Children())
	if err != nil {
		t.Fatal(err)
	}
	if !rebuilt.(plan.SQLRoutineExpr).SameArguments(call) || rebuilt.(plan.SQLRoutineExpr).Procedure != call.Procedure {
		t.Fatal("rebuild changed CALL ownership")
	}
	badInfo := call.Info
	foreign := *call.Usage.Type
	badInfo.Usage = foreign.Usage()
	forged, err := call.Rebuild(badInfo, call.Children())
	if err == nil && forged.(plan.SQLRoutineExpr).ValidateStoredProvider() == nil {
		t.Fatal("same-OID foreign result generation accepted")
	}
	// Real protocol definitions/bindings share only the immutable accepted fold
	// payload. CALL owns a portal close capability, not a query producer/frame.
	enginetest.SessionSQL(t, session, "BEGIN")
	cycle := session.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, session, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("procedure_definition", "CALL procedure_owned(COALESCE(-(7::integer),0),1/0)", nil)
		return err
	})
	registry := core.PayloadOf(session).PreparedSQL
	definition := registry.Lookup("procedure_definition")
	owner := definition.Description.Folds
	if owner == nil || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse folded CALL")
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "procedure_definition", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	bind("procedure_old")
	old := registry.Binding("procedure_old")
	accepted := owner.Current()
	if old == nil || old.Folds == nil || old.Entry.Description.Folds != owner || len(accepted.Entries) == 0 || old.Folds.Generation != accepted.Generation || &old.Folds.Entries[0] != &accepted.Entries[0] || old.Resource == nil || old.Resource.Root != nil || old.Resource.Closer == nil {
		t.Fatal("CALL binding did not accept actual definition ownership")
	}
	preparedFoldEntry(t, definition.Description, sqltype.Int4, "-7")
	for _, fold := range accepted.Entries {
		if fold.Source.Position == ast.(*parser.Call).Function.Args[1].SourcePosition() {
			t.Fatal("OUT entered fold demand")
		}
	}
	bind("procedure_again")
	again := registry.Binding("procedure_again")
	if again == old || again.Entry.Description.Folds != owner || again.Folds.Generation != accepted.Generation || &again.Folds.Entries[0] != &accepted.Entries[0] || &old.Folds.Entries[0] != &accepted.Entries[0] || again.Resource == nil || again.Resource == old.Resource || again.Resource.Root != nil || again.Resource.Closer == nil {
		t.Fatal("repeated Bind replaced immutable backing or binding identity")
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("procedure_definition") })
	if owner.Live() || old.Retired || again.Retired || &old.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("definition retirement mutated accepted CALL binding")
	}
	for _, name := range []string{"procedure_old", "procedure_again"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	if !old.Retired || !again.Retired {
		t.Fatal("CALL portal retirement leaked ownership")
	}
	op, _, err = session.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, session, "ROLLBACK")
	preparedFoldExecute(t, e, session, ast, &description)
	before := snapshotFoldDescription(description)
	for _, cause := range []string{"cancellation", "retirement", "generation"} {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		op, err := session.Acquire(ctx)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		t.Cleanup(op.Done)
		scope, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			cancel()
			t.Fatal(err)
		}
		t.Cleanup(func() { scope.Finish(context.Canceled) })
		local := cloneFoldDescription(t, description)
		scope.Expected = &local
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			e.Mu.Unlock()
			scope.Finish(err)
			op.Done()
			cancel()
			t.Fatal(err)
		}
		environment, err := e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			e.Mu.Unlock()
			scope.Finish(err)
			op.Done()
			cancel()
			t.Fatal(err)
		}
		publication, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
		if err != nil {
			e.Mu.Unlock()
			scope.Finish(err)
			op.Done()
			cancel()
			t.Fatal(err)
		}
		publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
		publication.Group.Private = true
		switch cause {
		case "cancellation":
			cancel()
		case "retirement":
			local.Retire()
		case "generation":
			local.Folds.Invalidate(true)
		}
		err = publication.Accept(op.Ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
		e.Mu.Unlock()
		scope.Finish(err)
		op.Done()
		cancel()
		if cause == "cancellation" && !errors.Is(err, context.Canceled) || cause != "cancellation" && !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("CALL publication fence", cause, err)
		}
		if &description.Folds.Current().Entries[0] != &before.Accepted.Entries[0] || &local.Folds.Current().Entries[0] != &before.Accepted.Entries[0] {
			t.Fatal("rejected CALL publication mutated accepted backing")
		}
	}
	// Invoke the actual admitted SQL body, including a nested no-output CALL.
	// Each exit must restore the caller's lexical/result and statement scratch.
	for _, cause := range []string{"success", "failure", "cancellation"} {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		op, err := session.Acquire(ctx)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		t.Cleanup(op.Done)
		scope, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			cancel()
			t.Fatal(err)
		}
		t.Cleanup(func() { scope.Finish(context.Canceled) })
		// Raw invocation borrows an admitted statement, just like Execute.
		// StartStatement alone owns admission but has not attached a transaction.
		if err := scope.Gate(ast); err != nil {
			t.Fatal(err)
		}
		previous := core.ScopeSQLRoutine(scope)
		depth := core.ScopeActiveSQLRoutineDepth(scope)
		expected := scope.Expected
		frame := proc.NewFrame(call.Procedure.SQL.Inputs)
		t.Cleanup(frame.Close)
		n := int64(1)
		if cause == "failure" {
			n = 0
		}
		v, err := sqltype.Integer(call.Procedure.SQL.Inputs[0].Type, n)
		if err != nil {
			t.Fatal(err)
		}
		if err = frame.Bind(0, v, call.Procedure.SQL.Inputs[0]); err != nil {
			t.Fatal(err)
		}
		backing := &frame.Parameters[0]
		if cause == "cancellation" {
			cancel()
		}
		value, err := core.ScopeInvokeSQLRoutine(scope, call.Procedure, frame, call.Usage, nil, sqltype.CollationRef{}, false)
		if cause == "success" && (err != nil || value.Type() != call.Usage.Type) || cause == "failure" && err == nil || cause == "cancellation" && !errors.Is(err, context.Canceled) {
			t.Fatal("procedure invocation route", cause, err)
		}
		if !core.SameSQLRoutineDemand(core.ScopeSQLRoutine(scope), previous) || core.ScopeActiveSQLRoutineDepth(scope) != depth || scope.Expected != expected || &frame.Parameters[0] != backing || !sqltype.SameDatum(frame.Parameters[0], v) {
			t.Fatal("CALL leaked scratch/depth or overwrote caller frame", cause)
		}
		frame.Close()
		scope.Finish(err)
		op.Done()
		cancel()
	}
}
