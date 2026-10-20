package engine

import (
	"stuntgres/internal/engine/catalog"
)

func GrantRoutineKindMatches(target string, kind catalog.RoutineKind) bool {
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
