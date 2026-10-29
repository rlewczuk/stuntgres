package catalog

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// SQL routine kind is independent of the catalog's ProcedureObject class.
type RoutineKind byte

const (
	FunctionRoutine  RoutineKind = 'f'
	AggregateRoutine RoutineKind = 'a'
	WindowRoutine    RoutineKind = 'w'
	ProcedureRoutine RoutineKind = 'p'
)

// A provider association describes an already-implemented declaration, not an
// execution capability. It cannot make a lookup-only signature callable.
type BuiltinRoutineDeclaration struct {
	Identity   RoutineIdentity
	Kind       RoutineKind
	Result     ids.OID
	Strict     bool
	Volatility expr.Volatility
	Parallel   byte
	ReturnsSet bool
	Variadic   ids.OID
	Defaults   uint8
	Providers  expr.RoutineProvider
	Outputs    *RoutineOutputs
}

// ReturnsSet describes the intrinsic result capability, independently of mutable
// row estimates and the routine's current public name.
func (d *Procedure) ReturnsSet() bool {
	if d.SQL != nil {
		return d.SQL.ReturnsSet
	}
	if d.Builtin != nil {
		return d.Builtin.ReturnsSet
	}
	return d.Installed.ReturnsSet
}

// RowEstimate applies an explicit canonical estimate only to a genuine set
// declaration. Zero leaves the immutable provider's released default intact.
func (d *Procedure) RowEstimate() float32 {
	if !d.ReturnsSet() {
		return 0
	}
	if d.Rows > 0 {
		return d.Rows
	}
	if d.SQL != nil {
		return 1000
	}
	if d.Builtin != nil {
		return builtinRoutineRows(d.Builtin)
	}
	return d.Installed.Rows
}

// CostEstimate resolves the declaration's zero sentinel with the same language
// defaults for catalog rows and routine-definition presentation.
func (d *Procedure) CostEstimate() float32 {
	if d.Cost != 0 {
		return d.Cost
	}
	if d.Display.Language == SQLRoutineLanguage || d.Display.Language == PLpgSQLRoutineLanguage {
		return 100
	}
	return 1
}

func (d *Procedure) Seeded() bool {
	return d.SnowballHelper != NoSnowballHelper || d.Builtin != nil || d.Conversion.Name() != "" || d.BrinSupport != 0 || d.AccessMethod != NoAccessMethod && NativeHandler(d.Address().ObjectID) == d.AccessMethod
}

// Snowball helpers are allocated after initdb stops making pinned objects.
// Seeded still identifies their intrinsic implementation and replay authority.
func (d *Procedure) Pinned() bool {
	return d.Seeded() && d.SnowballHelper == NoSnowballHelper
}

// Published descriptors are pointer-owned catalog payloads. They remain stable
// across binder dependency-slice growth and carry no cached permission decision.
func (d *Procedure) RoutineOID() uint32 { return uint32(d.Address().ObjectID) }
