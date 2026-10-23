package typedef

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// DomainReplayEnv is what reconstructing a captured native replay cell needs:
// the statement context, the destination catalog, the evaluation input and
// output environments, and the scalar leaf. It is read-only during a walk.
//
// Scalar binds and evaluates the single target of a captured scalar
// "SELECT <expression>" for usage u. The engine implements it: it binds under
// Engine.mu and evaluates outside it, so RestoreDomainReplayNode and
// DomainReplayCells are called without Engine.mu. The walk has already
// checked the statement shape, and rejects a NULL or mistyped result itself.
type DomainReplayEnv struct {
	Context context.Context
	Catalog catalog.Read
	Input   sqltype.InputEnvironment
	Output  sqltype.OutputEnvironment
	Scalar  func(raw parser.Expr, u sqltype.Usage) (sqltype.Value, error)
}

// DomainReplayProvenance is accumulated only after destination-owned
// reconstruction. Only an authentic domain NULL admits private cell replay;
// constructor-only circles use portable SQL and cannot independently
// authorize a sidecar.
type DomainReplayProvenance uint8

const DomainReplayDomainNull DomainReplayProvenance = 1

// RestoreDomainReplayNode rebuilds the value of n for usage u: domain and
// domain-NULL nodes against the domain's captured identity, NULL, array and
// composite shapes recursively, and scalar leaves through env.Scalar. It
// returns the provenance found below n.
func RestoreDomainReplayNode(env *DomainReplayEnv, u sqltype.Usage, n *DomainReplayNode) (sqltype.Value, DomainReplayProvenance, error) {
	bad := func() (sqltype.Value, DomainReplayProvenance, error) {
		return sqltype.Value{}, 0, fmt.Errorf("stuntgres: invalid native domain replay value")
	}
	if n == nil || !u.Valid() {
		return bad()
	}
	if err := env.Context.Err(); err != nil {
		return sqltype.Value{}, 0, err
	}
	if n.Shape("domain_null") || n.Shape("domain") {
		identity, err := DomainReplayIdentity(env.Catalog, u.Type)
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
			return sqltype.Null(u.Type), DomainReplayDomainNull, nil
		}
		base, count, err := RestoreDomainReplayNode(env, u.BaseUsage(), n.Base)
		if err != nil {
			return sqltype.Value{}, 0, err
		}
		if base.IsNull() {
			return bad()
		}
		coercion, err := sqltype.PlanCoercion(u.BaseUsage(), u, sqltype.Assignment)
		if err != nil {
			return sqltype.Value{}, 0, err
		}
		value, err := coercion.Apply(env.Context, base, env.Input, env.Output)
		return value, count, err
	}
	if n.Shape("null") {
		value := sqltype.Null(u.Type)
		if err := u.Type.CheckDomainInput(env.Context, value, env.Input); err != nil {
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
		count := DomainReplayProvenance(0)
		for i := range elements {
			value, found, err := RestoreDomainReplayNode(env, element, &(*n.Elements)[i])
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
		count := DomainReplayProvenance(0)
		for i := range members {
			member := u.Type.CompositeMember(i)
			captured := (*n.Members)[i]
			if captured.Name != member.Name {
				return bad()
			}
			value, found, err := RestoreDomainReplayNode(env, member.Usage, captured.Value)
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
	statements, err := parser.ParseWithOptions("SELECT "+*n.Scalar, parser.Options{Context: env.Context, StandardConformingStrings: true})
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
	value, err := env.Scalar(selectStmt.Targets[0], u)
	if err != nil {
		return sqltype.Value{}, 0, err
	}
	if value.IsNull() || value.Type() != u.Type {
		return bad()
	}
	return value, 0, nil
}

// DomainReplayLiteral is one admitted captured cell: the literal that
// replaces its NULL placeholder, typed by the destination column and placed
// at the placeholder.
type DomainReplayLiteral struct {
	expr.Info
	Value sqltype.Value
}

// DomainReplayCells admits the captured cells of a native replay INSERT s
// into relation, whose single VALUES row maps position i to column slot
// slots[i]. Each cell must name a whole, non-generated column targeted
// exactly once whose VALUES item is an exact NULL placeholder, and its
// reconstruction must carry authentic domain-NULL provenance. The result
// maps VALUES positions to the admitted literals.
func DomainReplayCells(env *DomainReplayEnv, s *parser.Insert, cells []DomainReplayCell, relation *catalog.Relation, slots []int) (map[int]DomainReplayLiteral, error) {
	admitted := make(map[int]DomainReplayLiteral, len(cells))
	for _, cell := range cells {
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
		if len(s.Targets) != 0 && len(s.Targets[position].Steps) != 0 {
			return nil, fmt.Errorf("stuntgres: native replay does not accept partial captured targets")
		}
		if _, duplicate := admitted[position]; duplicate {
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
		value, count, err := RestoreDomainReplayNode(env, usage, cell.Value)
		if err != nil {
			return nil, err
		}
		if count&DomainReplayDomainNull == 0 || value.Type() != usage.Type {
			return nil, fmt.Errorf("stuntgres: native replay cell has no admitted domain NULL")
		}
		admitted[position] = DomainReplayLiteral{Info: expr.Info{Usage: usage, Pos: raw.SourcePosition()}, Value: value}
	}
	return admitted, nil
}
