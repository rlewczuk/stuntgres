package engine

import (
	"iter"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

// OperatorRoutineKey is a released operator identity: name and operand types
// (Left is 0 for a prefix operator).
type OperatorRoutineKey struct {
	Name        string
	Left, Right ids.OID
}

// Join immutable released declarations once. Comparison selection and coercion
// happen before this identity lookup; evaluation never consults the catalog.
var builtinOperatorRoutineByDeclaration = indexBuiltinOperatorRoutines()

func indexBuiltinOperatorRoutines() map[OperatorRoutineKey]*catalog.BuiltinRoutineDeclaration {
	byDeclaration := make(map[OperatorRoutineKey]*catalog.BuiltinRoutineDeclaration, len(builtinOperatorRoutineAssociations))
	for _, association := range builtinOperatorRoutineAssociations {
		i := sort.Search(len(builtinOperators), func(i int) bool { return builtinOperators[i].oid >= association.operator })
		if i == len(builtinOperators) || builtinOperators[i].oid != association.operator {
			panic("stuntgres: missing released operator identity")
		}
		operator := builtinOperators[i]
		declaration := builtinRoutineByOID[association.routine]
		if declaration == nil || declaration.Providers&functions.OperatorRoutineProvider == 0 {
			panic("stuntgres: missing genuine operator routine association")
		}
		byDeclaration[OperatorRoutineKey{operator.name, operator.left, operator.right}] = declaration
	}
	return byDeclaration
}

// OperatorRoutine returns the released routine implementing the builtin
// operator name(left, right), or nil when no genuine provider is associated.
func OperatorRoutine(name string, left, right ids.OID) *catalog.BuiltinRoutineDeclaration {
	return builtinOperatorRoutineByDeclaration[OperatorRoutineKey{name, left, right}]
}

// OperatorRoutines yields every released operator routine association, in no
// particular order.
func OperatorRoutines() iter.Seq2[OperatorRoutineKey, *catalog.BuiltinRoutineDeclaration] {
	return func(yield func(OperatorRoutineKey, *catalog.BuiltinRoutineDeclaration) bool) {
		for key, declaration := range builtinOperatorRoutineByDeclaration {
			if !yield(key, declaration) {
				return
			}
		}
	}
}
