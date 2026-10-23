package functions

import "stuntgres/internal/engine/expr"

// RoutineProvider names the builtin bind families that may select a builtin
// routine declaration (expr.RoutineProvider). A declaration lists its
// providers as a bit set; a family names its own provider when it asks the
// Binder for routine identity. The type lives in expr so that catalog routine
// declarations can hold it; the family constants stay here.
type RoutineProvider = expr.RoutineProvider

const (
	StringRoutineProvider RoutineProvider = 1 << iota
	BinaryRoutineProvider
	MathRoutineProvider
	VariadicRoutineProvider
	JSONRoutineProvider
	ArrayRoutineProvider
	EnumRoutineProvider
	RegexpRoutineProvider
	DatetimeRoutineProvider
	RandomRoutineProvider
	UUIDRoutineProvider
	CatalogRoutineProvider
	AdvisoryRoutineProvider
	SequenceRoutineProvider
	SettingRoutineProvider
	AggregateRoutineProvider
	WindowRoutineProvider
	SeriesRoutineProvider
	SubscriptRoutineProvider
	OperatorRoutineProvider
	RangeRoutineProvider
	EmbeddedRoutineProvider // Existing function-method conversion inside genuine SQL bodies, not standalone dispatch.
	NetworkRoutineProvider
	LargeObjectRoutineProvider
	CircleRoutineProvider
	IndexSupportRoutineProvider
	FTSRoutineProvider
	GeometryConversionRoutineProvider
	GeometryMeasureRoutineProvider
)
