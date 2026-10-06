package engine

import (
	"fmt"
	"math"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type relationKind uint8

const (
	tableRelation relationKind = iota
	sequenceRelation
)

type sequenceOptions struct {
	typ                               sqltype.ID
	increment, min, max, start, cache int64
	cycle                             bool
	persistence                       string
	owned                             ObjectAddress
}
type sequenceGenerator struct {
	last       int64
	called     bool
	generation DefinitionRevision
}
type sequenceMemory struct {
	value      int64
	valid      bool
	next       int64
	remaining  uint64
	generation DefinitionRevision
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
	o := sequenceOptions{typ: sqltype.Int8, increment: 1, cache: 1, persistence: "logged"}
	if old != nil {
		o = *old
	}
	seen := make(map[string]parser.SequenceOption, len(raw))
	for _, r := range raw {
		if _, ok := seen[r.Name]; ok {
			return o, nil, expressionError("42601", "conflicting or redundant options", r.Pos)
		}
		seen[r.Name] = r
	}
	if r, ok := seen["as"]; ok {
		switch r.Type.Name {
		case "smallint", "int2":
			o.typ = sqltype.Int2
		case "integer", "int", "int4":
			o.typ = sqltype.Int4
		case "bigint", "int8":
			o.typ = sqltype.Int8
		default:
			return o, nil, sequenceOptionError("sequence type must be smallint, integer, or bigint")
		}
		if len(r.Type.Modifiers) != 0 {
			return o, nil, sequenceOptionError("sequence type must be smallint, integer, or bigint")
		}
	}
	if r, ok := seen["increment"]; ok {
		o.increment = r.Value
	}
	if o.increment == 0 {
		return o, nil, sequenceOptionError("INCREMENT must not be zero")
	}
	defmin, defmax := sequenceDefaults(o.typ, o.increment)
	if old == nil {
		o.min, o.max = defmin, defmax
	} else if old.typ != o.typ {
		oldmin, oldmax, _ := sequenceTypeLimits(old.typ)
		newmin, newmax, _ := sequenceTypeLimits(o.typ)
		if old.min == oldmin {
			o.min = newmin
		}
		if old.max == oldmax {
			o.max = newmax
		}
	}
	if r, ok := seen["min"]; ok {
		if r.Type.Name == "default" {
			o.min = defmin
		} else {
			o.min = r.Value
		}
	}
	if r, ok := seen["max"]; ok {
		if r.Type.Name == "default" {
			o.max = defmax
		} else {
			o.max = r.Value
		}
	}
	lo, hi, typ := sequenceTypeLimits(o.typ)
	if o.min < lo || o.min > hi {
		return o, nil, sequenceOptionError("MINVALUE (%d) is out of range for sequence data type %s", o.min, typ)
	}
	if o.max < lo || o.max > hi {
		return o, nil, sequenceOptionError("MAXVALUE (%d) is out of range for sequence data type %s", o.max, typ)
	}
	if o.min >= o.max {
		return o, nil, sequenceOptionError("MINVALUE (%d) must be less than MAXVALUE (%d)", o.min, o.max)
	}
	if old == nil {
		if o.increment > 0 {
			o.start = o.min
		} else {
			o.start = o.max
		}
	}
	if r, ok := seen["start"]; ok {
		o.start = r.Value
	}
	if err := sequenceBoundOption("START", o.start, o); err != nil {
		return o, nil, err
	}
	if r, ok := seen["cache"]; ok {
		o.cache = r.Value
	}
	if o.cache <= 0 {
		return o, nil, sequenceOptionError("CACHE (%d) must be greater than zero", o.cache)
	}
	if r, ok := seen["cycle"]; ok {
		o.cycle = r.Value != 0
	}
	var restart *int64
	if r, ok := seen["restart"]; ok {
		v := r.Value
		if r.Type.Name == "default" {
			v = o.start
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
	if v < o.min {
		return sequenceOptionError("%s value (%d) cannot be less than MINVALUE (%d)", name, v, o.min)
	}
	if v > o.max {
		return sequenceOptionError("%s value (%d) cannot be greater than MAXVALUE (%d)", name, v, o.max)
	}
	return nil
}

// Ordered unsigned distance avoids overflowing either signed subtraction or
// multiplication at INT64 endpoints, including increment=INT64_MIN.
func sequenceDistance(a, b int64) uint64 {
	return (uint64(b) ^ uint64(1<<63)) - (uint64(a) ^ uint64(1<<63))
}
func sequenceStep(o sequenceOptions, v int64) (int64, bool) {
	if o.increment > 0 {
		if sequenceDistance(v, o.max) < uint64(o.increment) {
			return o.min, false
		}
		return v + o.increment, true
	}
	step := uint64(-(o.increment + 1)) + 1
	if sequenceDistance(o.min, v) < step {
		return o.max, false
	}
	return v + o.increment, true
}
func reserveSequence(o sequenceOptions, g *sequenceGenerator, name string) (int64, uint64, error) {
	first := g.last
	if g.called {
		v, ok := sequenceStep(o, first)
		if !ok && !o.cycle {
			which, bound := "maximum", o.max
			if o.increment < 0 {
				which, bound = "minimum", o.min
			}
			return 0, 0, sqlError("2200H", fmt.Sprintf(`nextval: reached %s value of sequence "%s" (%d)`, which, name, bound))
		}
		first = v
	}
	var distance, step uint64
	if o.increment > 0 {
		distance, step = sequenceDistance(first, o.max), uint64(o.increment)
	} else {
		distance, step = sequenceDistance(o.min, first), uint64(-(o.increment+1))+1
	}
	// Cache may end at a bound; wrapping is deferred to the next reservation.
	count := uint64(o.cache)
	available := distance / step
	if count-1 > available {
		count = available + 1
	}
	delta := (count - 1) * step
	last := int64(uint64(first) + delta)
	if o.increment < 0 {
		last = int64(uint64(first) - delta)
	}
	g.last, g.called = last, true
	return first, count, nil
}
