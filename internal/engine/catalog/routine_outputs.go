package catalog

import (
	"slices"
	"strings"

	"stuntgres/internal/engine/ids"
)

// RoutineOutputs owns a fixed native declaration whose IN slots precede its
// OUT slots. Input types remain solely in Procedure.Signature. Numeric output
// types are released catalog identities, not call-specialized record shapes.
// Private backing keeps shared builtin and installed declarations immutable.
type RoutineOutputs struct {
	types []ids.OID
	names []string // complete formal order, including unnamed input placeholders
}

// NewRoutineOutputs freezes declaration facts at their builtin/manifest owner.
// Invalid facts are programming errors in that finite declaration inventory.
func NewRoutineOutputs(types []ids.OID, names ...string) *RoutineOutputs {
	if len(types) == 0 || len(names) < len(types) {
		panic("stuntgres: invalid fixed output declaration")
	}
	for i, id := range types {
		if id == 0 || names[len(names)-len(types)+i] == "" {
			panic("stuntgres: invalid fixed output formal")
		}
	}
	return &RoutineOutputs{types: slices.Clone(types), names: slices.Clone(names)}
}

func (d *RoutineOutputs) Len() int           { return len(d.types) }
func (d *RoutineOutputs) Type(i int) ids.OID { return d.types[i] }
func (d *RoutineOutputs) Name(i int) string  { return d.names[i] }
func (d *RoutineOutputs) Inputs() int        { return len(d.names) - len(d.types) }

// MatchesInputs checks shape without duplicating the input type signature.
func (d *RoutineOutputs) MatchesInputs(signature ProcedureSignature) bool {
	inputs := 0
	if signature != "" {
		inputs = strings.Count(string(signature), ",") + 1
	}
	return d != nil && d.Inputs() == inputs
}

func (d *Procedure) OutputDeclaration() *RoutineOutputs {
	if d.Builtin != nil {
		return d.Builtin.Outputs
	}
	if d.Implementation != nil {
		return d.Installed.Outputs
	}
	return nil
}
