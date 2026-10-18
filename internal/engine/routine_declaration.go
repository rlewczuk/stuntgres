package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

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
	return d.Builtin != nil && d.Builtin.returnsSet ||
		d.intaggProvider() == stockIntagg && d.RoutineKind == functionRoutine &&
			d.name == "int_array_enum" && d.Signature == "1007" && d.Result == 23
}

// A provider association describes an already-implemented declaration, not an
// execution capability. It cannot make a lookup-only signature callable.
type builtinRoutineDeclaration struct {
	identity   routineIdentity
	kind       routineKind
	result     ids.OID
	strict     bool
	volatility expr.Volatility
	parallel   byte
	returnsSet bool
	variadic   ids.OID
	defaults   uint8
	providers  routineProvider
}

// Temporary aliases for the provider vocabulary that moved to functions in
// ENGINE-SPLIT step 3 F3. A later alias-removal sub-step replaces their uses.
type routineProvider = functions.RoutineProvider

const (
	stringRoutineProvider    = functions.StringRoutineProvider
	binaryRoutineProvider    = functions.BinaryRoutineProvider
	mathRoutineProvider      = functions.MathRoutineProvider
	variadicRoutineProvider  = functions.VariadicRoutineProvider
	jsonRoutineProvider      = functions.JSONRoutineProvider
	arrayRoutineProvider     = functions.ArrayRoutineProvider
	enumRoutineProvider      = functions.EnumRoutineProvider
	regexpRoutineProvider    = functions.RegexpRoutineProvider
	datetimeRoutineProvider  = functions.DatetimeRoutineProvider
	randomRoutineProvider    = functions.RandomRoutineProvider
	uuidRoutineProvider      = functions.UUIDRoutineProvider
	catalogRoutineProvider   = functions.CatalogRoutineProvider
	advisoryRoutineProvider  = functions.AdvisoryRoutineProvider
	sequenceRoutineProvider  = functions.SequenceRoutineProvider
	settingRoutineProvider   = functions.SettingRoutineProvider
	aggregateRoutineProvider = functions.AggregateRoutineProvider
	windowRoutineProvider    = functions.WindowRoutineProvider
	seriesRoutineProvider    = functions.SeriesRoutineProvider
	subscriptRoutineProvider = functions.SubscriptRoutineProvider
	operatorRoutineProvider  = functions.OperatorRoutineProvider
	rangeRoutineProvider     = functions.RangeRoutineProvider
	embeddedRoutineProvider  = functions.EmbeddedRoutineProvider
	networkRoutineProvider   = functions.NetworkRoutineProvider
)

func (d *procedureDescriptor) seeded() bool {
	return d.Builtin != nil || d.Conversion.Name() != ""
}

// Published descriptors are pointer-owned catalog payloads. They remain stable
// across binder dependency-slice growth and carry no cached permission decision.
func (d *procedureDescriptor) RoutineOID() uint32 { return uint32(d.address.ObjectID) }
