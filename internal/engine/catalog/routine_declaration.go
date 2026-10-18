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
}

func (d *Procedure) Seeded() bool {
	return d.Builtin != nil || d.Conversion.Name() != ""
}

// Published descriptors are pointer-owned catalog payloads. They remain stable
// across binder dependency-slice growth and carry no cached permission decision.
func (d *Procedure) RoutineOID() uint32 { return uint32(d.Address().ObjectID) }
