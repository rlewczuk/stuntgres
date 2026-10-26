package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

var builtinRoutineLiteralDefaults = func() map[ids.OID]*catalog.RoutineLiteralDefaults {
	recipes := make(map[ids.OID]*catalog.RoutineLiteralDefaults)
	for i := range BuiltinRoutineDeclarations {
		d := &BuiltinRoutineDeclarations[i]
		recipe := builtinLiteralDefaults(d.Identity.ID)
		if recipe.Len() != int(d.Defaults) {
			panic("stuntgres: incomplete builtin default declaration")
		}
		if recipe != nil {
			recipes[d.Identity.ID] = recipe
		}
	}
	return recipes
}()

// These are the source constants of the released builtin declarations, not
// invocation defaults. Presentation and codec authority are captured on demand.
func builtinLiteralDefaults(oid ids.OID) *catalog.RoutineLiteralDefaults {
	literal := func(typ ids.OID, text string) catalog.RoutineLiteral {
		return catalog.RoutineLiteral{SourceType: typ, Text: text}
	}
	switch oid {
	case 3464:
		return catalog.NewRoutineLiteralDefaults(literal(23, "0"), literal(23, "0"), literal(23, "0"), literal(23, "0"), literal(23, "0"), literal(23, "0"), literal(1700, "0.0"))
	case 3579, 3261, 3960, 3961:
		return catalog.NewRoutineLiteralDefaults(literal(16, "false"))
	case 4005, 4006, 4007, 4008, 4009:
		return catalog.NewRoutineLiteralDefaults(literal(3802, "{}"), literal(16, "false"))
	case 4350, 4351:
		return catalog.NewRoutineLiteralDefaults(literal(25, "NFC"))
	case 6212:
		return catalog.NewRoutineLiteralDefaults(literal(23, "0"), literal(23, "1"))
	case 3305, 1268:
		return catalog.NewRoutineLiteralDefaults(literal(16, "true"))
	case 5054:
		return catalog.NewRoutineLiteralDefaults(literal(16, "true"), literal(25, "use_json_null"))
	default:
		return nil
	}
}
