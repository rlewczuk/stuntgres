package engine

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
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

func (r *sqlPreparedRegistry) EndSession(token ids.SessionToken, _ session.EndReason) error {
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
		return SQLError("26000", "unnamed prepared statement does not exist")
	}
	return SQLError("26000", fmt.Sprintf("prepared statement \"%s\" does not exist", name))
}

func (s *StatementScope) executePrepareSQL(raw *parser.PrepareSQL, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e := s.Engine()
	// Declarations enter the sole analyzer as a formal input seed. Typmods on
	// declarations are not input constraints; real destinations still own theirs.
	types := make([]sqltype.Usage, len(raw.Types))
	e.Mu.Lock()
	for i, declaration := range raw.Types {
		usage, err := e.resolveAnnotationType(declaration, s.Op, true)
		if err != nil {
			e.Mu.Unlock()
			return Result{}, err
		}
		if usage.Type != nil && usage.Type.ID() != sqltype.Unknown {
			types[i] = usage.Type.Usage()
		}
	}
	e.Mu.Unlock()
	s.initialParameters = types
	if err := s.selectPreparedAnalysis(raw.Body); err != nil {
		return Result{}, err
	}
	if err := s.AcquireRelation(raw.Body, false); err != nil {
		return Result{}, err
	}
	description, err := e.describeOperation(s, raw.Body)
	if err != nil {
		return Result{}, err
	}
	r := PayloadOf(s.Op.Session).preparedSQL
	if r.entries[raw.Name.Text] != nil {
		return Result{}, SQLError("42P05", fmt.Sprintf("prepared statement \"%s\" already exists", raw.Name.Text))
	}
	entry := &sqlPreparedStatement{body: raw.Body, source: raw.Source, description: description}
	if err := s.sessionPublicationPreflight(); err != nil {
		return Result{}, err
	}
	r.entries[raw.Name.Text] = entry
	// Like cursor mutations, SQL names are session effects, not rollback state.
	s.Completed, s.sessionCompleted = true, true
	return Result{}, nil
}

func (s *StatementScope) executeDeallocateSQL(raw *parser.DeallocateSQL, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	r := PayloadOf(s.Op.Session).preparedSQL
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
	s.Completed, s.sessionCompleted = true, true
	return Result{}, nil
}

// The existing executor imports directly into its one canonical frame, unlocked.
// This importer owns only the actual-expression binding/capture lock; evaluation
// runs unlocked within the same scope/clock and revalidates without replay.
func (s *StatementScope) sqlExecuteImporter(raw *parser.ExecuteSQL, utilityInput bool) FrameImporter {
	return func(ctx context.Context, formal analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
		if len(raw.Arguments) != formal.Count {
			d := SQLError("42601", fmt.Sprintf("wrong number of parameters for prepared statement \"%s\"", raw.Name.Text)).(*sqlstate.Error)
			d.Detail = fmt.Sprintf("Expected %d parameters but got %d.", formal.Count, len(raw.Arguments))
			return d
		}
		if utilityInput {
			if err := s.prepareProtocolUtilityInput(formal.Types); err != nil {
				return err
			}
			input = s.Input
		}
		e := s.Engine()
		e.Mu.Lock()
		locked := true
		defer func() {
			if locked {
				e.Mu.Unlock()
			}
		}()
		input.LockedDomainInput = s
		auth, err := e.AuthorizationLocked(s.Op)
		if err != nil {
			return err
		}
		binder := NewExpressionBinder(ctx, e, input, s.Op, nil)
		binder.Query.Auth, binder.Params.NoParameters, binder.Params.ExecuteParameters = &auth, true, true
		nodes := make([]expr.Expr, formal.Count)
		for i, argument := range raw.Arguments {
			node, err := binder.Bind(argument, analyze.ExpressionPolicy{Context: analyze.FunctionArgumentContext, Expected: formal.Types[i], Mode: sqltype.Assignment})
			if err != nil {
				if d, ok := err.(*sqlstate.Error); ok && d.Code == "42804" && node != nil {
					copy := *d
					copy.Message = fmt.Sprintf("parameter $%d of type %s cannot be coerced to the expected type %s", i+1, binder.DiagnosticTypeName(node.ResultUsage().Type), binder.DiagnosticTypeName(formal.Types[i].Type))
					copy.Hint = "You will need to rewrite or cast the expression."
					copy.Position = strconv.Itoa(parser.ExpressionStart(argument).Character)
					return &copy
				}
				return err
			}
			nodes[i] = node
		}
		if binder.Consumer.CatalogSnapshot || binder.Prepared.RegclassConcat {
			if err := e.CaptureCommentSnapshotLocked(s); err != nil {
				return err
			}
		}
		if binder.Consumer.VisibilitySnapshot {
			s.Inquiry.visibilityDemand = true
			if err := e.captureVisibilitySnapshotLocked(s); err != nil {
				return err
			}
		}
		input = binder.Input
		e.Mu.Unlock()
		locked = false
		// Actuals are their own executable recipe, not the prepared body's
		// parameter-only binding. Borrow the original actual manifest without
		// replacing the statement's main-query authority or dependencies.
		var actualProgram exec.RoutineProgram
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: HostOf(s), Catalog: auth.Catalog, Input: input, Output: s.Output}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: binder.Dependencies, RoutineProgram: &actualProgram}}
		for _, node := range nodes {
			if sqltype.ContainsDomainConstraints(node.ResultUsage().Type) {
				evaluation.Input.DomainChecks = &evaluation
				break
			}
		}
		for i, node := range nodes {
			prepared, _, err := exec.PreflightCoercionsWithPolicy(&evaluation, node, true)
			if err != nil {
				return err
			}
			if err := view.ValidateExpressionJoinExecution(&evaluation, prepared, auth.Catalog); err != nil {
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
		if len(actualProgram.Calls) != 0 {
			// Preserve the original consumed calls without retaining a graph
			// per nested utility hop. Merge only successive anonymous graphs,
			// preserving readmission order across other original-call owners.
			retained := false
			if n := len(s.routinePrograms); n != 0 {
				program := &s.routinePrograms[n-1]
				if program.outer == nil && program.consumer == nil && !program.ri {
					program.admission.Include(&actualProgram)
					retained = true
				}
			}
			if !retained {
				s.routinePrograms = append(s.routinePrograms, commandRoutineProgram{admission: &actualProgram})
			}
		}
		e.Mu.Lock()
		locked = true
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := e.AuthorizationLocked(s.Op)
		if err != nil {
			return err
		}
		if err := typedef.ValidateCapturedInputBinding(auth.Catalog, current.Catalog, binder.Dependencies); err != nil {
			return err
		}
		e.Mu.Unlock()
		locked = false
		return s.readmitPreparedPrograms(current)
	}
}
