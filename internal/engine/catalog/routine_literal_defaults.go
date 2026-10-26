package catalog

import "stuntgres/internal/engine/ids"

// RoutineLiteral is source expression metadata, not an executable or rendered
// SQL fragment. SourceType may differ from the input type through assignment
// coercion (e.g. numeric 0.0 assigned to make_interval's double precision).
type RoutineLiteral struct {
	SourceType ids.OID
	Text       string
	Null       bool
}

// RoutineLiteralDefaults owns immutable trailing-input recipes. No catalog-bound
// descriptor, datum, caller slice or cached presentation is retained.
type RoutineLiteralDefaults struct{ literals []RoutineLiteral }

func NewRoutineLiteralDefaults(literals ...RoutineLiteral) *RoutineLiteralDefaults {
	return &RoutineLiteralDefaults{literals: append([]RoutineLiteral(nil), literals...)}
}
func (d *RoutineLiteralDefaults) Len() int {
	if d == nil {
		return 0
	}
	return len(d.literals)
}
func (d *RoutineLiteralDefaults) At(i int) RoutineLiteral { return d.literals[i] }
