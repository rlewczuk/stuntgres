package control

import (
	"fmt"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/stored"
)

// RemapDefinitionOIDs gives every imported definition (domain checks, rules,
// policies, constraints, defaults and generations) an OID no other object of
// the replacement candidate claims. Replacement retains temporary objects and
// imports persistent definitions, so the entire candidate is reserved before
// any collision gets a replacement from allocate.
//
// view reads the private replacement candidate, which is changed in place.
// allocate returns the next unused OID; the caller holds the lock that guards
// it for the whole call.
func RemapDefinitionOIDs(view catalog.Read, allocate func() (ids.OID, error)) error {
	next := view.State()
	reserved := make(map[ids.OID]bool)
	var relations []*catalog.Relation
	var domains []*catalog.Type
	for address, descriptor := range next.Descriptors() {
		reserved[address.ObjectID] = true
		if proc, ok := descriptor.(*catalog.Procedure); ok && proc.Aggregate != nil {
			catalog.ReserveDefinitionOIDs(reserved, proc)
		}
		if domain, ok := descriptor.(*catalog.Type); ok && len(domain.DomainChecks) != 0 {
			domains = append(domains, domain)
			catalog.ReserveDefinitionOIDs(reserved, domain)
		}
		if d, ok := descriptor.(*catalog.Relation); ok {
			relations = append(relations, d)
			catalog.ReserveDefinitionOIDs(reserved, d)
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].Address().ObjectID < relations[j].Address().ObjectID })
	claimed := make(map[ids.OID]bool)
	for address, descriptor := range next.Descriptors() {
		if address.ClassID != ids.LargeObjectClassID {
			claimed[address.ObjectID] = true
		}
		if proc, ok := descriptor.(*catalog.Procedure); ok && proc.Aggregate != nil {
			for _, helper := range proc.Aggregate.Helpers {
				claimed[helper.ID] = true
			}
		}
	}
	for _, d := range relations {
		if d.View != nil {
			claimed[d.View.RuleOID()] = true
		}
		if ns, ok := next.Namespace(d.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) {
			for _, r := range stored.RulesOf(d) {
				claimed[r.OID] = true
			}
			for _, p := range d.Security.Policies {
				claimed[p.OID] = true
			}
			for _, definition := range d.Constraints.NotNull {
				claimed[definition.OID] = true
			}
			for _, definition := range d.Constraints.Check {
				claimed[definition.OID] = true
			}
			for _, definition := range d.Constraints.Unique {
				claimed[definition.OID] = true
			}
			for _, definition := range d.Constraints.Exclusion {
				claimed[definition.OID] = true
			}
			for _, definition := range d.Constraints.ForeignKeys {
				claimed[definition.OID] = true
			}
			for _, definition := range d.ColumnMeta.Defaults {
				claimed[definition.OID] = true
			}
			for _, definition := range d.ColumnMeta.Generations {
				claimed[definition.OID] = true
			}
		}
	}
	remint := func(id ids.OID) (ids.OID, error) {
		if id == 0 {
			return 0, fmt.Errorf("stuntgres: invalid imported definition identity")
		}
		if !claimed[id] {
			claimed[id] = true
			return id, nil
		}
		for {
			candidate, err := allocate()
			if err != nil {
				return 0, err
			}
			if !reserved[candidate] {
				reserved[candidate], claimed[candidate] = true, true
				return candidate, nil
			}
		}
	}
	sort.Slice(domains, func(i, j int) bool { return domains[i].Address().ObjectID < domains[j].Address().ObjectID })
	for _, domain := range domains {
		copy := *domain
		var checks []catalog.DomainCheckConstraint
		for i, check := range domain.DomainChecks {
			id, err := remint(check.OID)
			if err != nil {
				return err
			}
			if id != check.OID {
				if checks == nil {
					checks = append([]catalog.DomainCheckConstraint(nil), domain.DomainChecks...)
				}
				checks[i].OID = id
			}
		}
		if checks != nil {
			copy.DomainChecks = checks
			next.SetDescriptor(domain.Address(), &copy)
		}
	}
	uniqueMapping := make(map[ids.OID]ids.OID)
	var constraintMapping map[ids.ObjectAddress]ids.ObjectAddress
	recordConstraint := func(old, next ids.OID) {
		if old == next {
			return
		}
		if constraintMapping == nil {
			constraintMapping = make(map[ids.ObjectAddress]ids.ObjectAddress)
		}
		constraintMapping[ids.ObjectAddress{ClassID: 2606, ObjectID: old}] = ids.ObjectAddress{ClassID: 2606, ObjectID: next}
	}
	for _, old := range relations {
		if ns, ok := next.Namespace(old.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) {
			continue
		}
		if len(old.Constraints.NotNull)+len(old.Constraints.Check)+len(old.Constraints.Unique)+len(old.Constraints.Exclusion)+len(old.Constraints.ForeignKeys)+len(old.ColumnMeta.Defaults)+len(old.ColumnMeta.Generations)+len(old.Security.Policies)+len(stored.RulesOf(old)) == 0 {
			continue
		}
		d := *old
		changed, nullCopied, checkCopied, defaultCopied := false, false, false, false
		for i, r := range stored.RulesOf(old) {
			id, err := remint(r.OID)
			if err != nil {
				return err
			}
			if id == r.OID {
				continue
			}
			if len(stored.RulesOf(&d)) != 0 && &stored.RulesOf(&d)[0] == &stored.RulesOf(old)[0] {
				d.Rules = stored.RulesPayload(append([]stored.Rule(nil), stored.RulesOf(old)...))
			}
			stored.RulesOf(&d)[i].OID = id
			changed = true
		}
		for i, p := range old.Security.Policies {
			id, err := remint(p.OID)
			if err != nil {
				return err
			}
			if id == p.OID {
				continue
			}
			if len(d.Security.Policies) != 0 && &d.Security.Policies[0] == &old.Security.Policies[0] {
				d.Security.Policies = append([]catalog.PolicyDefinition(nil), old.Security.Policies...)
			}
			d.Security.Policies[i].OID = id
			changed = true
		}
		for i, def := range old.Constraints.NotNull {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if !nullCopied {
				d.Constraints.NotNull = append([]catalog.NotNullConstraint(nil), old.Constraints.NotNull...)
				nullCopied = true
			}
			d.Constraints.NotNull[i].OID = id
			recordConstraint(def.OID, id)
			changed = true
		}
		for i, def := range old.Constraints.Check {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if !checkCopied {
				d.Constraints.Check = append([]catalog.CheckConstraint(nil), old.Constraints.Check...)
				checkCopied = true
			}
			d.Constraints.Check[i].OID = id
			recordConstraint(def.OID, id)
			changed = true
		}
		for i, def := range old.Constraints.Unique {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			uniqueMapping[def.OID] = id
			if id == def.OID {
				continue
			}
			if d.Constraints.Unique == nil || &d.Constraints.Unique[0] == &old.Constraints.Unique[0] {
				d.Constraints.Unique = append([]catalog.UniqueConstraint(nil), old.Constraints.Unique...)
			}
			d.Constraints.Unique[i].OID = id
			recordConstraint(def.OID, id)
			idx, ok := next.Relation(def.Index)
			if !ok || idx.SQLIndex == nil {
				return fmt.Errorf("stuntgres: invalid imported unique index")
			}
			clone := *idx
			definition := *idx.SQLIndex
			constraint := d.Constraints.Unique[i]
			definition.Constraint = index.UniqueBacking(&constraint)
			clone.SQLIndex = &definition
			next.SetDescriptor(clone.Address(), &clone)
			changed = true
		}
		for i, def := range old.Constraints.Exclusion {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if d.Constraints.Exclusion == nil || &d.Constraints.Exclusion[0] == &old.Constraints.Exclusion[0] {
				d.Constraints.Exclusion = append([]catalog.ExclusionConstraint(nil), old.Constraints.Exclusion...)
			}
			d.Constraints.Exclusion[i].OID = id
			recordConstraint(def.OID, id)
			idx, ok := next.Relation(def.Index)
			if !ok || idx.SQLIndex == nil {
				return fmt.Errorf("stuntgres: invalid imported exclusion index")
			}
			clone := *idx
			definition := *idx.SQLIndex
			definition.Constraint = index.ExclusionBacking(&d.Constraints.Exclusion[i])
			clone.SQLIndex = &definition
			next.SetDescriptor(clone.Address(), &clone)
			changed = true
		}
		for i, def := range old.Constraints.ForeignKeys {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if &d.Constraints.ForeignKeys[0] == &old.Constraints.ForeignKeys[0] {
				d.Constraints.ForeignKeys = append([]catalog.ForeignKeyConstraint(nil), old.Constraints.ForeignKeys...)
			}
			d.Constraints.ForeignKeys[i].OID = id
			recordConstraint(def.OID, id)
			changed = true
		}
		for attr, def := range old.ColumnMeta.Defaults {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if !defaultCopied {
				d.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault, len(old.ColumnMeta.Defaults))
				for key, value := range old.ColumnMeta.Defaults {
					d.ColumnMeta.Defaults[key] = value
				}
				defaultCopied = true
			}
			def.OID = id
			d.ColumnMeta.Defaults[attr] = def
			changed = true
		}
		generationCopied := false
		for attr, definition := range old.ColumnMeta.Generations {
			id, err := remint(definition.OID)
			if err != nil {
				return err
			}
			if id == definition.OID {
				continue
			}
			if !generationCopied {
				d.ColumnMeta.Generations = make(map[int32]catalog.ColumnGeneration, len(old.ColumnMeta.Generations))
				for key, value := range old.ColumnMeta.Generations {
					d.ColumnMeta.Generations[key] = value
				}
				generationCopied = true
			}
			definition.OID = id
			d.ColumnMeta.Generations[attr] = definition
			changed = true
		}
		if changed {
			replacement := d
			next.SetDescriptor(d.Address(), &replacement)
		}
	}
	for _, descriptor := range next.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || len(table.Constraints.Unique) == 0 && len(table.Constraints.Exclusion) == 0 {
			continue
		}
		var changed *catalog.Relation
		for i, constraint := range table.Constraints.Unique {
			parent, exists := uniqueMapping[constraint.Parent]
			if !exists || parent == constraint.Parent {
				continue
			}
			if changed == nil {
				copy := *table
				copy.Constraints.Unique = append([]catalog.UniqueConstraint(nil), table.Constraints.Unique...)
				changed = &copy
			}
			changed.Constraints.Unique[i].Parent = parent
		}
		for i, constraint := range table.Constraints.Exclusion {
			parent, exists := constraintMapping[ids.ObjectAddress{ClassID: 2606, ObjectID: constraint.Parent}]
			if !exists || parent.ObjectID == constraint.Parent {
				continue
			}
			if changed == nil {
				copy := *table
				changed = &copy
			}
			if changed.Constraints.Exclusion == nil || &changed.Constraints.Exclusion[0] == &table.Constraints.Exclusion[0] {
				changed.Constraints.Exclusion = append([]catalog.ExclusionConstraint(nil), table.Constraints.Exclusion...)
			}
			changed.Constraints.Exclusion[i].Parent = parent.ObjectID
		}
		if changed != nil {
			next.SetDescriptor(changed.Address(), changed)
		}
	}
	if len(constraintMapping) != 0 {
		for address, descriptor := range next.Descriptors() {
			parent, ok := descriptor.(*catalog.Relation)
			if !ok || len(stored.RulesOf(parent)) == 0 {
				continue
			}
			if namespace, ok := next.Namespace(parent.Namespace()); ok && namespace.Temporary != (ids.SessionToken{}) {
				continue
			}
			remapped, err := stored.RemapRuleConstraintIdentities(view, parent, constraintMapping)
			if err != nil {
				return err
			}
			if remapped != parent {
				next.SetDescriptor(address, remapped)
			}
		}
	}
	return nil
}
