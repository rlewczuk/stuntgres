package engine

import (
	"context"
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Statement admission serializes this physical-session owner. Definitions never
// retain adapter handles, snapshots or execution-private parameter frames.
type sqlPreparedRegistry struct {
	token SessionToken
	entries map[string]*sqlPreparedStatement
}
type sqlPreparedStatement struct {
	body parser.Statement
	source string
	description StatementDescription
}
func (r *sqlPreparedRegistry) EndSession(token SessionToken, _ EndReason) error {
	if token != r.token {
		return fmt.Errorf("stuntgres: foreign SQL prepared attachment")
	}
	clear(r.entries)
	return nil
}
func missingSQLPrepared(name string) error {
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

// The existing executor imports directly into its one canonical frame. Binding
// runs under its engine lock; volatile evaluation runs unlocked, within the same
// statement scope/clock. Nothing from the actuals is retained by the definition.
func (s *StatementScope) sqlExecuteImporter(raw *parser.ExecuteSQL) FrameImporter {
	return func(ctx context.Context, formal ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
		if len(raw.Arguments) != formal.Count {
			d := sqlError("42601", fmt.Sprintf("wrong number of parameters for prepared statement \"%s\"", raw.Name.Text)).(*sqlstate.Error)
			d.Detail = fmt.Sprintf("Expected %d parameters but got %d.", formal.Count, len(raw.Arguments))
			return d
		}
		e := s.op.session.Engine()
		auth, err := e.authorizationLocked(s.op)
		if err != nil { return err }
		binder := newExpressionBinder(ctx, e, input, s.op, nil)
		binder.queryAuth, binder.noParameters, binder.executeParameters = &auth, true, true
		nodes := make([]BoundExpr, formal.Count)
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
		if binder.catalogSnapshot || binder.regclassConcat {
			if err := e.captureCommentSnapshotLocked(s); err != nil {
				return err
			}
		}
		if binder.visibilitySnapshot {
			s.visibilityDemand = true
			if err := e.captureVisibilitySnapshotLocked(s); err != nil {
				return err
			}
		}
		input = binder.input
		e.mu.Unlock()
		defer e.mu.Lock()
		evaluation := EvalContext{Context: ctx, statementScope: s, Input: input, Output: s.output}
		for i, node := range nodes {
			value, err := node.Eval(&evaluation)
			if err != nil { return err }
			frame[i] = value
		}
		return nil
	}
}
