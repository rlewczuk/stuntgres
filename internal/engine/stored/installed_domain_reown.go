package stored

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// installedDomainCheckRecipes recognizes only closed, native provider recipes.
// User CHECKs/defaults and SQL routine bodies are consumers, never bootstrap work.
func installedDomainCheckRecipes(d *catalog.Type) bool {
	if d.Extension == (ids.ObjectAddress{}) || !d.SQLType.IsDomain() || len(d.DomainChecks) == 0 {
		return false
	}
	for _, check := range d.DomainChecks {
		if _, ok := check.Expression.(plan.EarthCheckExpr); !ok {
			return false
		}
	}
	return true
}

func reownExpressionReference(view catalog.Read, next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress, typesOnly bool, old ids.ObjectRef) ids.ObjectRef {
	if old == (ids.ObjectRef{}) || typesOnly {
		return old
	}
	address := old.Address
	address.SubID = 0
	if target, ok := mapping[address]; ok {
		target.SubID = old.Address.SubID
		old.Address = target
	}
	return view.Owner().Domain.Ref(next.Epoch(), old.Address)
}

// ReownInstalledDomainChecks establishes the actual installed CHECK generation
// before any dependent routine/body/default/relation is retagged. This phase
// does not admit the installation: all ordinary full provider validators remain
// in force. Its closed recipes contain only lexical VALUE, not callable bodies.
// The same operation memo supplies type/collation authority to later consumers.
func ReownInstalledDomainChecks(view catalog.Read, next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress, typesOnly bool, remap *catalog.RoutineRemap) error {
	for address, descriptor := range next.Descriptors() {
		d, ok := descriptor.(*catalog.Type)
		if !ok || !installedDomainCheckRecipes(d) {
			continue
		}
		// External callers bootstrap before declaration rebinding, then enter
		// ReownExpressions with this same memo. Do not map destination refs or
		// collision-chain IDs a second time on that second entry.
		current := true
		boolean, _ := view.LookupType(sqltype.Bool)
		for _, check := range d.DomainChecks {
			n := check.Expression.(plan.EarthCheckExpr)
			current = current && n.Valid() && n.Value.Usage == d.SQLType.DomainBase() && n.Usage.Type == boolean
			if !typesOnly {
				for _, dependency := range check.Dependencies {
					current = current && dependency.Ref.Domain() == view.Owner().Domain && dependency.Ref.Epoch == next.Epoch()
				}
			}
		}
		if current {
			continue
		}
		ref := func(old ids.ObjectRef) ids.ObjectRef {
			return reownExpressionReference(view, next, mapping, typesOnly, old)
		}
		retag := func(node expr.Expr) (expr.Expr, bool, error) {
			return RetagTypes(view, node, remap.Type, remap.Collation)
		}
		checks, err := reownDomainCheckDefinitions(view, d.DomainChecks, ref, retag)
		if err != nil {
			return err
		}
		copy := *d
		copy.DomainChecks = checks
		next.SetDescriptor(address, &copy)
	}
	return nil
}

func reownDomainCheckDefinitions(view catalog.Read, original []catalog.DomainCheckConstraint, ref func(ids.ObjectRef) ids.ObjectRef, retag func(expr.Expr) (expr.Expr, bool, error)) ([]catalog.DomainCheckConstraint, error) {
	checks := append([]catalog.DomainCheckConstraint(nil), original...)
	for i := range checks {
		check := &checks[i]
		node, err := RemapExpression(check.Expression, ref, nil)
		if err != nil {
			return nil, err
		}
		node, _, err = retag(node)
		if err != nil {
			return nil, err
		}
		check.Expression = node
		check.Dependencies = append([]catalog.ExpressionDependency(nil), check.Dependencies...)
		for j := range check.Dependencies {
			check.Dependencies[j].Ref = ref(check.Dependencies[j].Ref)
			address := check.Dependencies[j].Ref.Address
			address.SubID = 0
			if provider, exists := view.Descriptor(address); exists && address.ClassID != ids.ProcedureClassID {
				check.Dependencies[j].Revision = provider.Revision()
			}
		}
	}
	return checks, nil
}
