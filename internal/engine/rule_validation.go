package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func validateRules(view CatalogRead, d *relationDescriptor) error {
	if d.Rules == nil {
		return nil
	}
	return d.Rules.Validate(view, d)
}

// validateRuleList checks rules owned by d.
func validateRuleList(view CatalogRead, d *relationDescriptor, rules []ruleDefinition) error {
	previous := ""
	for i, r := range rules {
		if r.OID == 0 || r.Name == "" || i != 0 && strings.Compare(previous, r.Name) >= 0 {
			return fmt.Errorf("stuntgres: invalid rule identity/order")
		}
		previous = r.Name
		if r.event != "INSERT" && r.event != "UPDATE" && r.event != "DELETE" {
			return fmt.Errorf("stuntgres: invalid ordinary rule event")
		}
		if r.condition != nil && (r.condition.ResultUsage().Type == nil || r.condition.ResultUsage().Type.ID() != sqltype.Bool || r.conditionPresentation == nil) {
			return fmt.Errorf("stuntgres: invalid rule qualification")
		}
		providers := 0
		for _, action := range r.actions {
			if ruleEvent(action.syntax) == "" || action.binding.parameters.Count != 0 {
				return fmt.Errorf("stuntgres: invalid rule action")
			}
			if action.binding.returning != nil {
				providers++
				if !r.instead || r.condition != nil || providers > 1 {
					return fmt.Errorf("stuntgres: invalid rule RETURNING contract")
				}
			}
			if action.binding.ref != (ids.ObjectRef{}) {
				address := action.binding.ref.Address
				provider, ok := view.Relation(address)
				if !ok || !storedReferenceEpochValid(view, d, action.binding.ref) || provider == nil {
					return fmt.Errorf("stuntgres: invalid rule action target")
				}
			}
		}
		for _, address := range r.references {
			whole := address
			whole.SubID = 0
			provider := view.Get(whole)
			if address.ClassID == 2606 && address.SubID == 0 {
				parent := constraintParent(view, address)
				if parent == nil {
					return fmt.Errorf("stuntgres: missing rule dependency")
				}
				provider = parent
			}
			if provider == nil {
				return fmt.Errorf("stuntgres: missing rule dependency")
			}
			if address.SubID != 0 && address.SubID != -6 {
				relation, ok := provider.(*relationDescriptor)
				if !ok || !relation.hasAttribute(address.SubID) {
					return fmt.Errorf("stuntgres: missing rule dependency attribute")
				}
			}
		}
	}
	return nil
}
