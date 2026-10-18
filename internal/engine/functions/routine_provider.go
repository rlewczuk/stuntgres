package functions

// RoutineProvider names the builtin bind families that may select a builtin
// routine declaration. A declaration lists its providers as a bit set; a
// family names its own provider when it asks the Binder for routine identity.
// The association describes an implemented declaration, not an execution
// capability.
type RoutineProvider uint32

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
)
