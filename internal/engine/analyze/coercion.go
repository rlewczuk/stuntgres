package analyze

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// CoercionProperties derives a coercion's properties from its child and plan:
// domain checks are runtime conversions and session-dependent plans are stable.
func CoercionProperties(child expr.Expr, plan sqltype.CoercionPlan) expr.Properties {
	properties := child.Properties()
	properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || plan.ChecksDomain()
	if plan.SessionDependent() && properties.Volatility < expr.Stable {
		properties.Volatility = expr.Stable
	}
	return properties
}

// DomainCoercionInput strips the COLLATE annotations above child when it is
// coerced to a collatable domain, returning the stripped input and the
// original annotated expression (nil when nothing was stripped).
func DomainCoercionInput(child expr.Expr, target sqltype.Usage) (expr.Expr, expr.Expr) {
	if !target.Type.IsDomain() || target.Collation.ID == 0 || child.ResultUsage().Type == target.Type {
		return child, nil
	}
	annotations := child
	for {
		annotation, ok := child.(plan.CollationExpr)
		if !ok {
			return child, annotations
		}
		child = annotation.Child
	}
}

// RestoreDomainCastCollations re-applies the annotations stripped by
// DomainCoercionInput above the coerced expression.
func RestoreDomainCastCollations(original, coerced expr.Expr) expr.Expr {
	if coerced == nil {
		return nil
	}
	annotation, ok := original.(plan.CollationExpr)
	if !ok {
		return coerced
	}
	ref := annotation.Usage.Collation
	annotation.Child = RestoreDomainCastCollations(annotation.Child, coerced)
	annotation.Usage = coerced.ResultUsage()
	annotation.Usage.Collation = ref
	annotation.Property = coerced.Properties()
	return annotation
}

// StorageDestination coerces node to the exact storage target Usage.
// Storage consumes an expression's value, not its result-collation derivation.
// Keep semantic domain boundaries and annotations inside this final assignment
// while publishing the exact immutable destination Usage.
func StorageDestination(node expr.Expr, target sqltype.Usage, pos parser.Position) (expr.Expr, error) {
	if node.ResultUsage() == target {
		return node, nil
	}
	coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), target, sqltype.Assignment)
	if err != nil {
		return nil, err
	}
	info := expr.Info{Usage: target, Pos: pos, Property: CoercionProperties(node, coercionPlan)}
	return plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Assignment}, nil
}

// StorageCoercionInfo is the Info of a storage coercion of child.
// Only a new domain conversion adds semantic authority before storage. Other
// assignments already retain their semantic expression as the child and can
// convert directly to the physical destination, without an intermediate relabel.
func StorageCoercionInfo(view catalog.Read, info expr.Info, child expr.Expr) (expr.Info, error) {
	if info.Usage.Type.IsDomain() && child.ResultUsage().Type != info.Usage.Type {
		return CoercionInfo(view, info, child)
	}
	return info, nil
}

// Implicit default yields to nondefault identities; differing nondefault
// identities remain indeterminate until an explicit annotation supplies authority.
func likeCollation(a, b sqltype.Usage) (sqltype.CollationRef, bool) {
	x, y := a.Collation, b.Collation
	if x.ID == 0 {
		return y, false
	}
	if y.ID == 0 || x.ID == y.ID {
		return x, false
	}
	if x.ID == 100 {
		return y, false
	}
	if y.ID == 100 {
		return x, false
	}
	return sqltype.CollationRef{ID: 100, Derivation: sqltype.Default}, true
}

// CoercionInfo derives a coercion's collation from its child.
// Coercion inherits its child's authority except CoerceToDomain to a declared
// nondefault collation. Targeting coercions move top-level COLLATE wrappers
// outside their domain boundary; nested explicit authority is replaced here.
func CoercionInfo(view catalog.Read, info expr.Info, child expr.Expr) (expr.Info, error) {
	info.CollationExplicit = parser.Position{}
	info.CollationConflict = false
	info.CollationLeft, info.CollationRight, info.CollationConflictPos = sqltype.CollationRef{}, sqltype.CollationRef{}, parser.Position{}
	if info.Usage.Collation.ID == 0 {
		return info, nil
	}
	if info.Usage.Type.IsDomain() && child.ResultUsage().Type != info.Usage.Type {
		declared := info.Usage.Type.Usage().Collation
		if declared.ID != 0 && declared.ID != 100 {
			info.Usage.Collation = declared
			return info, nil
		}
	}
	if source := child.ResultUsage().Collation; source.ID != 0 {
		info.Usage.Collation = source
	}
	return CollatedInfo(view, info, child)
}

// CollatedInfo derives the result collation of a collatable wrapper from its
// inputs. Collation identity is valid Usage metadata; indeterminacy is expression
// state. Preserve both across collatable wrappers without inventing an invalid
// stored collation or evaluating branches merely to choose their collation.
func CollatedInfo(view catalog.Read, info expr.Info, inputs ...expr.Expr) (expr.Info, error) {
	if info.Usage.Collation.ID == 0 {
		return info, nil
	}
	for _, input := range inputs {
		u, explicit := input.ResultUsage(), input.ExplicitCollation()
		if u.Collation.ID == 0 {
			continue
		}
		if explicit.Character > 0 {
			if info.CollationExplicit.Character > 0 && info.Usage.Collation.ID != u.Collation.ID {
				return info, expr.ErrorAt("42P21", fmt.Sprintf(`collation mismatch between explicit collations "%s" and "%s"`, typedef.CollationName(view, info.Usage.Collation.ID), typedef.CollationName(view, u.Collation.ID)), explicit)
			}
			info.Usage.Collation = u.Collation
			if info.CollationExplicit.Character == 0 {
				info.CollationExplicit = explicit
			}
			info.CollationConflict = false
			info.CollationLeft, info.CollationRight, info.CollationConflictPos = sqltype.CollationRef{}, sqltype.CollationRef{}, parser.Position{}
		} else if info.CollationExplicit.Character == 0 {
			selected, conflict := likeCollation(info.Usage, u)
			indeterminate := input.CollationIndeterminate()
			if !info.CollationConflict {
				if indeterminate {
					info.CollationLeft, info.CollationRight, info.CollationConflictPos = input.ImplicitCollationConflict()
				} else if conflict {
					info.CollationLeft, info.CollationRight, info.CollationConflictPos = info.Usage.Collation, u.Collation, input.SourcePosition()
				}
			}
			info.Usage.Collation = selected
			info.CollationConflict = info.CollationConflict || conflict || indeterminate
		}
	}
	return info, nil
}

// ComparisonOperator maps a comparison operator spelling to its sqltype
// operator.
func ComparisonOperator(name string) (sqltype.ComparisonOperator, bool) {
	switch name {
	case "=":
		return sqltype.CompareEqual, true
	case "<>", "!=":
		return sqltype.CompareNotEqual, true
	case "<":
		return sqltype.CompareLess, true
	case "<=":
		return sqltype.CompareLessEqual, true
	case ">":
		return sqltype.CompareGreater, true
	case ">=":
		return sqltype.CompareGreaterEqual, true
	}
	return 0, false
}
