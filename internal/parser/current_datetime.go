package parser

import "strings"

func currentDateTimePrecision(l sqlLexer, kind CurrentDateTimeKind, keyword, number lexeme) (CurrentDateTimeExpr, bool) {
	// Both special forms require an unsigned int32 Iconst, not a generic typmod.
	precision, ok := timezoneIntervalPrecision(l, number)
	if !ok {
		return CurrentDateTimeExpr{}, false
	}
	return CurrentDateTimeExpr{Kind: kind, Precision: int32(precision), HasPrecision: true, Pos: keyword.pos}, true
}

// Quoted/qualified spellings are ordinary names, not SQL value functions.
// Admit only their zero-argument syntax so signature resolution can diagnose it.
// Their call tokens also retain ordinary name legality in declaration contexts.
func currentDateTimeOrdinaryName(name string) bool {
	switch len(name) {
	case 9:
		return strings.EqualFold(name, "localtime")
	case 12:
		return strings.EqualFold(name, "current_date") || strings.EqualFold(name, "current_time")
	case 14:
		return strings.EqualFold(name, "localtimestamp")
	case 17:
		return strings.EqualFold(name, "current_timestamp")
	}
	return false
}
