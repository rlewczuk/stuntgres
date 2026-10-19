package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const domainReplayMarker = "stuntgres-domain-null-v1"

type domainReplayRecord struct {
	Schema string             `json:"schema"`
	Table  string             `json:"table"`
	Cells  []domainReplayCell `json:"cells"`
}
type domainReplayCell struct {
	Column string            `json:"column"`
	Value  *domainReplayNode `json:"value"`
}

// Pointer fields distinguish absent fields from inappropriate empty fields.
// NULL is not part of this wire format: null nodes are explicit tagged objects.
type domainReplayNode struct {
	Kind       string                   `json:"kind"`
	Type       *[]string                `json:"type,omitempty"`
	Scalar     *string                  `json:"scalar,omitempty"`
	Base       *domainReplayNode        `json:"base,omitempty"`
	Members    *[]domainReplayMember    `json:"members,omitempty"`
	Dimensions *[]domainReplayDimension `json:"dimensions,omitempty"`
	Elements   *[]domainReplayNode      `json:"elements,omitempty"`
}
type domainReplayMember struct {
	Name  string            `json:"name"`
	Value *domainReplayNode `json:"value"`
}
type domainReplayDimension struct {
	Lower  int32 `json:"lower"`
	Length int32 `json:"length"`
}
type positionedDomainReplay struct {
	record domainReplayRecord
	end    int
}

// This transport is operation-owned, immutable after construction, and only the
// exact INSERT's destination binder consumes it. It is never a session setting,
// generic parameter importer, or expression-binder exception.
type domainReplayCapture struct {
	statement *parser.Insert
	relation  *catalog.Relation
	cells     map[int]literalExpression // actual explicit VALUES slot, not raw SQL offsets
}

func containsStoredDomainNull(v sqltype.Value) bool {
	d := v.Type()
	if d == nil || !sqltype.ContainsDomainConstraints(d) {
		return false
	}
	if v.IsNull() {
		return d.IsDomain() && d.HasDomainConstraints()
	}
	if d.IsDomain() {
		base, err := v.BaseValue()
		return err == nil && containsStoredDomainNull(base)
	}
	if d.IsArray() {
		for i := range v.ArrayLen() {
			if containsStoredDomainNull(v.ArrayElement(i)) {
				return true
			}
		}
	}
	if d.IsComposite() {
		for i := range v.CompositeLen() {
			if containsStoredDomainNull(v.CompositeField(i)) {
				return true
			}
		}
	}
	return false
}

func domainReplayIdentity(view catalog.Read, d *sqltype.Descriptor) ([]string, error) {
	descriptor, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.ID())})
	if !ok || descriptor.SQLType != d || !d.IsDomain() {
		return nil, fmt.Errorf("stuntgres: invalid native domain identity")
	}
	namespace, ok := view.Descriptor(descriptor.Namespace())
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing native domain namespace")
	}
	return []string{namespace.Name(), descriptor.Name()}, nil
}

func captureDomainReplayNode(ctx context.Context, view catalog.Read, u sqltype.Usage, v sqltype.Value) (domainReplayNode, error) {
	if !u.Valid() || v.Type() != u.Type {
		return domainReplayNode{}, fmt.Errorf("stuntgres: invalid native captured cell type")
	}
	n := domainReplayNode{Kind: "null"}
	if v.IsNull() {
		if u.Type.IsDomain() && u.Type.HasDomainConstraints() {
			identity, err := domainReplayIdentity(view, u.Type)
			if err != nil {
				return n, err
			}
			n.Kind = "domain_null"
			n.Type = &identity
		}
		return n, nil
	}
	if u.Type.IsDomain() {
		identity, err := domainReplayIdentity(view, u.Type)
		if err != nil {
			return n, err
		}
		base, err := v.BaseValue()
		if err != nil {
			return n, err
		}
		child, err := captureDomainReplayNode(ctx, view, u.BaseUsage(), base)
		if err != nil {
			return n, err
		}
		return domainReplayNode{Kind: "domain", Type: &identity, Base: &child}, nil
	}
	if u.Type.IsArray() {
		dimensions := make([]domainReplayDimension, v.ArrayRank())
		for i := range dimensions {
			dimension := v.ArrayDimension(i)
			dimensions[i] = domainReplayDimension{Lower: dimension.Lower, Length: dimension.Length}
		}
		elements := make([]domainReplayNode, v.ArrayLen())
		element := u
		element.Type = u.Type.Element()
		for i := range elements {
			var err error
			elements[i], err = captureDomainReplayNode(ctx, view, element, v.ArrayElement(i))
			if err != nil {
				return n, err
			}
		}
		return domainReplayNode{Kind: "array", Dimensions: &dimensions, Elements: &elements}, nil
	}
	if u.Type.IsComposite() {
		members := make([]domainReplayMember, v.CompositeLen())
		for i := range members {
			field := u.Type.CompositeMember(i)
			child, err := captureDomainReplayNode(ctx, view, field.Usage, v.CompositeField(i))
			if err != nil {
				return n, err
			}
			members[i] = domainReplayMember{Name: field.Name, Value: &child}
		}
		return domainReplayNode{Kind: "composite", Members: &members}, nil
	}
	literal, err := catalogLiteralSQL(ctx, view, literalExpression{Info: expr.Info{Usage: u}, value: v})
	if err != nil {
		return n, err
	}
	return domainReplayNode{Kind: "scalar", Scalar: &literal}, nil
}

func encodeDomainReplay(record domainReplayRecord) (string, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return "-- " + domainReplayMarker + " " + base64.StdEncoding.EncodeToString(raw) + "\n", nil
}

// The token pass rejects duplicate keys at EVERY depth, JSON nulls and trailing
// values; the typed pass rejects unknown fields and nonintegral/bounded dimensions.
func validateDomainReplayJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return fmt.Errorf("native domain replay does not accept JSON null")
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || name != strings.ToLower(name) || seen[name] {
				return fmt.Errorf("duplicate or noncanonical native domain replay field")
			}
			seen[name] = true
			if err := validateDomainReplayJSON(decoder); err != nil {
				return err
			}
		}
		if seen["lower"] != seen["length"] {
			return fmt.Errorf("incomplete native domain replay dimension")
		}
	case '[':
		for decoder.More() {
			if err := validateDomainReplayJSON(decoder); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid native domain replay JSON")
	}
	_, err = decoder.Token()
	return err
}

func decodeDomainReplay(comment string) (domainReplayRecord, bool, error) {
	text := strings.TrimSpace(comment)
	if !strings.HasPrefix(text, "stuntgres-domain-null-") {
		return domainReplayRecord{}, false, nil
	}
	parts := strings.Fields(text)
	fail := func(err error) (domainReplayRecord, bool, error) {
		return domainReplayRecord{}, true, fmt.Errorf("stuntgres: invalid native domain replay: %w", err)
	}
	if len(parts) != 2 || parts[0] != domainReplayMarker {
		return fail(fmt.Errorf("unsupported marker or payload"))
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return fail(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err = validateDomainReplayJSON(decoder); err != nil {
		return fail(err)
	}
	if _, err = decoder.Token(); err != io.EOF {
		return fail(fmt.Errorf("trailing JSON data"))
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var record domainReplayRecord
	if err = decoder.Decode(&record); err != nil {
		return fail(err)
	}
	if record.Schema == "" || record.Table == "" || len(record.Cells) == 0 {
		return fail(fmt.Errorf("missing target or cells"))
	}
	return record, true, nil
}

func pairDomainReplay(script string, statements []parser.Statement, records []positionedDomainReplay) (map[*parser.Insert]domainReplayRecord, error) {
	if len(records) == 0 {
		return nil, nil
	}
	pairs := make(map[*parser.Insert]domainReplayRecord, len(records))
	statementIndex := 0
	for _, entry := range records {
		for statementIndex < len(statements) {
			insert, ok := statements[statementIndex].(*parser.Insert)
			if ok && insert.Pos.Offset >= entry.end {
				break
			}
			statementIndex++
		}
		if statementIndex == len(statements) {
			return nil, fmt.Errorf("stuntgres: unpaired native domain replay")
		}
		statement := statements[statementIndex]
		insert, ok := statement.(*parser.Insert)
		if !ok || entry.end < 0 || entry.end > len(script) || insert.Pos.Offset < entry.end || strings.TrimSpace(script[entry.end:insert.Pos.Offset]) != "" {
			return nil, fmt.Errorf("stuntgres: native domain replay must immediately precede its INSERT")
		}
		if _, duplicate := pairs[insert]; duplicate {
			return nil, fmt.Errorf("stuntgres: duplicate native domain replay")
		}
		if len(insert.Name.Parts) != 2 || insert.Name.Parts[0].Text != entry.record.Schema || insert.Name.Parts[1].Text != entry.record.Table || insert.Alias.Text != "" || len(insert.Rows) != 1 || insert.DefaultValues || insert.Source != nil || len(insert.With) != 0 || insert.Returning != nil || insert.Conflict != nil {
			return nil, fmt.Errorf("stuntgres: invalid native domain replay INSERT target")
		}
		pairs[insert] = entry.record
	}
	return pairs, nil
}

func (n *domainReplayNode) shape(kind string) bool {
	if n == nil || n.Kind != kind {
		return false
	}
	switch kind {
	case "null":
		return n.Type == nil && n.Scalar == nil && n.Base == nil && n.Members == nil && n.Dimensions == nil && n.Elements == nil
	case "domain_null":
		return n.Type != nil && n.Scalar == nil && n.Base == nil && n.Members == nil && n.Dimensions == nil && n.Elements == nil
	case "domain":
		return n.Type != nil && n.Scalar == nil && n.Base != nil && n.Members == nil && n.Dimensions == nil && n.Elements == nil
	case "scalar":
		return n.Type == nil && n.Scalar != nil && n.Base == nil && n.Members == nil && n.Dimensions == nil && n.Elements == nil
	case "array":
		return n.Type == nil && n.Scalar == nil && n.Base == nil && n.Members == nil && n.Dimensions != nil && n.Elements != nil
	case "composite":
		return n.Type == nil && n.Scalar == nil && n.Base == nil && n.Members != nil && n.Dimensions == nil && n.Elements == nil
	}
	return false
}

func restoreDomainReplayNode(c *EvalContext, e *Engine, op *Operation, auth AuthorizationView, u sqltype.Usage, n *domainReplayNode) (sqltype.Value, int, error) {
	bad := func() (sqltype.Value, int, error) {
		return sqltype.Value{}, 0, fmt.Errorf("stuntgres: invalid native domain replay value")
	}
	if n == nil || !u.Valid() {
		return bad()
	}
	if err := c.Context.Err(); err != nil {
		return sqltype.Value{}, 0, err
	}
	if n.shape("domain_null") || n.shape("domain") {
		identity, err := domainReplayIdentity(auth.Catalog, u.Type)
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
			return sqltype.Null(u.Type), 1, nil
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
	if n.shape("null") {
		value := sqltype.Null(u.Type)
		if err := u.Type.CheckDomainInput(c.Context, value, c.Input); err != nil {
			return sqltype.Value{}, 0, err
		}
		return value, 0, nil
	}
	if u.Type.IsDomain() {
		return bad()
	}
	if n.shape("array") {
		if !u.Type.IsArray() {
			return bad()
		}
		element := u
		element.Type = u.Type.Element()
		elements := make([]sqltype.Value, len(*n.Elements))
		count := 0
		for i := range elements {
			value, found, err := restoreDomainReplayNode(c, e, op, auth, element, &(*n.Elements)[i])
			if err != nil {
				return sqltype.Value{}, 0, err
			}
			elements[i] = value
			count += found
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
	if n.shape("composite") {
		if !u.Type.IsComposite() || len(*n.Members) != u.Type.CompositeLen() {
			return bad()
		}
		members := make([]sqltype.Value, len(*n.Members))
		count := 0
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
			count += found
		}
		value, err := sqltype.OwnedCompositeValue(u.Type, members)
		return value, count, err
	}
	if !n.shape("scalar") || u.Type.IsArray() || u.Type.IsComposite() || *n.Scalar == "" {
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
	if err = materializedSnapshotScalar(raw); err != nil {
		return sqltype.Value{}, 0, err
	}
	e.mu.Lock()
	binder := newExpressionBinder(c.Context, e, c.Input, op, nil)
	binder.queryAuth = &auth
	binder.queryNamespace = &queryNamespace{}
	binder.noParameters = true
	node, err := binder.bindDefault(raw, catalog.Column{Name: "native captured cell", Usage: u})
	if err == nil {
		err = binder.captureRetainedWriteInput([]expr.Expr{node}, nil, nil)
	}
	e.mu.Unlock()
	if err != nil {
		return sqltype.Value{}, 0, err
	}
	local := *c
	local.Input = binder.input
	local.Input.DomainChecks = &local
	local.routineRecipe = true
	local.routineChecks = binder.dependencies
	local.routineReferences = nil
	local.routineConsumer = nil
	node, _, err = preflightCoercionsWithPolicy(&local, node, false)
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
	return value, 0, nil
}

func (e *Engine) captureDomainReplay(scope *StatementScope, s *parser.Insert, record domainReplayRecord, auth AuthorizationView, relation *catalog.Relation) (*domainReplayCapture, error) {
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
	capture := &domainReplayCapture{statement: s, relation: relation, cells: make(map[int]literalExpression, len(record.Cells))}
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
		if err := bindInsertTargets(relation, s.Targets, slots); err != nil {
			return nil, err
		}
		for _, target := range s.Targets {
			if len(target.Steps) != 0 {
				return nil, fmt.Errorf("stuntgres: native replay does not accept partial targets")
			}
		}
	}
	c := EvalContext{Context: scope.op.Context(), Scope: 1, Frame: 1, statementScope: scope, catalog: auth.Catalog, Input: scope.input, Output: scope.output, routineAuthority: &auth}
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
		if count == 0 || value.Type() != usage.Type {
			return nil, fmt.Errorf("stuntgres: native replay cell has no admitted domain NULL")
		}
		capture.cells[position] = literalExpression{Info: expr.Info{Usage: usage, Pos: raw.SourcePosition()}, value: value}
	}
	return capture, nil
}

func (e *Engine) executeDomainReplayInsert(op *Operation, s *parser.Insert, record domainReplayRecord) (result Result, err error) {
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
	if op.restoreDomainReplay != nil {
		return Result{}, fmt.Errorf("stuntgres: nested native replay transport")
	}
	op.restoreDomainReplay = capture
	defer func() { op.restoreDomainReplay = nil }()
	return scope.Execute(s, 0, nil)
}

func (b *binding) capturedDomainReplayCell(op *Operation, s *parser.Insert, row, column int) (expr.Expr, bool, error) {
	if op == nil || op.restoreDomainReplay == nil || op.restoreDomainReplay.statement != s {
		return nil, false, nil
	}
	capture := op.restoreDomainReplay
	literal, ok := capture.cells[column]
	if !ok {
		return nil, false, nil
	}
	if row != 0 || capture.relation != b.relation || column >= len(b.targets) || literal.ResultUsage() != b.relation.Columns[b.targets[column]].Usage {
		return nil, true, fmt.Errorf("stuntgres: native domain replay destination changed")
	}
	return literal, true, nil
}
