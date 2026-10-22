package engine

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This transport is operation-owned, immutable after construction, and only the
// exact INSERT's destination binder consumes it. It is never a session setting,
// generic parameter importer, or expression-binder exception.
type domainReplayCapture struct {
	statement *parser.Insert
	relation  *catalog.Relation
	cells     map[int]plan.LiteralExpr // actual explicit VALUES slot, not raw SQL offsets
}

// Provenance is accumulated only after destination-owned reconstruction.
// Negative-circle provenance authorizes only an affected array carrier cell;
// it never grants a general scalar sidecar or negative text-input exception.
type domainReplayProvenance uint8

const (
	domainReplayDomainNull domainReplayProvenance = 1 << iota
	domainReplayNegativeCircle
)

func restoreDomainReplayNode(c *exec.Context, e *Engine, op *session.Operation, auth AuthorizationView, u sqltype.Usage, n *typedef.DomainReplayNode) (sqltype.Value, domainReplayProvenance, error) {
	bad := func() (sqltype.Value, domainReplayProvenance, error) {
		return sqltype.Value{}, 0, fmt.Errorf("stuntgres: invalid native domain replay value")
	}
	if n == nil || !u.Valid() {
		return bad()
	}
	if err := c.Context.Err(); err != nil {
		return sqltype.Value{}, 0, err
	}
	if n.Shape("domain_null") || n.Shape("domain") {
		identity, err := typedef.DomainReplayIdentity(auth.Catalog, u.Type)
		if err != nil {
			return bad()
		}
		if len(*n.Type) != 2 || (*n.Type)[0] != identity[0] || (*n.Type)[1] != identity[1] {
			return bad()
		}
		if n.Kind == "domain_null" {
			// CHECK recipes may be replayed after data (including NOT VALID).
			// The authentic declared domain identity, not today's partial
			// constraint set, owns this already-domain NULL provenance.
			return sqltype.Null(u.Type), domainReplayDomainNull, nil
		}
		base, count, err := restoreDomainReplayNode(c, e, op, auth, u.BaseUsage(), n.Base)
		if err != nil {
			return sqltype.Value{}, 0, err
		}
		if base.IsNull() {
			return bad()
		}
		plan, err := sqltype.PlanCoercion(u.BaseUsage(), u, sqltype.Assignment)
		if err != nil {
			return sqltype.Value{}, 0, err
		}
		value, err := plan.Apply(c.Context, base, c.Input, c.Output)
		return value, count, err
	}
	if n.Shape("null") {
		value := sqltype.Null(u.Type)
		if err := u.Type.CheckDomainInput(c.Context, value, c.Input); err != nil {
			return sqltype.Value{}, 0, err
		}
		return value, 0, nil
	}
	if u.Type.IsDomain() {
		return bad()
	}
	if n.Shape("array") {
		if !u.Type.IsArray() {
			return bad()
		}
		element := u
		element.Type = u.Type.Element()
		elements := make([]sqltype.Value, len(*n.Elements))
		count := domainReplayProvenance(0)
		for i := range elements {
			value, found, err := restoreDomainReplayNode(c, e, op, auth, element, &(*n.Elements)[i])
			if err != nil {
				return sqltype.Value{}, 0, err
			}
			elements[i] = value
			count |= found
		}
		dims := make([]sqltype.ArrayDimension, len(*n.Dimensions))
		for i, dimension := range *n.Dimensions {
			dims[i] = sqltype.ArrayDimension{Lower: dimension.Lower, Length: dimension.Length}
		}
		if len(dims) == 1 && dims[0] == (sqltype.ArrayDimension{Lower: 1, Length: 0}) && u.Type.ID() == sqltype.ACLItemArray && len(elements) == 0 {
			value, err := sqltype.EmptyACLArrayValue(u.Type)
			return value, count, err
		}
		value, err := sqltype.OwnedArrayValue(u.Type, dims, elements)
		return value, count, err
	}
	if n.Shape("composite") {
		if !u.Type.IsComposite() || len(*n.Members) != u.Type.CompositeLen() {
			return bad()
		}
		members := make([]sqltype.Value, len(*n.Members))
		count := domainReplayProvenance(0)
		for i := range members {
			member := u.Type.CompositeMember(i)
			captured := (*n.Members)[i]
			if captured.Name != member.Name {
				return bad()
			}
			value, found, err := restoreDomainReplayNode(c, e, op, auth, member.Usage, captured.Value)
			if err != nil {
				return sqltype.Value{}, 0, err
			}
			members[i] = value
			count |= found
		}
		value, err := sqltype.OwnedCompositeValue(u.Type, members)
		return value, count, err
	}
	if !n.Shape("scalar") || u.Type.IsArray() || u.Type.IsComposite() || *n.Scalar == "" {
		return bad()
	}
	statements, err := parser.ParseWithOptions("SELECT "+*n.Scalar, parser.Options{Context: c.Context, StandardConformingStrings: true})
	if err != nil {
		return sqltype.Value{}, 0, err
	}
	if len(statements) != 1 {
		return bad()
	}
	selectStmt, ok := statements[0].(*parser.Select)
	if !ok || len(selectStmt.Targets) != 1 || selectStmt.From != nil || selectStmt.Where != nil || selectStmt.Set != nil || len(selectStmt.With) != 0 || selectStmt.Into != nil || selectStmt.Order != nil || selectStmt.Bounds != nil || selectStmt.GroupBy != nil || selectStmt.Having != nil || len(selectStmt.Windows) != 0 || len(selectStmt.Locking) != 0 || selectStmt.Distinct != nil {
		return bad()
	}
	raw := selectStmt.Targets[0]
	if err = view.MaterializedSnapshotScalar(raw); err != nil {
		return sqltype.Value{}, 0, err
	}
	e.mu.Lock()
	binder := newExpressionBinder(c.Context, e, c.Input, op, nil)
	binder.Query.Auth = &auth
	binder.Query.Namespace = &analyze.QueryNamespace{}
	binder.Params.NoParameters = true
	node, err := binder.BindDefault(raw, catalog.Column{Name: "native captured cell", Usage: u})
	if err == nil {
		err = binder.CaptureRetainedWriteInput([]expr.Expr{node}, nil, nil)
	}
	e.mu.Unlock()
	if err != nil {
		return sqltype.Value{}, 0, err
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
		return sqltype.Value{}, 0, err
	}
	value, err := node.Eval(&local)
	if err != nil {
		return sqltype.Value{}, 0, err
	}
	if value.IsNull() || value.Type() != u.Type {
		return bad()
	}
	if value.RequiresCircleConstructorReplay() {
		return value, domainReplayNegativeCircle, nil
	}
	return value, 0, nil
}

func (e *Engine) captureDomainReplay(scope *StatementScope, s *parser.Insert, record typedef.DomainReplayRecord, auth AuthorizationView, relation *catalog.Relation) (*domainReplayCapture, error) {
	e.mu.Lock()
	valid := partitionConstraintReplayImport(e, scope.op, auth)
	e.mu.Unlock()
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
	if _, ok := e.importTargets[relation.Owner]; !ok {
		return nil, fmt.Errorf("stuntgres: uncaptured native domain replay owner")
	}
	capture := &domainReplayCapture{statement: s, relation: relation, cells: make(map[int]plan.LiteralExpr, len(record.Cells))}
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
		for _, target := range s.Targets {
			if len(target.Steps) != 0 {
				return nil, fmt.Errorf("stuntgres: native replay does not accept partial targets")
			}
		}
	}
	c := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.op.Context(), Host: hostOf(scope), Catalog: auth.Catalog, Input: scope.input, Output: scope.output}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth}}
	c.Input.ExactSnapshot = true
	c.Input.DomainChecks = &c
	scope.routineAuth = auth
	for _, cell := range record.Cells {
		slot, ok := relation.Index[cell.Column]
		if !ok {
			return nil, fmt.Errorf("stuntgres: unknown native replay column")
		}
		position := -1
		for i, mapped := range slots {
			if mapped == slot {
				if position != -1 {
					return nil, fmt.Errorf("stuntgres: duplicate native replay target")
				}
				position = i
			}
		}
		if position < 0 {
			return nil, fmt.Errorf("stuntgres: missing native replay column target")
		}
		if _, duplicate := capture.cells[position]; duplicate {
			return nil, fmt.Errorf("stuntgres: duplicate native replay cell")
		}
		if _, generated := relation.ColumnMeta.Generations[relation.Attributes[slot]]; generated {
			return nil, fmt.Errorf("stuntgres: generated native replay cell")
		}
		raw, ok := s.Rows[0][position].(parser.LiteralExpr)
		if !ok || raw.Value.Kind != parser.NullValue {
			return nil, fmt.Errorf("stuntgres: native replay requires an exact NULL placeholder")
		}
		usage := relation.Columns[slot].Usage
		value, count, err := restoreDomainReplayNode(&c, e, scope.op, auth, usage, cell.Value)
		if err != nil {
			return nil, err
		}
		admitted := count&domainReplayDomainNull != 0 || count&domainReplayNegativeCircle != 0 && value.RequiresCircleArrayReplay()
		if !admitted || value.Type() != usage.Type {
			return nil, fmt.Errorf("stuntgres: native replay cell has no admitted domain NULL or constructor array")
		}
		capture.cells[position] = plan.LiteralExpr{Info: expr.Info{Usage: usage, Pos: raw.SourcePosition()}, Value: value}
	}
	return capture, nil
}

func (e *Engine) executeDomainReplayInsert(op *session.Operation, s *parser.Insert, record typedef.DomainReplayRecord) (result Result, err error) {
	scope, err := e.StartStatement(op)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		err = scope.Finish(err)
		if err != nil {
			if result.Rows != nil {
				result.Rows.Close()
			}
			result = Result{}
		}
	}()
	if err = scope.gate(s); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	auth, err := e.statementAuthorizationLocked(op, s)
	var relation *catalog.Relation
	if err == nil {
		var ref ids.ObjectRef
		ref, err = e.resolveRelationReference(auth, s.Name, false)
		if err == nil {
			relation, _ = auth.Catalog.Relation(ref.Address)
		}
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if relation == nil || relation.RelationKind != catalog.TableRelation && relation.RelationKind != catalog.PartitionedRelation {
		return Result{}, fmt.Errorf("stuntgres: invalid native replay relation kind")
	}
	capture, err := e.captureDomainReplay(scope, s, record, auth, relation)
	if err != nil {
		return Result{}, err
	}
	if operationReplayOrZero(op).domainReplay != nil {
		return Result{}, fmt.Errorf("stuntgres: nested native replay transport")
	}
	operationReplay(op).domainReplay = capture
	defer func() { operationReplay(op).domainReplay = nil }()
	return scope.Execute(s, 0, nil)
}

func capturedDomainReplayCell(b *analyze.Binding, op *session.Operation, s *parser.Insert, row, column int) (expr.Expr, bool, error) {
	if op == nil || operationReplayOrZero(op).domainReplay == nil || operationReplayOrZero(op).domainReplay.statement != s {
		return nil, false, nil
	}
	capture := operationReplayOrZero(op).domainReplay
	literal, ok := capture.cells[column]
	if !ok {
		return nil, false, nil
	}
	if row != 0 || capture.relation != b.Relation || column >= len(b.Targets) || literal.ResultUsage() != b.Relation.Columns[b.Targets[column]].Usage {
		return nil, true, fmt.Errorf("stuntgres: native domain replay destination changed")
	}
	return literal, true, nil
}
