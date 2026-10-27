package stored

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func ValidateRules(view catalog.Read, d *catalog.Relation) error {
	if d.Rules == nil {
		return nil
	}
	return d.Rules.Validate(view, d)
}

// validateRuleList checks rules owned by d.
func validateRuleList(view catalog.Read, d *catalog.Relation, rules []Rule) error {
	previous := ""
	for i, r := range rules {
		if r.OID == 0 || r.Name == "" || i != 0 && strings.Compare(previous, r.Name) >= 0 {
			return fmt.Errorf("stuntgres: invalid rule identity/order")
		}
		previous = r.Name
		if !r.mode.Valid() {
			return fmt.Errorf("stuntgres: invalid rule enable mode")
		}
		if r.event != "INSERT" && r.event != "UPDATE" && r.event != "DELETE" {
			return fmt.Errorf("stuntgres: invalid ordinary rule event")
		}
		if r.condition != nil && (r.condition.ResultUsage().Type == nil || r.condition.ResultUsage().Type.ID() != sqltype.Bool || r.conditionPresentation == nil) {
			return fmt.Errorf("stuntgres: invalid rule qualification")
		}
		providers := 0
		for _, action := range r.actions {
			if RuleEvent(action.syntax) == "" || action.binding.Parameters.Count != 0 {
				return fmt.Errorf("stuntgres: invalid rule action")
			}
			if action.binding.Returning != nil {
				providers++
				if !r.instead || r.condition != nil || providers > 1 {
					return fmt.Errorf("stuntgres: invalid rule RETURNING contract")
				}
			}
			if action.binding.Ref != (ids.ObjectRef{}) {
				address := action.binding.Ref.Address
				provider, ok := view.Relation(address)
				if !ok || !ReferenceEpochValid(view, d, action.binding.Ref) || provider == nil {
					return fmt.Errorf("stuntgres: invalid rule action target")
				}
			}
		}
		for _, address := range r.references {
			whole := address
			whole.SubID = 0
			provider := view.Get(whole)
			if address.ClassID == 2606 && address.SubID == 0 {
				parent := ConstraintParent(view, address)
				if parent == nil {
					return fmt.Errorf("stuntgres: missing rule dependency")
				}
				provider = parent
			}
			if provider == nil {
				return fmt.Errorf("stuntgres: missing rule dependency")
			}
			if address.SubID != 0 {
				relation, ok := provider.(*catalog.Relation)
				if !ok || !relation.HasSupportedAttribute(address.SubID) {
					return fmt.Errorf("stuntgres: missing rule dependency attribute")
				}
			}
		}
	}
	return nil
}
