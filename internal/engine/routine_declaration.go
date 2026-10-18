package engine

import "stuntgres/internal/engine/ids"

// SQL routine kind is independent of the catalog's ProcedureObject class.
type routineKind byte

const (
	functionRoutine  routineKind = 'f'
	aggregateRoutine routineKind = 'a'
	windowRoutine    routineKind = 'w'
	procedureRoutine routineKind = 'p'
)

func grantRoutineKindMatches(target string, kind routineKind) bool {
	switch target {
	case "function":
		return kind == functionRoutine || kind == aggregateRoutine || kind == windowRoutine
	case "procedure":
		return kind == procedureRoutine
	case "routine":
		return kind == functionRoutine || kind == aggregateRoutine || kind == windowRoutine || kind == procedureRoutine
	}
	return false
}

func (d *procedureDescriptor) returnsSet() bool {
	return d.builtin != nil && d.builtin.returnsSet ||
		d.intagg == stockIntagg && d.routineKind == functionRoutine &&
			d.name == "int_array_enum" && d.signature == "1007" && d.result == 23
}

// A provider association describes an already-implemented declaration, not an
// execution capability. It cannot make a lookup-only signature callable.
type builtinRoutineDeclaration struct {
	identity   routineIdentity
	kind       routineKind
	result     ids.OID
	strict     bool
	volatility Volatility
	parallel   byte
	returnsSet bool
	variadic   ids.OID
	defaults   uint8
	providers  routineProvider
}

type routineProvider uint32

const (
	stringRoutineProvider routineProvider = 1 << iota
	binaryRoutineProvider
	mathRoutineProvider
	variadicRoutineProvider
	jsonRoutineProvider
	arrayRoutineProvider
	enumRoutineProvider
	regexpRoutineProvider
	datetimeRoutineProvider
	randomRoutineProvider
	uuidRoutineProvider
	catalogRoutineProvider
	advisoryRoutineProvider
	sequenceRoutineProvider
	settingRoutineProvider
	aggregateRoutineProvider
	windowRoutineProvider
	seriesRoutineProvider
	subscriptRoutineProvider
	operatorRoutineProvider
	rangeRoutineProvider
	embeddedRoutineProvider // Existing function-method conversion inside genuine SQL bodies, not standalone dispatch.
	networkRoutineProvider
)

func (d *procedureDescriptor) seeded() bool {
	return d.builtin != nil || d.conversion.Name() != ""
}

// Published descriptors are pointer-owned catalog payloads. They remain stable
// across binder dependency-slice growth and carry no cached permission decision.
func (d *procedureDescriptor) RoutineOID() uint32 { return uint32(d.address.ObjectID) }
