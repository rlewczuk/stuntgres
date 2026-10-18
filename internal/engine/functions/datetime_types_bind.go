package functions

import "stuntgres/internal/sqltype"

// TemporalTypesFor supplies the binding catalog's usages for the types a
// prepared datetime resolution demands, and nothing else.
func TemporalTypesFor(b Binder, demand TemporalDemand) TemporalTypes {
	var types TemporalTypes
	if demand == 0 {
		return types
	}
	if demand&NeedText != 0 {
		types.Text = b.Type(sqltype.Text).Usage()
	}
	if demand&NeedDate != 0 {
		types.Date = b.Type(sqltype.Date).Usage()
	}
	if demand&NeedTime != 0 {
		types.Time = b.Type(sqltype.Time).Usage()
	}
	if demand&NeedTimetz != 0 {
		types.Timetz = b.Type(sqltype.Timetz).Usage()
	}
	if demand&NeedTimestamp != 0 {
		types.Timestamp = b.Type(sqltype.Timestamp).Usage()
	}
	if demand&NeedTimestamptz != 0 {
		types.Timestamptz = b.Type(sqltype.Timestamptz).Usage()
	}
	if demand&NeedInterval != 0 {
		types.Interval = b.Type(sqltype.Interval).Usage()
	}
	if demand&NeedInt4 != 0 {
		types.Int4 = b.Type(sqltype.Int4).Usage()
	}
	if demand&NeedFloat8 != 0 {
		types.Float8 = b.Type(sqltype.Float8).Usage()
	}
	return types
}
