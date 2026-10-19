package functions

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// CallTypeContext is deliberately smaller than Binder: candidate applicability
// may consult type ownership, but cannot bind, infer a parameter, coerce a value,
// capture a dependency or construct an executable provider.
type CallTypeContext interface {
	Type(sqltype.ID) *sqltype.Descriptor
	ArrayType(*sqltype.Descriptor) (*sqltype.Descriptor, bool)
	CommonUsage([]sqltype.Usage) sqltype.Usage
}

// CallShape tests one already-checked declaration against analyzed arguments.
// Declared inputs are expanded for positional variadic/default call shapes by
// the caller, but retain pseudo identities for PostgreSQL's ranking rules.
// The selected family still owns all argument coercion and diagnostics.
type CallShape func(CallTypeContext, parser.FunctionExpr, []Argument, []sqltype.Usage, []sqltype.Usage) bool

func knownCallEdges(operands []Argument, targets []sqltype.Usage) bool {
	for i, o := range operands {
		if o.Usage.Type != nil && !o.Usage.CatalogNullUsage() {
			if targets[i].Type == nil {
				return false
			}
			if _, err := sqltype.PlanCoercion(o.Usage, targets[i], sqltype.Implicit); err != nil {
				return false
			}
		}
	}
	return true
}

// compatibleArrayTargets is the existing anycompatible array-family common
// leaf selection, shared by transforms, searches and threshold width_bucket.
func compatibleArrayTargets(b CallTypeContext, operands []Argument, arrays []bool, targets []sqltype.Usage, inheritCollation bool) (sqltype.Usage, bool) {
	var small [3]sqltype.Usage
	known := small[:0]
	for i, o := range operands {
		u := o.Usage
		if arrays[i] {
			var ok bool
			u, ok = ArraySearchLeafUsage(u)
			if !ok {
				return sqltype.Usage{}, false
			}
		}
		if u.Type != nil {
			known = append(known, u)
		}
	}
	common := b.CommonUsage(known)
	if common.Type == nil {
		return sqltype.Usage{}, false
	}
	array, ok := b.ArrayType(common.Type)
	if !ok || !array.Concrete() {
		return sqltype.Usage{}, false
	}
	result := array.Usage()
	if inheritCollation {
		result.Collation = common.Collation
	}
	for i := range operands {
		targets[i] = common
		if arrays[i] {
			targets[i] = result
		}
	}
	return result, knownCallEdges(operands, targets)
}
