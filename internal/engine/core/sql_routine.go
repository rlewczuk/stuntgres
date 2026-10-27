package core

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type sqlRoutineAnalysis struct {
	definition     *proc.Definition
	name           string // Current qualifier; declaration/source payload stays immutable.
	descriptions   []Description
	statements     []parser.Statement   // Immutable invocation syntax shares retained occurrence keys.
	inputCollation sqltype.CollationRef // Quoted aggregate-support analysis context, not caller explicitness.
	plProcedure    *catalog.Procedure   // Session-private validator/first-use signature owner.
	plDiagnostic   string
	signature      proc.InvocationSignature
	plSpecialized  *catalog.Procedure
	activation     *proc.Definition
}

// InvokeSQLRoutine runs a SQL-language routine body inside the statement.
func (h *statementHost) InvokeSQLRoutine(d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage, inputCollation sqltype.CollationRef, assignmentPlanning bool) (sqltype.Value, error) {
	return h.scope().invokeSQLRoutine(d, frame, resultUsage, nil, inputCollation, assignmentPlanning, nil)
}

func (h *statementHost) InvokeSQLRoutineSet(d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage, collation sqltype.CollationRef, assignmentPlanning bool) ([]sqltype.Value, error) {
	var values []sqltype.Value
	_, err := h.scope().invokeSQLRoutine(d, frame, resultUsage, nil, collation, assignmentPlanning, &values)
	if err != nil {
		return nil, err
	}
	return values, nil
}

// The owner supplies only top-level support EXECUTE. The body still runs in
// the invoking scope, with its ordinary caller identity and relation rights.
func (h *statementHost) InvokeAggregateSQLRoutine(ctx context.Context, d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage, owner *catalog.Procedure, collation sqltype.CollationRef) (sqltype.Value, error) {
	s := h.scope()
	if ctx.Value(aggregateInvocationKey{}) != s {
		s.aggregateGateOnce.Do(func() { s.aggregateGate = make(chan struct{}, 1) })
		select {
		case s.aggregateGate <- struct{}{}:
		case <-ctx.Done():
			return sqltype.Value{}, ctx.Err()
		}
		defer func() { <-s.aggregateGate }()
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		saved := s.invocationContext
		s.invocationContext = context.WithValue(ctx, aggregateInvocationKey{}, s)
		defer func() { s.invocationContext = saved }()
	}
	return s.invokeSQLRoutine(d, frame, resultUsage, owner, collation, false, nil)
}

type aggregateInvocationKey struct{}

// invokeSQLRoutine borrows the admitted operation/transaction. It never acquires
// a driver connection, starts a root command, calls Finish or ends the operation.
func (parent *StatementScope) invokeSQLRoutine(d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage, supportOwner *catalog.Procedure, inputCollation sqltype.CollationRef, assignmentPlanning bool, values *[]sqltype.Value) (value sqltype.Value, err error) {
	if err = parent.enterSQLRoutine(); err != nil {
		return value, err
	}
	defer parent.leaveSQLRoutine()
	e := parent.Engine()
	e.Mu.Lock()
	auth, err := parent.RoutineAuthorizationLocked()
	if err == nil {
		var current *catalog.Procedure
		current, err = exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, d)
		if err == nil {
			d = current
			if supportOwner != nil {
				current, failure := exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, supportOwner)
				if failure != nil {
					e.Mu.Unlock()
					return value, failure
				}
				auth.State.CurrentUser = current.Owner
			}
			err = auth.Check(access.Requirement{Object: e.Catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address()), Privileges: authorization.ExecutePrivilege})
		}
	}
	changes := parent.Catalog
	if changes == nil {
		changes = parent.Owner.Catalog
	}
	read := e.PrivateCatalogLocked(changes)
	if err == nil && inputCollation.ID != 0 {
		err = stored.ValidateUsageCollation(read, sqltype.Usage{Collation: inputCollation})
	}
	retained := PayloadOf(parent.Op.Session).sqlRoutines[d.Address()]
	e.Mu.Unlock()
	if err != nil {
		return value, exec.SQLRoutineStatementContext(err, d.Name(), 1)
	}
	if d.SQL.Invocation != proc.OrdinaryInvocation {
		return value, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "trigger functions can only be called as triggers", Context: fmt.Sprintf("compilation of PL/pgSQL function %q near line 1", d.Name())}
	}
	if d.Strict {
		for _, argument := range frame.Parameters {
			if argument.IsNull() {
				if err = parent.executionContext().Err(); err != nil {
					return value, err
				}
				return sqltype.Null(resultUsage.Type), nil
			}
		}
	}
	previousAtomic := parent.plAtomic
	parent.plAtomic = previousAtomic || d.RoutineKind != catalog.ProcedureRoutine || d.SecurityDefiner || d.Configuration != nil || parent.plSubtransactionActive()
	defer func() { parent.plAtomic = previousAtomic }()
	leaveEnvironment, err := parent.enterRoutineEnvironment(d)
	if err != nil {
		return value, err
	}
	defer func() { err = leaveEnvironment(err) }()
	leavePolicy := parent.activateRoutineCommandPolicy(d)
	defer leavePolicy()
	if d.SQL.Kind == proc.PLpgSQLBody {
		return parent.invokePLRoutineSpecialized(d, frame, read, inputCollation, resultUsage, values)
	}
	// SQL-language body utilities run in an atomic query context, even when
	// the procedure's outer CALL was non-atomic. The invocation defer restores
	// the caller's policy on every exit; direct PL CALL/DO chains bypass this.
	parent.plAtomic = true
	statements := d.SQL.Statements
	if d.SQL.Standard {
		// SQL-standard bodies retain their creation-analyzed parameter semantics.
		inputCollation = sqltype.CollationRef{}
	}
	var program *stored.RoutineProgram
	if d.SQL.Standard {
		program, err = stored.RoutineProgramOf(d)
		if err != nil {
			return value, err
		}
	} else if retained.definition == d.SQL {
		statements = retained.statements
	} else {
		// Parse once for this invocation-analysis owner. Reusing immutable syntax
		// preserves the pointer-keyed source/target occurrence manifest; command
		// analysis still happens only as each command is reached.
		statements, err = parser.ParseWithOptions(d.SQL.Source, parser.Options{StandardConformingStrings: true, ClosedRoutineFormals: true, FormalCount: len(d.SQL.Inputs)})
		if err != nil {
			return value, exec.SQLRoutineStatementContext(err, d.Name(), 1)
		}
	}
	if len(statements) == 0 && !d.SQL.Void() {
		name, failure := CaptureTypeDiagnosticName(auth)
		if failure == nil {
			failure = SQLRoutineFinalError(resultUsage, name)
		}
		return value, exec.SQLRoutineStatementContext(failure, d.Name(), 1)
	}
	signature := proc.InvocationSignature{Inputs: frame.Inputs, Result: resultUsage}
	if retained.definition != d.SQL || retained.name != d.Name() || retained.inputCollation != inputCollation || !retained.signature.Equal(signature) || len(retained.descriptions) != len(statements) {
		retained = sqlRoutineAnalysis{definition: d.SQL, name: d.Name(), statements: statements, descriptions: make([]Description, len(statements)), inputCollation: inputCollation, signature: signature}
		retained.activation, err = d.SQL.Specialize(signature)
		if err != nil {
			return value, exec.SQLRoutineStatementContext(err, d.Name(), 1)
		}
	}
	value = sqltype.Null(resultUsage.Type)
	live := routineLive{parent: parent.routineLive, sqlName: d.Name()}
	parent.routineLive = &live
	defer func() { parent.routineLive = live.parent; live = routineLive{} }()
	for i, statement := range statements {
		live.statement = i + 1
		terminal := i == len(statements)-1 && !d.SQL.Void()
		value, err = parent.executeSQLRoutineCommand(d, retained.activation, program, frame, statement, i, terminal, read, &retained.descriptions[i], resultUsage, inputCollation, assignmentPlanning, values)
		if err != nil {
			// Preserve the reached support statement frame when the root's
			// genuine statement timer fires. Only CommandError classifies the
			// root cause; sibling/caller cancellation remains an ordinary error.
			if parent.invocationContext != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && errors.Is(context.Cause(parent.Op.Context()), session.StatementTimeoutCause) {
				err = parent.Op.CommandError(err)
			}
			return value, exec.SQLRoutineStatementContext(err, d.Name(), i+1)
		}
	}
	e.Mu.Lock()
	if PayloadOf(parent.Op.Session).sqlRoutines == nil {
		PayloadOf(parent.Op.Session).sqlRoutines = make(map[ids.ObjectAddress]sqlRoutineAnalysis)
	}
	PayloadOf(parent.Op.Session).sqlRoutines[d.Address()] = retained
	e.Mu.Unlock()
	return value, nil
}

func (s *StatementScope) executeSQLRoutineCommand(d *catalog.Procedure, activation *proc.Definition, program *stored.RoutineProgram, frame *proc.Frame, statement parser.Statement, index int, terminal bool, read catalog.Read, retained *Description, resultUsage sqltype.Usage, inputCollation sqltype.CollationRef, assignmentPlanning bool, values *[]sqltype.Value) (value sqltype.Value, err error) {
	if err := s.parallelExecutionEligibility(statement); err != nil {
		return value, err
	}
	noticeStart := len(s.Op.Notices)
	defer func() {
		if noticeStart != len(s.Op.Notices) {
			s.appendNoticeContext(noticeStart, exec.SQLRoutineStatementFrame(d.Name(), index+1))
		}
	}()
	restore := s.ActivateSQLRoutine(frame.Inputs, read)
	defer func() { err = restore(err) }()
	finishCommand, err := s.BeginRoutineCommand()
	if err != nil {
		return value, err
	}
	defer func() { err = finishCommand(err) }()
	s.sqlRoutine.definition, s.sqlRoutine.terminal = activation, terminal
	s.sqlRoutine.name = d.Name()
	s.sqlRoutine.assignmentPlanning = assignmentPlanning
	s.sqlRoutine.inputCollation = inputCollation
	_, selecting := statement.(*parser.Select)
	s.sqlRoutine.scalar, s.sqlRoutine.result = terminal && selecting && values == nil, resultUsage
	e := s.Engine()
	if err := s.routineUtilityEligibility(statement); err != nil {
		return value, err
	}
	if _, transaction := statement.(*parser.TransactionControl); transaction {
		return value, SQLError("0A000", RoutineCommandTag(statement)+" is not allowed in an SQL function")
	}
	if _, savepoint := statement.(*parser.SavepointControl); savepoint {
		return value, SQLError("0A000", RoutineCommandTag(statement)+" is not allowed in an SQL function")
	}
	if tag, eligible := session.EventCommand(statement); eligible {
		if err := s.readOnlyUtilityEligibility(statement); err != nil {
			return value, err
		}
		if err := s.executionEligibility(statement); err != nil {
			return value, err
		}
		previous, beginErr := s.BeginEventCommand(tag)
		if beginErr != nil {
			return value, beginErr
		}
		defer func() { err = s.FinishEventCommand(previous, err) }()
	}
	if program != nil {
		if err = s.admitSQLRoutineCommand(d, program, index); err != nil {
			return value, err
		}
	} else {
		if retained.Analysis != nil {
			description := *retained
			s.Expected = &description
		}
		if err = s.AcquireRelation(statement, true); err != nil {
			return value, err
		}
		if s.Expected == nil {
			description, describeErr := e.describeOperation(s, statement)
			if describeErr != nil {
				return value, describeErr
			}
			s.Expected = &description
		}
	}
	count := len(frame.Parameters)
	switch statement.(type) {
	case *parser.Set, *parser.Reset, *parser.Show:
		// Utility commands do not bind the enclosing routine's formal frame.
		count = 0
	}
	result, err := e.executeStatement(s, statement, count, func(_ context.Context, _ analyze.ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
		copy(dst, frame.Parameters)
		return nil
	})
	if err != nil {
		return value, err
	}
	value = sqltype.Null(resultUsage.Type)
	if result.Rows != nil {
		defer func() {
			closeErr := result.Rows.Close()
			if err == nil {
				err = closeErr
			}
		}()
		if terminal {
			row := make([]sqltype.Value, len(result.Rows.Schema()))
			for {
				present, nextErr := result.Rows.Next(s.executionContext(), row)
				if nextErr != nil {
					return value, nextErr
				}
				if !present {
					break
				}
				value, err = s.sqlRoutine.resultPlan.Apply(s.executionContext(), row, s.Input, s.Output)
				if err != nil {
					return value, err
				}
				if values == nil {
					break
				}
				*values = append(*values, value)
			}
		}
	}
	for _, rows := range result.RowSets {
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
	}
	if s.Expected != nil {
		*retained = *s.Expected
	}
	return value, err
}

// Frozen commands enter the incumbent checked-command admission path. No SQL
// rendering, parsing or name/overload selection occurs during invocation.
func (s *StatementScope) admitSQLRoutineCommand(d *catalog.Procedure, program *stored.RoutineProgram, index int) error {
	e := s.Engine()
	e.Mu.Lock()
	if index < 0 || index >= program.Len() || index >= len(d.SQL.Statements) || program.Action(index).Syntax() != d.SQL.Statements[index] {
		e.Mu.Unlock()
		return fmt.Errorf("stuntgres: mismatched SQL-standard command capture")
	}
	auth, err := s.statementAuthorizationLocked(program.Action(index).Syntax())
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	if err = program.ValidateAction(auth.Catalog, index); err != nil {
		e.Mu.Unlock()
		return err
	}
	action := program.Action(index)
	b := action.Binding()
	if b.Relation != nil {
		current, ok := auth.Catalog.Relation(b.Ref.Address)
		if !ok {
			e.Mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		b, err = stored.RefreshRuleBinding(auth.Catalog, b, current)
		if err != nil {
			e.Mu.Unlock()
			return err
		}
		b.Storage = auth.StorageLocked(b.Ref.Address)
	}
	err = stored.ValidateRoutineCallIdentities(auth.Catalog, b)
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	b, err = view.RefreshRuleSources(auth, b)
	if err == nil {
		b, err = view.RefreshRuleActionCTEs(auth, b)
	}
	if err != nil {
		e.Mu.Unlock()
		return err
	}
	if err := s.routineReadOnlyEligibility(action.Syntax(), auth.Catalog, b.QuerySources, b.Query); err != nil {
		e.Mu.Unlock()
		return err
	}
	commands := view.RuleProgram{Commands: []view.RuleCommand{{Syntax: action.Syntax(), Binding: b, Auth: auth}}}
	if err := stored.VisitRuleModifiers(b, func(definition *plan.CTEDefinition, mutation *analyze.Binding) error {
		commands.Commands = append(commands.Commands, view.RuleCommand{Syntax: definition.Statement, Binding: *mutation, Auth: auth})
		return nil
	}); err != nil {
		e.Mu.Unlock()
		return err
	}
	refs, err := e.AdmitRuleCommandsLocked(s, action.Syntax(), &commands)
	if err != nil {
		return err
	}
	if len(commands.Commands) > 1 {
		refs, err = s.admitSQLRoutineCTETargetsLocked(&commands, refs)
		if err != nil {
			return err
		}
	}
	if len(refs) != 0 {
		if _, err = s.AcquireDataViewsLocked(e, refs); err != nil {
			e.Mu.Unlock()
			return err
		}
	}
	b = commands.Commands[0].Binding
	if len(commands.Commands) == 1 {
		physical := view.PhysicalBinding(b)
		s.installRuleScratch(ruleCommandScratch{Mutation: StatementMutation{Statement: action.Syntax()}, Ref: physical.Ref, Descriptor: physical.Relation, Storage: physical.Storage, ViewWrite: view.ViewWriteOf(b.ViewWrite), Sources: b.QuerySources})
	}
	s.sqlRoutine.binding = &b
	e.Mu.Unlock()
	return nil
}

// Query demand is selected before opening producers. Sort/aggregate semantics
// remain query-owned; only unnecessary later projected output is not requested.
func finalizeSQLRoutineQuery(ctx context.Context, root plan.Node, source *exec.Source, parameters []sqltype.Value, scalar bool) (rows exec.RowResult, count int, err error) {
	defer source.Close()
	defer func() {
		finishErr := exec.FinishForeignSources(ctx, source)
		if err == nil {
			err = finishErr
		}
	}()
	project, ok := root.(*plan.ProjectNode)
	if !ok {
		return nil, 0, fmt.Errorf("invalid SQL routine projection")
	}
	if scalar && len(project.Schema()) != 1 {
		return nil, 0, fmt.Errorf("invalid scalar SQL routine projection")
	}
	exec.AdmitScan(root, source)
	state := exec.State{Source: source, SourceMode: exec.SourceReusable, Parameters: parameters, Locking: source.Locks != nil, HashFilter: exec.HashQueryFilter(root)}
	state.CapturePlannerAdmission()
	defer state.ReleaseMemberships()
	if scope := ScopeOf(source.LiveHost()); scope != nil {
		state.Input, state.Output = scope.Input, scope.Output
	}
	stage, err := exec.OpenQueryProjectStage(ctx, &state, project)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		closeErr := stage.Close()
		if err == nil {
			err = closeErr
		}
	}()
	row := make([]sqltype.Value, len(project.Schema()))
	present, err := stage.NextProjected(ctx, row)
	if err != nil {
		return nil, 0, err
	}
	if present {
		count = 1
	}
	result := exec.NewFinalRows(project.Schema(), count)
	result.Output = state.Output
	if present {
		result.Values = row
	}
	return result, count, nil
}

var _ expr.Composite = plan.SQLRoutineExpr{}
