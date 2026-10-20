package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// NeedsTypeDiagnosticInput reports whether diagnostics for typ need captured
// catalog input: user-defined, domain or enum types, or containers of them.
func NeedsTypeDiagnosticInput(typ *sqltype.Descriptor) bool {
	if typ == nil {
		return false
	}
	if typ.IsUserDefined() || typ.HasDomainConstraints() {
		return true
	}
	typ = typ.BaseType()
	if typ.IsEnum() {
		return true
	}
	if typ.IsArray() {
		return NeedsTypeDiagnosticInput(typ.Element())
	}
	if typ.IsComposite() {
		for i := range typ.CompositeLen() {
			if NeedsTypeDiagnosticInput(typ.CompositeMember(i).Usage.Type) {
				return true
			}
		}
	}
	return false
}

// Retained reads and writes have identical input demands, independent of the
// tree walker used by their consumer. Naming never suppresses catalog input.
func CaptureRetainedExpressionInput(node expr.Expr, element func() error, diagnostic func(*sqltype.Descriptor) error) error {
	// Stored columns and record constructors can demand a named nested provider
	// without any input conversion. Retain the same naming authority as fresh
	// comparison binding, including domain and array identity.
	if NeedsTypeDiagnosticInput(node.ResultUsage().Type) {
		if err := diagnostic(node.ResultUsage().Type); err != nil {
			return err
		}
	}
	switch node := node.(type) {
	case functions.Enum:
		return diagnostic(node.Plan.Enum())
	case functions.JSONBRecord:
		targets := [2]*sqltype.Descriptor{node.RecordUsage().Type}
		if len(node.Args) == 2 {
			targets[1] = node.Args[0].ResultUsage().Type
		}
		for i, target := range targets {
			if target == nil || i == 1 && target == targets[0] {
				continue
			}
			if target.ContainsCatalogIdentifier() {
				if err := element(); err != nil {
					return err
				}
			}
			if err := diagnostic(target); err != nil {
				return err
			}
		}
	case plan.CoercionExpr:
		target := node.ResultUsage().Type
		characterInput := sqltype.IsCharacter(node.Child.ResultUsage().Type.BaseType().ID())
		if characterInput || sqltype.ContainsDomainConstraints(target) {
			if characterInput && target.ContainsCatalogIdentifier() {
				if err := element(); err != nil {
					return err
				}
			}
			return diagnostic(target)
		}
	}
	return nil
}
