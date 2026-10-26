package typecmds

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// DomainReplayEnv reconstructs captured cells for auth under the evaluation
// context c. Its scalar leaf binds the captured expression as a column default
// under Engine.mu, then evaluates it in a local copy of c with the leaf's own
// routine manifest, so nothing leaks into c.
func DomainReplayEnv(e *core.Engine, c *exec.Context, op *session.Operation, auth core.AuthorizationView) *typedef.DomainReplayEnv {
	scalar := func(raw parser.Expr, u sqltype.Usage) (sqltype.Value, error) {
		if err := view.MaterializedSnapshotScalar(raw); err != nil {
			return sqltype.Value{}, err
		}
		e.Mu.Lock()
		binder := core.NewExpressionBinder(c.Context, e, c.Input, op, nil)
		binder.Query.Auth = &auth
		binder.Query.Namespace = &analyze.QueryNamespace{}
		binder.Params.NoParameters = true
		node, err := binder.BindDefault(raw, catalog.Column{Name: "native captured cell", Usage: u})
		if err == nil {
			err = binder.CaptureRetainedWriteInput([]expr.Expr{node}, nil, nil)
		}
		e.Mu.Unlock()
		if err != nil {
			return sqltype.Value{}, err
		}
		local := *c
		local.Input = binder.Input
		local.Input.DomainChecks = &local
		local.RoutineRecipe = true
		local.RoutineChecks = binder.Dependencies
		local.RoutineReferences = nil
		local.RoutineConsumer = nil
		node, _, err = exec.PreflightCoercionsWithPolicy(&local, node, false)
		if err != nil {
			return sqltype.Value{}, err
		}
		return node.Eval(&local)
	}
	return &typedef.DomainReplayEnv{Context: c.Context, Catalog: auth.Catalog, Input: c.Input, Output: c.Output, Scalar: scalar, ValidateType: (stored.TypeProvider{}).Validate}
}

// CaptureDomainReplay checks that a private Restore INSERT into relation
// matches its native domain replay record and reconstructs the captured
// cells for the statement to consume.
func CaptureDomainReplay(e *core.Engine, scope *core.StatementScope, s *parser.Insert, record typedef.DomainReplayRecord, auth core.AuthorizationView, relation *catalog.Relation) (*core.DomainReplayCapture, error) {
	e.Mu.Lock()
	valid := core.PartitionConstraintReplayImport(e, scope.Op, auth)
	e.Mu.Unlock()
	if !valid {
		return nil, fmt.Errorf("stuntgres: invalid native domain replay authority")
	}
	if relation == nil || len(s.Rows) != 1 || len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != record.Schema || s.Name.Parts[1].Text != record.Table || record.Schema != auth.Catalog.Get(relation.Namespace()).Name() || record.Table != relation.Name() {
		return nil, fmt.Errorf("stuntgres: invalid native domain replay target")
	}
	actual, exists := auth.Catalog.Relation(relation.Address())
	if !exists || actual != relation {
		return nil, fmt.Errorf("stuntgres: noncatalog native replay destination")
	}
	if _, ok := e.ImportTargets[relation.Owner]; !ok {
		return nil, fmt.Errorf("stuntgres: uncaptured native domain replay owner")
	}
	slots := make([]int, len(s.Rows[0]))
	if len(s.Targets) == 0 {
		if len(slots) != len(relation.Columns) {
			return nil, fmt.Errorf("stuntgres: invalid native replay row width")
		}
		for i := range slots {
			slots[i] = i
		}
	} else {
		if len(s.Targets) != len(slots) {
			return nil, fmt.Errorf("stuntgres: invalid native replay target width")
		}
		if err := analyze.BindInsertTargets(relation, s.Targets, slots); err != nil {
			return nil, err
		}
	}
	c := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	c.Input.ExactSnapshot = true
	c.Input.DomainChecks = &c
	scope.RoutineAuth = auth
	cells, err := typedef.DomainReplayCells(DomainReplayEnv(e, &c, scope.Op, auth), s, record.Cells, relation, slots)
	if err != nil {
		return nil, err
	}
	return &core.DomainReplayCapture{Statement: s, Relation: relation, Cells: cells}, nil
}

// ExecuteDomainReplayInsert runs a private Restore INSERT paired with a native
// domain replay record, carrying the captured cells on the operation.
func ExecuteDomainReplayInsert(e *core.Engine, op *session.Operation, s *parser.Insert, record typedef.DomainReplayRecord) (result core.Result, err error) {
	scope, err := e.StartStatement(op)
	if err != nil {
		return core.Result{}, err
	}
	defer func() {
		err = scope.Finish(err)
		if err != nil {
			if result.Rows != nil {
				result.Rows.Close()
			}
			result = core.Result{}
		}
	}()
	if err = scope.Gate(s); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	auth, err := e.StatementAuthorizationLocked(op, s)
	var relation *catalog.Relation
	if err == nil {
		var ref ids.ObjectRef
		ref, err = e.ResolveRelationReference(auth, s.Name, false)
		if err == nil {
			relation, _ = auth.Catalog.Relation(ref.Address)
		}
	}
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	if relation == nil || relation.RelationKind != catalog.TableRelation && relation.RelationKind != catalog.PartitionedRelation {
		return core.Result{}, fmt.Errorf("stuntgres: invalid native replay relation kind")
	}
	capture, err := CaptureDomainReplay(e, scope, s, record, auth, relation)
	if err != nil {
		return core.Result{}, err
	}
	if core.OperationReplayOrZero(op).DomainReplay != nil {
		return core.Result{}, fmt.Errorf("stuntgres: nested native replay transport")
	}
	core.OperationReplay(op).DomainReplay = capture
	defer func() { core.OperationReplay(op).DomainReplay = nil }()
	return scope.Execute(s, 0, nil)
}
