package engine

import (
	"context"
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
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type sqlRoutineAnalysis struct {
	definition   *proc.Definition
	descriptions []StatementDescription
	statements   []parser.Statement // Immutable invocation syntax shares retained occurrence keys.
}

// InvokeSQLRoutine runs a SQL-language routine body inside the statement.
func (h *statementHost) InvokeSQLRoutine(d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage) (sqltype.Value, error) {
	return h.scope().invokeSQLRoutine(d, frame, resultUsage)
}

// invokeSQLRoutine borrows the admitted operation/transaction. It never acquires
// a driver connection, starts a root command, calls Finish or ends the operation.
func (parent *StatementScope) invokeSQLRoutine(d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage) (value sqltype.Value, err error) {
	if err = parent.enterSQLRoutine(); err != nil {
		return value, err
	}
	defer parent.leaveSQLRoutine()
	e := parent.engine()
	e.mu.Lock()
	auth, err := e.authorizationLocked(parent.op)
	if err == nil {
		_, err = exec.ValidateRoutineDescriptor(auth.Catalog, auth.Roles, d)
		if err == nil {
			err = auth.Check(access.Requirement{Object: e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address()), Privileges: authorization.ExecutePrivilege})
		}
	}
	changes := parent.catalog
	if changes == nil {
		changes = parent.owner.Catalog
	}
	read := e.privateCatalogLocked(changes)
	retained := payloadOf(parent.op.Session).sqlRoutines[d.Address()]
	e.mu.Unlock()
	if err != nil {
		return value, exec.SQLRoutineStatementContext(err, d.Name(), 1)
	}
	statements := d.SQL.Statements
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
	if retained.definition != d.SQL || len(retained.descriptions) != len(statements) {
		retained = sqlRoutineAnalysis{definition: d.SQL, statements: statements, descriptions: make([]StatementDescription, len(statements))}
	}
	value = sqltype.Null(resultUsage.Type)
	for i, statement := range statements {
		terminal := i == len(statements)-1 && !d.SQL.Void()
		value, err = parent.executeSQLRoutineCommand(d, program, frame, statement, i, terminal, read, &retained.descriptions[i], resultUsage)
		if err != nil {
			return value, exec.SQLRoutineStatementContext(err, d.Name(), i+1)
		}
	}
	e.mu.Lock()
	if payloadOf(parent.op.Session).sqlRoutines == nil {
		payloadOf(parent.op.Session).sqlRoutines = make(map[ids.ObjectAddress]sqlRoutineAnalysis)
	}
	payloadOf(parent.op.Session).sqlRoutines[d.Address()] = retained
	e.mu.Unlock()
	return value, nil
}

func (s *StatementScope) executeSQLRoutineCommand(d *catalog.Procedure, program *stored.RoutineProgram, frame *proc.Frame, statement parser.Statement, index int, terminal bool, read catalog.Read, retained *StatementDescription, resultUsage sqltype.Usage) (value sqltype.Value, err error) {
	restore := s.activateSQLRoutine(d.SQL.Inputs, read)
	defer func() { err = restore(err) }()
	s.sqlRoutine.definition, s.sqlRoutine.terminal = d.SQL, terminal
	_, selecting := statement.(*parser.Select)
	s.sqlRoutine.scalar, s.sqlRoutine.result = terminal && selecting, resultUsage
	e := s.engine()
	if _, transaction := statement.(*parser.TransactionControl); transaction {
		return value, sqlError("0A000", routineCommandTag(statement)+" is not allowed in an SQL function")
	}
	if _, savepoint := statement.(*parser.SavepointControl); savepoint {
		return value, sqlError("0A000", routineCommandTag(statement)+" is not allowed in an SQL function")
	}
	if program != nil {
		if err = s.admitSQLRoutineCommand(d, program, index); err != nil {
			return value, err
		}
	} else {
		if retained.analysis != nil {
			description := *retained
			s.Expected = &description
		}
		if err = s.selectPreparedAnalysis(statement); err != nil {
			return value, err
		}
		if err = s.acquireRelation(statement, true); err != nil {
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
	result, err := e.executeStatement(s, statement, len(frame.Parameters), func(_ context.Context, _ analyze.ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
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
			present, nextErr := result.Rows.Next(s.op.Context(), row)
			if nextErr != nil {
				return value, nextErr
			}
			if present {
				value, err = s.sqlRoutine.resultPlan.Apply(s.op.Context(), row[0], s.input, s.output)
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
	e := s.engine()
	e.mu.Lock()
	if index < 0 || index >= program.Len() || index >= len(d.SQL.Statements) || program.Action(index).Syntax() != d.SQL.Statements[index] {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: mismatched SQL-standard command capture")
	}
	auth, err := e.statementAuthorizationLocked(s.op, program.Action(index).Syntax())
	if err != nil {
		e.mu.Unlock()
		return err
	}
	if err = program.ValidateAction(auth.Catalog, index); err != nil {
		e.mu.Unlock()
		return err
	}
	action := program.Action(index)
	b := action.Binding()
	if b.Relation != nil {
		current, ok := auth.Catalog.Relation(b.Ref.Address)
		if !ok {
			e.mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		b, err = stored.RefreshRuleBinding(auth.Catalog, b, current)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		b.Storage = auth.StorageLocked(b.Ref.Address)
	}
	b, err = stored.RefreshRoutineCalls(auth.Catalog, b)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	b, err = view.RefreshRuleSources(auth, b)
	if err == nil {
		b, err = view.RefreshRuleActionCTEs(auth, b)
	}
	if err != nil {
		e.mu.Unlock()
		return err
	}
	commands := view.RuleProgram{Commands: []view.RuleCommand{{Syntax: action.Syntax(), Binding: b, Auth: auth}}}
	refs, err := e.admitRuleCommandsLocked(s, action.Syntax(), &commands)
	if err != nil {
		return err
	}
	if len(refs) != 0 {
		if _, err = s.acquireDataViewsLocked(e, refs); err != nil {
			e.mu.Unlock()
			return err
		}
	}
	b = commands.Commands[0].Binding
	physical := view.PhysicalBinding(b)
	s.installRuleScratch(ruleCommandScratch{mutation: statementMutation{statement: action.Syntax()}, ref: physical.Ref, descriptor: physical.Relation, storage: physical.Storage, viewWrite: view.ViewWriteOf(b.ViewWrite), sources: b.QuerySources})
	s.sqlRoutine.binding = &b
	e.mu.Unlock()
	return nil
}

// Query demand is selected before opening producers. Sort/aggregate semantics
// remain query-owned; only unnecessary later projected output is not requested.
func finalizeSQLRoutineQuery(ctx context.Context, root plan.Node, source *exec.Source, parameters []sqltype.Value) (rows exec.RowResult, count int, err error) {
	defer source.Close()
	defer func() {
		finishErr := exec.FinishForeignSources(ctx, source)
		if err == nil {
			err = finishErr
		}
	}()
	project, ok := root.(*plan.ProjectNode)
	if !ok || len(project.Schema()) != 1 {
		return nil, 0, fmt.Errorf("invalid SQL routine projection")
	}
	state := exec.State{Source: source, SourceMode: exec.SourceReusable, Parameters: parameters, Locking: source.Locks != nil, HashFilter: exec.HashQueryFilter(root)}
	defer state.ReleaseMemberships()
	if scope := scopeOf(source.LiveHost()); scope != nil {
		state.Input, state.Output = scope.input, scope.output
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
	row := make([]sqltype.Value, 1)
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
