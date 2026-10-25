package parser

import "slices"

// aggregateUtilityArguments normalizes utility identity only; ORDER BY does not
// grant ordered-set declaration or invocation capabilities.
func (l *lexer) aggregateUtilityArguments(direct, ordered []GrantRoutineArgument) ([]GrantRoutineArgument, bool) {
	for _, arguments := range [][]GrantRoutineArgument{direct, ordered} {
		for _, argument := range arguments {
			if argument.Mode == "out" || argument.Mode == "inout" {
				l.failAt("0A000", "aggregates cannot have output arguments", argument.Pos.Offset)
				return nil, false
			}
		}
	}
	if len(direct) != 0 && len(ordered) != 0 && direct[len(direct)-1].Mode == "variadic" {
		last, first := direct[len(direct)-1], ordered[0]
		if len(ordered) != 1 || first.Mode != "variadic" || !aggregateUtilitySameType(last.Type, first.Type) {
			l.failAt("0A000", "an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type", first.Pos.Offset)
			return nil, false
		}
		return direct, true
	}
	return append(direct, ordered...), true
}

func aggregateUtilitySameType(a, b TypeName) bool {
	// Source locations and parameter names are not type identity. PostgreSQL
	// compares parsed types here, before catalog alias/type resolution.
	// Declaration syntax canonicalizes generic int4/int2/int8/bool names.
	// Their preserved components still distinguish them from SQL keyword
	// aliases that actually construct pg_catalog-qualified parsed names.
	if len(a.Parts) != 0 {
		a.Name = a.Parts[len(a.Parts)-1].Text
	}
	if len(b.Parts) != 0 {
		b.Name = b.Parts[len(b.Parts)-1].Text
	}
	a, b = standaloneType(a), standaloneType(b)
	if a.Name != b.Name || a.Setof != b.Setof || !slices.Equal(a.ArrayBounds, b.ArrayBounds) || len(a.Modifiers) != len(b.Modifiers) {
		return false
	}
	// SQL type aliases construct pg_catalog-qualified parsed names in
	// PostgreSQL. System is our storage shorthand, not an extra name part.
	count := aggregateUtilityNameCount(a)
	if count != aggregateUtilityNameCount(b) {
		return false
	}
	for i := range count {
		if aggregateUtilityNamePart(a, i) != aggregateUtilityNamePart(b, i) {
			return false
		}
	}
	for i, am := range a.Modifiers {
		bm := b.Modifiers[i]
		if am.Raw != bm.Raw || am.UnaryPlus != bm.UnaryPlus || am.NonSimple != bm.NonSimple || (am.Integer == nil) != (bm.Integer == nil) {
			return false
		}
		if am.Integer != nil && *am.Integer != *bm.Integer {
			return false
		}
	}
	return true
}

func aggregateUtilityNameCount(raw TypeName) int {
	if raw.System {
		return 2
	}
	if len(raw.Parts) != 0 {
		return len(raw.Parts)
	}
	if raw.Schema != nil {
		return 2
	}
	return 1
}

func aggregateUtilityNamePart(raw TypeName, i int) string {
	if raw.System {
		if i == 0 {
			return "pg_catalog"
		}
		return raw.Name
	}
	if len(raw.Parts) != 0 {
		return raw.Parts[i].Text
	}
	if raw.Schema != nil && i == 0 {
		return raw.Schema.Text
	}
	return raw.Name
}
