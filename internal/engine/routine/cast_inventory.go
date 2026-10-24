package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// The finite executable cast catalog supplies procedure references, not invented
// signatures. Released pg_proc identities/results remain their own authority.
func completeCastRoutines(base []catalog.BuiltinRoutineDeclaration) []catalog.BuiltinRoutineDeclaration {
	byOID := make(map[ids.OID]int, len(base))
	for i := range base {
		byOID[base[i].Identity.ID] = i
	}
	for i := range sqltype.CastMetadataCount() {
		edge, _ := sqltype.CastMetadataAt(i)
		id := ids.OID(edge.Function)
		if id == 0 {
			continue
		}
		if index, ok := byOID[id]; ok {
			if base[index].Providers == functions.EmbeddedRoutineProvider {
				base[index].Providers |= functions.CastRoutineProvider
			}
			continue
		}
		identity, ok := catalog.BuiltinRoutineIdentityByOID(id)
		if !ok {
			panic("stuntgres: missing released cast routine identity")
		}
		result, ok := catalog.BuiltinRoutineResultByOID(id)
		if !ok {
			panic("stuntgres: missing released cast routine result")
		}
		volatility := expr.Immutable
		switch id {
		case 1079, 1174, 1178, 1388, 2019, 2027, 2028, 2047, 3811, 3812, 3823, 3824:
			volatility = expr.Stable
		}
		byOID[id] = len(base)
		base = append(base, catalog.BuiltinRoutineDeclaration{Identity: identity, Kind: catalog.FunctionRoutine, Result: result, Strict: true, Volatility: volatility, Parallel: 's', Providers: functions.CastRoutineProvider})
	}
	for _, id := range []ids.OID{4280, 4283, 4295} {
		identity, ok := catalog.BuiltinRoutineIdentityByOID(id)
		if !ok {
			panic("stuntgres: missing released multirange constructor")
		}
		result, ok := catalog.BuiltinRoutineResultByOID(id)
		if !ok {
			panic("stuntgres: missing released multirange constructor result")
		}
		base = append(base, catalog.BuiltinRoutineDeclaration{Identity: identity, Kind: catalog.FunctionRoutine, Result: result, Strict: true, Volatility: expr.Immutable, Parallel: 's', Providers: functions.CastRoutineProvider})
	}
	return base
}
