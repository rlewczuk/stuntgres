package engine

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Statement admission serializes this physical-session owner. Definitions never
// retain adapter handles, snapshots or execution-private parameter frames.
type sqlPreparedRegistry struct {
	token   ids.SessionToken
	entries map[string]*sqlPreparedStatement
	portals map[string]*protocolPortal
}
type sqlPreparedStatement struct {
	body          parser.Statement
	source        string
	description   StatementDescription
	parameterOIDs []uint32 // Empty protocol statements preserve unvalidated hints.
}

func (r *sqlPreparedRegistry) EndSession(token ids.SessionToken, _ EndReason) error {
	if token != r.token {
		return fmt.Errorf("stuntgres: foreign SQL prepared attachment")
	}
	clear(r.entries)
	var failure error
	for name, portal := range r.portals {
		delete(r.portals, name)
		if err := portal.close(context.Background()); failure == nil {
			failure = err
		}
	}
	if failure != nil {
		return failure
	}
	return nil
}
func missingSQLPrepared(name string) error {
	if name == "" {
		return sqlError("26000", "unnamed prepared statement does not exist")
	}
	return sqlError("26000", fmt.Sprintf("prepared statement \"%s\" does not exist", name))
}

func (s *StatementScope) executePrepareSQL(raw *parser.PrepareSQL, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e := s.op.session.Engine()
	// Declarations enter the sole analyzer as a formal input seed. Typmods on
	// declarations are not input constraints; real destinations still own theirs.
	types := make([]sqltype.Usage, len(raw.Types))
	e.mu.Lock()
	for i, declaration := range raw.Types {
		usage, err := e.resolveAnnotationType(declaration, s.op, true)
		if err != nil {
			e.mu.Unlock()
			return Result{}, err
		}
		if usage.Type != nil && usage.Type.ID() != sqltype.Unknown {
			types[i] = usage.Type.Usage()
		}
	}
	e.mu.Unlock()
	s.initialParameters = types
	if err := s.selectPreparedAnalysis(raw.Body); err != nil {
		return Result{}, err
	}
	if err := s.acquireRelation(raw.Body, false); err != nil {
		return Result{}, err
	}
	description, err := e.describeOperation(s, raw.Body)
	if err != nil {
		return Result{}, err
	}
	r := s.op.session.preparedSQL
	if r.entries[raw.Name.Text] != nil {
		return Result{}, sqlError("42P05", fmt.Sprintf("prepared statement \"%s\" already exists", raw.Name.Text))
	}
	entry := &sqlPreparedStatement{body: raw.Body, source: raw.Source, description: description}
	if err := s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
	r.entries[raw.Name.Text] = entry
	// Like cursor mutations, SQL names are session effects, not rollback state.
	s.completed, s.sessionCompleted = true, true
	return Result{}, nil
}

func (s *StatementScope) executeDeallocateSQL(raw *parser.DeallocateSQL, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	r := s.op.session.preparedSQL
	if !raw.All && r.entries[raw.Name.Text] == nil {
		return Result{}, missingSQLPrepared(raw.Name.Text)
	}
	if err := s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
	if raw.All {
		clear(r.entries)
	} else {
		delete(r.entries, raw.Name.Text)
	}
	s.completed, s.sessionCompleted = true, true
	return Result{}, nil
}

// The existing executor imports directly into its one canonical frame, unlocked.
// This importer owns only the actual-expression binding/capture lock; evaluation
// runs unlocked within the same scope/clock and revalidates without replay.
func (s *StatementScope) sqlExecuteImporter(raw *parser.ExecuteSQL, utilityInput bool) FrameImporter {
	return func(ctx context.Context, formal ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
		if len(raw.Arguments) != formal.Count {
			d := sqlError("42601", fmt.Sprintf("wrong number of parameters for prepared statement \"%s\"", raw.Name.Text)).(*sqlstate.Error)
			d.Detail = fmt.Sprintf("Expected %d parameters but got %d.", formal.Count, len(raw.Arguments))
			return d
		}
		if utilityInput {
			if err := s.prepareProtocolUtilityInput(formal.Types); err != nil {
				return err
			}
			input = s.input
		}
		e := s.op.session.Engine()
		e.mu.Lock()
		locked := true
		defer func() {
			if locked {
				e.mu.Unlock()
			}
		}()
		input.LockedDomainInput = s
		auth, err := e.authorizationLocked(s.op)
		if err != nil {
			return err
		}
		binder := newExpressionBinder(ctx, e, input, s.op, nil)
		binder.query.auth, binder.params.noParameters, binder.params.executeParameters = &auth, true, true
		nodes := make([]expr.Expr, formal.Count)
		for i, argument := range raw.Arguments {
			node, err := binder.bind(argument, expressionPolicy{context: functionArgumentContext, expected: formal.Types[i], mode: sqltype.Assignment})
			if err != nil {
				if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" && node != nil {
					copy := *d
					copy.Message = fmt.Sprintf("parameter $%d of type %s cannot be coerced to the expected type %s", i+1, binder.diagnosticTypeName(node.ResultUsage().Type), binder.diagnosticTypeName(formal.Types[i].Type))
					copy.Hint = "You will need to rewrite or cast the expression."
					copy.Position = strconv.Itoa(parser.ExpressionStart(argument).Character)
					return &copy
				}
				return err
			}
			nodes[i] = node
		}
		if binder.consumer.catalogSnapshot || binder.prepared.regclassConcat {
			if err := e.captureCommentSnapshotLocked(s); err != nil {
				return err
			}
		}
		if binder.consumer.visibilitySnapshot {
			s.inquiry.visibilityDemand = true
			if err := e.captureVisibilitySnapshotLocked(s); err != nil {
				return err
			}
		}
		input = binder.input
		e.mu.Unlock()
		locked = false
		// Actuals are their own executable recipe, not the prepared body's
		// parameter-only binding. Borrow the original actual manifest without
		// replacing the statement's main-query authority or dependencies.
		var actualProgram routineAdmissionProgram
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(s), Catalog: auth.Catalog, Input: input, Output: s.output}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: binder.dependencies, RoutineProgram: &actualProgram}}
		for _, node := range nodes {
			if sqltype.ContainsDomainConstraints(node.ResultUsage().Type) {
				evaluation.Input.DomainChecks = &evaluation
				break
			}
		}
		for i, node := range nodes {
			prepared, _, err := preflightCoercionsWithPolicy(&evaluation, node, true)
			if err != nil {
				return err
			}
			if err := validateExpressionJoinExecution(&evaluation, prepared, auth.Catalog); err != nil {
				return err
			}
			nodes[i] = prepared
		}
		for i, node := range nodes {
			value, err := node.Eval(&evaluation)
			if err != nil {
				return err
			}
			frame[i] = value
		}
		if len(actualProgram.calls) != 0 {
			// Preserve the original consumed calls without retaining a graph
			// per nested utility hop. Merge only successive anonymous graphs,
			// preserving readmission order across other original-call owners.
			retained := false
			if n := len(s.routinePrograms); n != 0 {
				program := &s.routinePrograms[n-1]
				if program.outer == nil && program.consumer == nil && !program.ri {
					program.admission.include(&actualProgram)
					retained = true
				}
			}
			if !retained {
				s.routinePrograms = append(s.routinePrograms, commandRoutineProgram{admission: actualProgram})
			}
		}
		e.mu.Lock()
		locked = true
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := e.authorizationLocked(s.op)
		if err != nil {
			return err
		}
		if err := typedef.ValidateCapturedInputBinding(auth.Catalog, current.Catalog, binder.dependencies); err != nil {
			return err
		}
		e.mu.Unlock()
		locked = false
		return s.readmitPreparedPrograms(current)
	}
}
