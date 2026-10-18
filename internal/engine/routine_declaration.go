package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
)

func grantRoutineKindMatches(target string, kind catalog.RoutineKind) bool {
	switch target {
	case "function":
		return kind == catalog.FunctionRoutine || kind == catalog.AggregateRoutine || kind == catalog.WindowRoutine
	case "procedure":
		return kind == catalog.ProcedureRoutine
	case "routine":
		return kind == catalog.FunctionRoutine || kind == catalog.AggregateRoutine || kind == catalog.WindowRoutine || kind == catalog.ProcedureRoutine
	}
	return false
}

func procedureReturnsSet(d *catalog.Procedure) bool {
	return d.Builtin != nil && d.Builtin.ReturnsSet ||
		intaggProviderOf(d) == stockIntagg && d.RoutineKind == catalog.FunctionRoutine &&
			d.Name() == "int_array_enum" && d.Signature == "1007" && d.Result == 23
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
