package engine

import (
	"fmt"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func (e *Engine) remapCatalogExpressions(next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
	return ReownExpressions(e.catalog.read(next), next, mapping, false)
}

// Imported rules have identities outside the descriptor address map. Reserve
// them before allocating replacements, including collisions with retained TEMP
// objects and relation identities remapped earlier in the private candidate.
func (e *Engine) remapViewRuleOIDs(next *catalog.State) error {
	const descriptorOID, ruleOID, claimedOID = uint8(1), uint8(2), uint8(4)
	reserved := make(map[ids.OID]uint8, next.DescriptorCount())
	var views []*catalog.Relation
	for address, descriptor := range next.Descriptors() {
		reserved[address.ObjectID] |= descriptorOID
		if view, ok := descriptor.(*catalog.Relation); ok && view.View != nil {
			if stored.ViewOf(view).RuleOID() == 0 {
				return fmt.Errorf("stuntgres: invalid imported view rule identity")
			}
			reserved[stored.ViewOf(view).RuleOID()] |= ruleOID
			views = append(views, view)
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Address().ObjectID < views[j].Address().ObjectID })
	for _, view := range views {
		rule := stored.ViewOf(view).RuleOID()
		if reserved[rule]&(descriptorOID|claimedOID) != 0 {
			for {
				candidate, err := e.catalog.allocateOIDLocked()
				if err != nil {
					return err
				}
				if reserved[candidate] != 0 {
					continue
				}
				rule = candidate
				break
			}
			replacement := *view
			replacement.View = stored.ViewOf(view).With(func(p *stored.ViewParts) { p.Rule = rule })
			next.SetDescriptor(view.Address(), &replacement)
		}
		reserved[rule] |= ruleOID | claimedOID
	}
	return e.remapDefinitionOIDsLocked(next)
}
