package engine

import (
	"fmt"
	"sort"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type builtinOperatorRoutineKey struct {
	name        string
	left, right OID
}

// This index joins the two immutable release declarations, not runtime child
// types. Existing arithmetic/comparison/pattern capability gates remain required.
var builtinOperatorRoutineByDeclaration = indexBuiltinOperatorRoutines()

func indexBuiltinOperatorRoutines() map[builtinOperatorRoutineKey]*builtinRoutineDeclaration {
	byDeclaration := make(map[builtinOperatorRoutineKey]*builtinRoutineDeclaration, len(builtinOperatorRoutineAssociations))
	for _, association := range builtinOperatorRoutineAssociations {
		i := sort.Search(len(builtinOperators), func(i int) bool { return builtinOperators[i].oid >= association.operator })
		if i == len(builtinOperators) || builtinOperators[i].oid != association.operator {
			panic("stuntgres: missing released operator identity")
		}
		operator := builtinOperators[i]
		declaration := builtinRoutineByOID[association.routine]
		if declaration == nil || declaration.providers&operatorRoutineProvider == 0 {
			panic("stuntgres: missing genuine operator routine association")
		}
		byDeclaration[builtinOperatorRoutineKey{operator.name, operator.left, operator.right}] = declaration
	}
	return byDeclaration
}

func (b *expressionBinder) builtinOperatorRoutineInfo(info expressionInfo, name string, left, right sqltype.ID) (expressionInfo, error) {
	if name == "!=" {
		name = "<>"
	}
	declaration := builtinOperatorRoutineByDeclaration[builtinOperatorRoutineKey{name, OID(left), OID(right)}]
	if declaration == nil {
		return expressionInfo{}, fmt.Errorf("stuntgres: missing successful operator routine association for %s (%d,%d)", name, left, right)
	}
	return b.selectedBuiltinRoutineInfo(info, declaration)
}

// Called only after the existing native comparison capability gate. Formal
// resolution identifies an admitted provider; it cannot enable a released-only
// operand pair. Known child usages and alias input captures remain untouched.
func (b *expressionBinder) comparisonRoutineDeclaration(name string, left, right *sqltype.Descriptor) (*builtinRoutineDeclaration, sqltype.EqualitySignature, error) {
	if name == "!=" {
		name = "<>"
	}
	left, right = b.comparisonCoercionTypes(left.BaseType(), right.BaseType())
	if left == right && left.IsArray() && left.Element().BaseType().ID() == sqltype.ACLItem && name != "=" && name != "<>" {
		// This existing predicate is a demand-time 42883 failure, not a
		// successful array ordering invocation with a released callable.
		return nil, sqltype.EqualitySignature{}, nil
	}
	if !hasScalarComparisonProvider(left) || !hasScalarComparisonProvider(right) {
		return nil, sqltype.EqualitySignature{}, sqlError("0A000", "scalar comparison provider is unsupported in embedded mode")
	}
	formal := sqltype.EqualitySignature{Left: comparisonFormalID(left), Right: comparisonFormalID(right)}
	if declaration := builtinOperatorRoutineByDeclaration[builtinOperatorRoutineKey{name, OID(formal.Left), OID(formal.Right)}]; declaration != nil {
		return declaration, formal, nil
	}
	selected, err := sqltype.ResolveEquality(left, right)
	if err != nil {
		return nil, sqltype.EqualitySignature{}, err
	}
	declaration := builtinOperatorRoutineByDeclaration[builtinOperatorRoutineKey{name, OID(selected.Left), OID(selected.Right)}]
	if declaration == nil {
		return nil, sqltype.EqualitySignature{}, fmt.Errorf("stuntgres: missing successful comparison routine association for %s (%d,%d)", name, selected.Left, selected.Right)
	}
	return declaration, selected, nil
}

func comparisonFormalID(d *sqltype.Descriptor) sqltype.ID {
	if d.IsEnum() {
		return 3500 // Released ANYENUM declaration; no concrete enum promotion.
	}
	if d.IsArray() {
		return 2277 // Released ANYARRAY declaration after actual capability admission.
	}
	id := d.ID()
	if formal := rangeRoutineFormalID(id); formal != 0 {
		return formal
	}
	if id == sqltype.Regclass || id == sqltype.Regtype || id == sqltype.Regrole || sqltype.IsRoutineAlias(id) || sqltype.IsOperatorAlias(id) || sqltype.IsRegObjectAlias(id) || sqltype.IsRegTS(id) {
		return sqltype.Oid
	}
	return id
}

// Normalize only the stock physical providers admitted by existing range
// authority. Other operands remain scalar subtypes, not released-only ranges.
func rangeRoutineFormalID(id sqltype.ID) sqltype.ID {
	switch id {
	case sqltype.Int4Range, sqltype.Int8Range, sqltype.NumRange,
		sqltype.DateRange, sqltype.TimestampRange, sqltype.TimestamptzRange:
		return 3831 // ANYRANGE
	case sqltype.Int4Multirange, sqltype.Int8Multirange, sqltype.NumMultirange:
		return 4537 // ANYMULTIRANGE
	}
	return 0
}

// Only unresolved OID-formal input changes its decoding target. Relabel the
// decoded unsigned datum to the already admitted physical alias provider using
// the existing direct implicit edge; never resolve symbolic text as an alias.
func (b *expressionBinder) comparisonOperandWithFormalInput(operand comparisonOperand, physical *sqltype.Descriptor, formal sqltype.ID, name string) (BoundExpr, error) {
	_, literal := operand.raw.(parser.LiteralExpr)
	if !literal || operand.descriptor() != nil || formal != sqltype.Oid || physical.ID() == sqltype.Oid {
		return b.coerceConditional(operand, physical.Usage(), name)
	}
	node, err := b.coerceConditional(operand, b.typ(sqltype.Oid).Usage(), name)
	if err != nil {
		return nil, err
	}
	return b.coerceConditional(comparisonOperand{node: node, raw: operand.raw}, physical.Usage(), name)
}

// Match the actual Compare early branches/switch and the explicit ACL predicate
// provider, not its historical homogeneous/default-zero admission shortcut.
// Arrays own a structural provider: leaf capability and collation failures are
// demand-time decisions, not grounds to reject their released ANYARRAY formal.
// PgNodeTree's private grouping provider is a separate, noncallable path.
func hasScalarComparisonProvider(d *sqltype.Descriptor) bool {
	if d.IsArray() || d.IsEnum() || d.ContainsACLItem() {
		return true
	}
	switch d.ID() {
	case sqltype.Bool, sqltype.Bytea, sqltype.Bit, sqltype.VarBit, sqltype.Char, sqltype.Name,
		sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Money,
		sqltype.Text, sqltype.BPChar, sqltype.VarChar,
		sqltype.Float4, sqltype.Float8, sqltype.Numeric,
		sqltype.Date, sqltype.Time, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Timetz, sqltype.Interval,
		sqltype.Oid, sqltype.Tid, sqltype.Xid, sqltype.Cid, sqltype.Xid8, sqltype.UUID, sqltype.JSONB, sqltype.Inet, sqltype.Cidr:
		return true
	}
	formal := comparisonFormalID(d)
	return formal == sqltype.Oid || formal == 3831 || formal == 4537
}
