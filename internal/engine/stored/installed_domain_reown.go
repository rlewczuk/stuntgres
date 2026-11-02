package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// installedDomainCheckRecipes recognizes only closed, native provider recipes.
// User CHECKs/defaults and SQL routine bodies are consumers, never bootstrap work.
func installedDomainCheckRecipes(d *catalog.Type) bool {
	if !d.Origin.Valid(d.Address()) || !d.SQLType.IsDomain() || len(d.DomainChecks) == 0 {
		return false
	}
	for _, check := range d.DomainChecks {
		if _, ok := check.Expression.(plan.EarthCheckExpr); !ok {
			return false
		}
	}
	return true
}

// Validate the original manifest before ref rewriting can erase its lifetime.
// Import validates against the actual source Read before this phase; a capture
// already owned by this catalog must still name this catalog's domain.
func validateInstalledDomainCheckSource(view catalog.Read, domain *catalog.Type) error {
	var source ids.ObjectRef
	for _, check := range domain.DomainChecks {
		captured, err := exec.ExpressionRoutineDescriptor(check.Expression)
		if err != nil || captured == nil || len(check.Dependencies) == 0 {
			return fmt.Errorf("stuntgres: missing installed CHECK source capture")
		}
		found := false
		for _, dependency := range check.Dependencies {
			ref := dependency.Ref
			if ref.Domain() == nil || ref.Epoch == 0 {
				return fmt.Errorf("stuntgres: invalid installed CHECK source lifetime")
			}
			if ref.Domain() == view.Domain() && ref.Epoch > view.Epoch() {
				return fmt.Errorf("stuntgres: future installed CHECK source epoch")
			}
			if source == (ids.ObjectRef{}) {
				source = ref
			} else if ref.Domain() != source.Domain() || ref.Epoch != source.Epoch {
				return fmt.Errorf("stuntgres: incoherent installed CHECK source lifetime")
			}
			if ref.Address != captured.Address() {
				continue
			}
			if dependency.Revision != captured.Revision() || dependency.Purpose != catalog.DefinitionDependency {
				return fmt.Errorf("stuntgres: invalid installed CHECK source declaration")
			}
			if view.Owner() != nil && view.Owner().Principals != nil && captured.Owner == view.Owner().Principals.Principal(captured.Owner.ID()) && ref.Domain() != view.Owner().Domain {
				return fmt.Errorf("stuntgres: foreign installed CHECK source lifetime")
			}
			found = true
		}
		if !found {
			return fmt.Errorf("stuntgres: missing installed CHECK source declaration")
		}
	}
	return nil
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
// in force. Closed Earth kernels retain their lexical VALUE and genuine cube
// helper declaration through the same original-procedure/type/collation memo.
func ReownInstalledDomainChecks(view catalog.Read, next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress, typesOnly bool, remap *catalog.RoutineRemap) error {
	var procedure catalog.ProcedureRemapper
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
		if current && remap.RefreshDependency == nil {
			if err := ValidateDomainCheckDefinitions(view, d); err == nil {
				continue
			}
		}
		if err := validateInstalledDomainCheckSource(view, d); err != nil {
			return err
		}
		if procedure == nil {
			procedure = originalProcedureMapper(view, mapping, remap.Procedure)
		}
		ref := func(old ids.ObjectRef) ids.ObjectRef {
			return reownExpressionReference(view, next, mapping, typesOnly, old)
		}
		retag := func(node expr.Expr) (expr.Expr, bool, error) {
			return mapExpressionWithProcedure(view, nil, node, remap.Type, nil, nil, false, nil, procedure, nil, remap.Collation)
		}
		checks, err := reownDomainCheckDefinitions(view, d.DomainChecks, ref, retag, dependencyRefresh(remap.RefreshDependency))
		if err != nil {
			return err
		}
		copy := *d
		copy.DomainChecks = checks
		if err := ValidateDomainCheckDefinitions(view, &copy); err != nil {
			return err
		}
		next.SetDescriptor(address, &copy)
	}
	return nil
}

func reownDomainCheckDefinitions(view catalog.Read, original []catalog.DomainCheckConstraint, ref func(ids.ObjectRef) ids.ObjectRef, retag func(expr.Expr) (expr.Expr, bool, error), refresh dependencyRefresh) ([]catalog.DomainCheckConstraint, error) {
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
		if err := refresh.apply(check.Dependencies); err != nil {
			return nil, err
		}
	}
	return checks, nil
}
