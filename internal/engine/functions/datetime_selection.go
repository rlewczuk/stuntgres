package functions

import "stuntgres/internal/sqltype"

// TemporalTypes contains only engine-supplied, generation-owned builtin usages.
type TemporalTypes struct{ Text, Date, Time, Timetz, Timestamp, Timestamptz, Interval, Int4, Float8 sqltype.Usage }

func (t TemporalTypes) Usage(id sqltype.ID) sqltype.Usage {
	switch id {
	case sqltype.Text:
		return t.Text
	case sqltype.Date:
		return t.Date
	case sqltype.Time:
		return t.Time
	case sqltype.Timetz:
		return t.Timetz
	case sqltype.Timestamp:
		return t.Timestamp
	case sqltype.Timestamptz:
		return t.Timestamptz
	case sqltype.Interval:
		return t.Interval
	case sqltype.Int4:
		return t.Int4
	case sqltype.Float8:
		return t.Float8
	}
	return sqltype.Usage{}
}

type TemporalDemand uint16

const (
	NeedText TemporalDemand = 1 << iota
	NeedDate
	NeedTime
	NeedTimetz
	NeedTimestamp
	NeedTimestamptz
	NeedInterval
	NeedInt4
	NeedFloat8
)

func temporalDemand(id sqltype.ID) TemporalDemand {
	switch id {
	case sqltype.Text:
		return NeedText
	case sqltype.Date:
		return NeedDate
	case sqltype.Time:
		return NeedTime
	case sqltype.Timetz:
		return NeedTimetz
	case sqltype.Timestamp:
		return NeedTimestamp
	case sqltype.Timestamptz:
		return NeedTimestamptz
	case sqltype.Interval:
		return NeedInterval
	case sqltype.Int4:
		return NeedInt4
	case sqltype.Float8:
		return NeedFloat8
	}
	return 0
}

type TemporalSelection struct {
	Selected, Input sqltype.ID
	Resolution      Resolution
	Unsupported     string
}
