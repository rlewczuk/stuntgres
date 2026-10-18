package engine

import (
	"fmt"
	"math"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type relationKind uint8

const (
	tableRelation relationKind = iota
	sequenceRelation
	viewRelation
	catalogRelation
	indexRelation
	compositeRelation
	materializedViewRelation
	partitionedRelation
	foreignRelation
)

func (k relationKind) Stored() bool {
	return k == tableRelation || k == materializedViewRelation
}

type sequenceOptions struct {
	SQLType                           sqltype.ID
	Increment, Min, Max, Start, Cache int64
	Cycle                             bool
	Persistence                       string
	Owned                             ids.ObjectAddress
	Identity                          bool
}
type sequenceGenerator struct {
	last       int64
	called     bool
	generation ids.DefinitionRevision
}
type sequenceMemory struct {
	value      int64
	valid      bool
	next       int64
	remaining  uint64
	generation ids.DefinitionRevision
	// Local relation-source invalidation survives reverted reset without
	// borrowing generator/cache identity or changing currval/lastval.
	analysisStamp ids.DefinitionRevision
}

func sequenceTypeLimits(id sqltype.ID) (int64, int64, string) {
	switch id {
	case sqltype.Int2:
		return math.MinInt16, math.MaxInt16, "smallint"
	case sqltype.Int4:
		return math.MinInt32, math.MaxInt32, "integer"
	}
	return math.MinInt64, math.MaxInt64, "bigint"
}
func sequenceDefaults(id sqltype.ID, inc int64) (int64, int64) {
	lo, hi, _ := sequenceTypeLimits(id)
	if inc < 0 {
		return lo, -1
	}
	return 1, hi
}
func sequenceOptionError(format string, args ...any) error {
	return sqlError("22023", fmt.Sprintf(format, args...))
}
func bindSequenceOptions(old *sequenceOptions, state sequenceGenerator, raw []parser.SequenceOption) (sequenceOptions, *int64, error) {
	o := sequenceOptions{SQLType: sqltype.Int8, Increment: 1, Cache: 1, Persistence: "logged"}
	if old != nil {
		o = *old
	}
	seen := make(map[string]parser.SequenceOption, len(raw))
	for _, r := range raw {
		if _, ok := seen[r.Name]; ok {
			return o, nil, expr.ErrorAt("42601", "conflicting or redundant options", r.Pos)
		}
		seen[r.Name] = r
	}
	if r, ok := seen["as"]; ok {
		switch r.Type.Name {
		case "smallint", "int2":
			o.SQLType = sqltype.Int2
		case "integer", "int", "int4":
			o.SQLType = sqltype.Int4
		case "bigint", "int8":
			o.SQLType = sqltype.Int8
		default:
			return o, nil, sequenceOptionError("sequence type must be smallint, integer, or bigint")
		}
		if len(r.Type.Modifiers) != 0 {
			return o, nil, sequenceOptionError("sequence type must be smallint, integer, or bigint")
		}
	}
	if r, ok := seen["increment"]; ok {
		o.Increment = r.Value
	}
	if o.Increment == 0 {
		return o, nil, sequenceOptionError("INCREMENT must not be zero")
	}
	defmin, defmax := sequenceDefaults(o.SQLType, o.Increment)
	if old == nil {
		o.Min, o.Max = defmin, defmax
	} else if old.SQLType != o.SQLType {
		oldmin, oldmax, _ := sequenceTypeLimits(old.SQLType)
		newmin, newmax, _ := sequenceTypeLimits(o.SQLType)
		if old.Min == oldmin {
			o.Min = newmin
		}
		if old.Max == oldmax {
			o.Max = newmax
		}
	}
	if r, ok := seen["min"]; ok {
		if r.Type.Name == "default" {
			o.Min = defmin
		} else {
			o.Min = r.Value
		}
	}
	if r, ok := seen["max"]; ok {
		if r.Type.Name == "default" {
			o.Max = defmax
		} else {
			o.Max = r.Value
		}
	}
	lo, hi, typ := sequenceTypeLimits(o.SQLType)
	if o.Max < lo || o.Max > hi {
		return o, nil, sequenceOptionError("MAXVALUE (%d) is out of range for sequence data type %s", o.Max, typ)
	}
	if o.Min < lo || o.Min > hi {
		return o, nil, sequenceOptionError("MINVALUE (%d) is out of range for sequence data type %s", o.Min, typ)
	}
	if o.Min >= o.Max {
		return o, nil, sequenceOptionError("MINVALUE (%d) must be less than MAXVALUE (%d)", o.Min, o.Max)
	}
	if old == nil {
		if o.Increment > 0 {
			o.Start = o.Min
		} else {
			o.Start = o.Max
		}
	}
	if r, ok := seen["start"]; ok {
		o.Start = r.Value
	}
	if err := sequenceBoundOption("START", o.Start, o); err != nil {
		return o, nil, err
	}
	if r, ok := seen["cache"]; ok {
		o.Cache = r.Value
	}
	if o.Cache <= 0 {
		return o, nil, sequenceOptionError("CACHE (%d) must be greater than zero", o.Cache)
	}
	if r, ok := seen["cycle"]; ok {
		o.Cycle = r.Value != 0
	}
	var restart *int64
	if r, ok := seen["restart"]; ok {
		v := r.Value
		if r.Type.Name == "default" {
			v = o.Start
		}
		restart = &v
		if err := sequenceBoundOption("RESTART", v, o); err != nil {
			return o, nil, err
		}
	}
	if old != nil && restart == nil {
		if err := sequenceBoundOption("RESTART", state.last, o); err != nil {
			return o, nil, err
		}
	}
	return o, restart, nil
}
func sequenceBoundOption(name string, v int64, o sequenceOptions) error {
	if v < o.Min {
		return sequenceOptionError("%s value (%d) cannot be less than MINVALUE (%d)", name, v, o.Min)
	}
	if v > o.Max {
		return sequenceOptionError("%s value (%d) cannot be greater than MAXVALUE (%d)", name, v, o.Max)
	}
	return nil
}

// Ordered unsigned distance avoids overflowing either signed subtraction or
// multiplication at INT64 endpoints, including increment=INT64_MIN.
func sequenceDistance(a, b int64) uint64 {
	return (uint64(b) ^ uint64(1<<63)) - (uint64(a) ^ uint64(1<<63))
}
func sequenceStep(o sequenceOptions, v int64) (int64, bool) {
	if o.Increment > 0 {
		if sequenceDistance(v, o.Max) < uint64(o.Increment) {
			return o.Min, false
		}
		return v + o.Increment, true
	}
	step := uint64(-(o.Increment + 1)) + 1
	if sequenceDistance(o.Min, v) < step {
		return o.Max, false
	}
	return v + o.Increment, true
}
func reserveSequence(o sequenceOptions, g *sequenceGenerator, name string) (int64, uint64, error) {
	first := g.last
	if g.called {
		v, ok := sequenceStep(o, first)
		if !ok && !o.Cycle {
			which, bound := "maximum", o.Max
			if o.Increment < 0 {
				which, bound = "minimum", o.Min
			}
			return 0, 0, sqlError("2200H", fmt.Sprintf(`nextval: reached %s value of sequence "%s" (%d)`, which, name, bound))
		}
		first = v
	}
	var distance, step uint64
	if o.Increment > 0 {
		distance, step = sequenceDistance(first, o.Max), uint64(o.Increment)
	} else {
		distance, step = sequenceDistance(o.Min, first), uint64(-(o.Increment+1))+1
	}
	// Cache may end at a bound; wrapping is deferred to the next reservation.
	count := uint64(o.Cache)
	available := distance / step
	if count-1 > available {
		count = available + 1
	}
	delta := (count - 1) * step
	last := int64(uint64(first) + delta)
	if o.Increment < 0 {
		last = int64(uint64(first) - delta)
	}
	g.last, g.called = last, true
	return first, count, nil
}
