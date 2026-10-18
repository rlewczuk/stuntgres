package engine

import (
	"fmt"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type builtinOperatorRoutineKey struct {
	name        string
	left, right ids.OID
}

// Join immutable released declarations once. Comparison selection and coercion
// happen before this identity lookup; evaluation never consults the catalog.
var builtinOperatorRoutineByDeclaration = indexBuiltinOperatorRoutines()

func indexBuiltinOperatorRoutines() map[builtinOperatorRoutineKey]*catalog.BuiltinRoutineDeclaration {
	byDeclaration := make(map[builtinOperatorRoutineKey]*catalog.BuiltinRoutineDeclaration, len(builtinOperatorRoutineAssociations))
	for _, association := range builtinOperatorRoutineAssociations {
		i := sort.Search(len(builtinOperators), func(i int) bool { return builtinOperators[i].oid >= association.operator })
		if i == len(builtinOperators) || builtinOperators[i].oid != association.operator {
			panic("stuntgres: missing released operator identity")
		}
		operator := builtinOperators[i]
		declaration := builtinRoutineByOID[association.routine]
		if declaration == nil || declaration.Providers&operatorRoutineProvider == 0 {
			panic("stuntgres: missing genuine operator routine association")
		}
		byDeclaration[builtinOperatorRoutineKey{operator.name, operator.left, operator.right}] = declaration
	}
	return byDeclaration
}

func (b *expressionBinder) builtinOperatorRoutineInfo(info expr.Info, name string, left, right sqltype.ID) (expr.Info, error) {
	if name == "!=" {
		name = "<>"
	}
	declaration := builtinOperatorRoutineByDeclaration[builtinOperatorRoutineKey{name, ids.OID(left), ids.OID(right)}]
	if declaration == nil {
		return expr.Info{}, fmt.Errorf("stuntgres: missing successful operator routine association for %s (%d,%d)", name, left, right)
	}
	return b.selectedBuiltinRoutineInfo(info, declaration)
}

// Operands are already the instantiated targets selected by ResolveComparison.
// This lookup records the released routine identity, never a second resolver or
// physical source-pair capability gate.
func (b *expressionBinder) comparisonRoutineDeclaration(name string, left, right *sqltype.Descriptor) (*catalog.BuiltinRoutineDeclaration, sqltype.EqualitySignature, error) {
	if name == "!=" {
		name = "<>"
	}
	formal := sqltype.EqualitySignature{Left: comparisonFormalID(left.BaseType()), Right: comparisonFormalID(right.BaseType())}
	declaration := builtinOperatorRoutineByDeclaration[builtinOperatorRoutineKey{name, ids.OID(formal.Left), ids.OID(formal.Right)}]
	if declaration == nil {
		return nil, sqltype.EqualitySignature{}, fmt.Errorf("stuntgres: missing successful comparison routine association for %s (%d,%d)", name, formal.Left, formal.Right)
	}
	return declaration, formal, nil
}

func comparisonFormalID(d *sqltype.Descriptor) sqltype.ID {
	if d.IsComposite() || d.IsDeferredRecord() {
		return 2249 // RECORD, with occurrence-owned concrete field authority.
	}
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
