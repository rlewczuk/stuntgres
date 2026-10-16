package engine

import (
	"fmt"
	"sort"
)

// structuralNull only recognizes transparent coercions of a literal NULL.
// Other expressions remain definitions even when evaluation would return NULL.
func structuralNull(node BoundExpr) bool {
	for {
		switch n := node.(type) {
		case literalExpression:
			return n.value.IsNull()
		case collationExpression:
			node = n.child
		case coercionExpression:
			node = n.child
		default:
			return false
		}
	}
}

func (e *Engine) assignDefinitionOIDsLocked(d *relationDescriptor) error {
	for i := range d.notNullConstraints {
		if d.notNullConstraints[i].OID == 0 {
			id, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			d.notNullConstraints[i].OID = id
		}
	}
	for _, attr := range d.attributes {
		def, ok := d.defaults[attr]
		if ok && def.OID == 0 {
			id, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			def.OID = id
			d.defaults[attr] = def
		}
	}
	for _, attr := range d.attributes {
		definition, exists := d.generations[attr]
		if exists && definition.OID == 0 {
			id, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			definition.OID = id
			d.generations[attr] = definition
		}
	}
	for i := range d.checkConstraints {
		if d.checkConstraints[i].OID == 0 {
			id, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			d.checkConstraints[i].OID = id
		}
	}
	for i := range d.uniqueConstraints {
		if d.uniqueConstraints[i].OID == 0 {
			id, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			d.uniqueConstraints[i].OID = id
		}
	}
	for i := range d.foreignKeys {
		if d.foreignKeys[i].OID == 0 {
			id, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			d.foreignKeys[i].OID = id
		}
	}
	return nil
}

func definitionOIDUsed(descriptor Descriptor, id OID) bool {
	d, relation := descriptor.(*relationDescriptor)
	if !relation {
		if proc, ok := descriptor.(*procedureDescriptor); ok && proc.aggregate != nil {
			for _, helper := range proc.aggregate.helpers {
				if helper.id == id {
					return true
				}
			}
		}
		if domain, ok := descriptor.(*typeDescriptor); ok {
			for _, check := range domain.domainChecks {
				if check.OID == id {
					return true
				}
			}
		}
		return false
	}
	if d.view != nil && d.view.rule == id {
		return true
	}
	for _, r := range d.rules {
		if r.OID == id {
			return true
		}
	}
	for _, p := range d.policies {
		if p.OID == id {
			return true
		}
	}
	for _, c := range d.notNullConstraints {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.checkConstraints {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.uniqueConstraints {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.exclusionConstraints {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.foreignKeys {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.defaults {
		if c.OID == id {
			return true
		}
	}
	for _, definition := range d.generations {
		if definition.OID == id {
			return true
		}
	}
	return false
}

// Descriptor and owned-definition OIDs share one identity space. Replacement
// must reserve both before allocating any collision replacement.
func reserveDefinitionOIDs(reserved map[OID]bool, descriptor Descriptor) {
	d, relation := descriptor.(*relationDescriptor)
	if !relation {
		if proc, ok := descriptor.(*procedureDescriptor); ok && proc.aggregate != nil {
			for _, helper := range proc.aggregate.helpers {
				reserved[helper.id] = true
			}
		}
		if domain, ok := descriptor.(*typeDescriptor); ok {
			for _, check := range domain.domainChecks {
				reserved[check.OID] = true
			}
		}
		return
	}
	if d.view != nil {
		reserved[d.view.rule] = true
	}
	for _, r := range d.rules {
		reserved[r.OID] = true
	}
	for _, p := range d.policies {
		reserved[p.OID] = true
	}
	for _, c := range d.notNullConstraints {
		reserved[c.OID] = true
	}
	for _, c := range d.checkConstraints {
		reserved[c.OID] = true
	}
	for _, c := range d.uniqueConstraints {
		reserved[c.OID] = true
	}
	for _, c := range d.exclusionConstraints {
		reserved[c.OID] = true
	}
	for _, c := range d.foreignKeys {
		reserved[c.OID] = true
	}
	for _, c := range d.defaults {
		reserved[c.OID] = true
	}
	for _, c := range d.generations {
		reserved[c.OID] = true
	}
}

func validateDefinitionOIDs(view CatalogRead) error {
	used := make(map[OID]bool)
	for address := range view.state.descriptors {
		used[address.ObjectID] = true
	}
	claim := func(id OID) error {
		if id == 0 || used[id] {
			return fmt.Errorf("stuntgres: invalid stored definition identity")
		}
		used[id] = true
		return nil
	}
	for _, descriptor := range view.state.descriptors {
		if proc, ok := descriptor.(*procedureDescriptor); ok && proc.aggregate != nil {
			for _, helper := range proc.aggregate.helpers {
				if err := claim(helper.id); err != nil {
					return err
				}
			}
		}
		if domain, ok := descriptor.(*typeDescriptor); ok {
			for _, check := range domain.domainChecks {
				if err := claim(check.OID); err != nil {
					return err
				}
			}
		}
		d, ok := descriptor.(*relationDescriptor)
		if !ok {
			continue
		}
		if d.view != nil {
			if err := claim(d.view.rule); err != nil {
				return err
			}
		}
		for _, r := range d.rules {
			if err := claim(r.OID); err != nil {
				return err
			}
		}
		for _, p := range d.policies {
			if err := claim(p.OID); err != nil {
				return err
			}
		}
		for _, c := range d.notNullConstraints {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.checkConstraints {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.uniqueConstraints {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.exclusionConstraints {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.foreignKeys {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.defaults {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, definition := range d.generations {
			if err := claim(definition.OID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Replacement retains temporary objects and imports persistent definitions.
// Reserve the entire candidate before allocating collision replacements.
func (e *Engine) remapDefinitionOIDsLocked(next *catalogState) error {
	reserved := make(map[OID]bool)
	var relations []*relationDescriptor
	var domains []*typeDescriptor
	for address, descriptor := range next.descriptors {
		reserved[address.ObjectID] = true
		if proc, ok := descriptor.(*procedureDescriptor); ok && proc.aggregate != nil {
			reserveDefinitionOIDs(reserved, proc)
		}
		if domain, ok := descriptor.(*typeDescriptor); ok && len(domain.domainChecks) != 0 {
			domains = append(domains, domain)
			reserveDefinitionOIDs(reserved, domain)
		}
		if d, ok := descriptor.(*relationDescriptor); ok {
			relations = append(relations, d)
			reserveDefinitionOIDs(reserved, d)
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].address.ObjectID < relations[j].address.ObjectID })
	claimed := make(map[OID]bool)
	for address, descriptor := range next.descriptors {
		claimed[address.ObjectID] = true
		if proc, ok := descriptor.(*procedureDescriptor); ok && proc.aggregate != nil {
			for _, helper := range proc.aggregate.helpers {
				claimed[helper.id] = true
			}
		}
	}
	for _, d := range relations {
		if d.view != nil {
			claimed[d.view.rule] = true
		}
		if ns, ok := next.descriptors[d.namespace].(*namespaceDescriptor); ok && ns.temporary != (SessionToken{}) {
			for _, r := range d.rules {
				claimed[r.OID] = true
			}
			for _, p := range d.policies {
				claimed[p.OID] = true
			}
			for _, definition := range d.notNullConstraints {
				claimed[definition.OID] = true
			}
			for _, definition := range d.checkConstraints {
				claimed[definition.OID] = true
			}
			for _, definition := range d.uniqueConstraints {
				claimed[definition.OID] = true
			}
			for _, definition := range d.exclusionConstraints {
				claimed[definition.OID] = true
			}
			for _, definition := range d.foreignKeys {
				claimed[definition.OID] = true
			}
			for _, definition := range d.defaults {
				claimed[definition.OID] = true
			}
			for _, definition := range d.generations {
				claimed[definition.OID] = true
			}
		}
	}
	remint := func(id OID) (OID, error) {
		if id == 0 {
			return 0, fmt.Errorf("stuntgres: invalid imported definition identity")
		}
		if !claimed[id] {
			claimed[id] = true
			return id, nil
		}
		for {
			candidate, err := e.allocateOIDLocked()
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
		var checks []domainCheckConstraint
		for i, check := range domain.domainChecks {
			id, err := remint(check.OID)
			if err != nil {
				return err
			}
			if id != check.OID {
				if checks == nil {
					checks = append([]domainCheckConstraint(nil), domain.domainChecks...)
				}
				checks[i].OID = id
			}
		}
		if checks != nil {
			copy.domainChecks = checks
			next.descriptors[domain.Address()] = &copy
		}
	}
	uniqueMapping := make(map[OID]OID)
	var constraintMapping map[ObjectAddress]ObjectAddress
	recordConstraint := func(old, next OID) {
		if old == next {
			return
		}
		if constraintMapping == nil {
			constraintMapping = make(map[ObjectAddress]ObjectAddress)
		}
		constraintMapping[ObjectAddress{ClassID: 2606, ObjectID: old}] = ObjectAddress{ClassID: 2606, ObjectID: next}
	}
	for _, old := range relations {
		if ns, ok := next.descriptors[old.namespace].(*namespaceDescriptor); ok && ns.temporary != (SessionToken{}) {
			continue
		}
		if len(old.notNullConstraints)+len(old.checkConstraints)+len(old.uniqueConstraints)+len(old.exclusionConstraints)+len(old.foreignKeys)+len(old.defaults)+len(old.generations)+len(old.policies)+len(old.rules) == 0 {
			continue
		}
		d := *old
		changed, nullCopied, checkCopied, defaultCopied := false, false, false, false
		for i, r := range old.rules {
			id, err := remint(r.OID)
			if err != nil {
				return err
			}
			if id == r.OID {
				continue
			}
			if len(d.rules) != 0 && &d.rules[0] == &old.rules[0] {
				d.rules = append([]ruleDefinition(nil), old.rules...)
			}
			d.rules[i].OID = id
			changed = true
		}
		for i, p := range old.policies {
			id, err := remint(p.OID)
			if err != nil {
				return err
			}
			if id == p.OID {
				continue
			}
			if len(d.policies) != 0 && &d.policies[0] == &old.policies[0] {
				d.policies = append([]policyDefinition(nil), old.policies...)
			}
			d.policies[i].OID = id
			changed = true
		}
		for i, def := range old.notNullConstraints {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if !nullCopied {
				d.notNullConstraints = append([]notNullConstraint(nil), old.notNullConstraints...)
				nullCopied = true
			}
			d.notNullConstraints[i].OID = id
			recordConstraint(def.OID, id)
			changed = true
		}
		for i, def := range old.checkConstraints {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if !checkCopied {
				d.checkConstraints = append([]checkConstraint(nil), old.checkConstraints...)
				checkCopied = true
			}
			d.checkConstraints[i].OID = id
			recordConstraint(def.OID, id)
			changed = true
		}
		for i, def := range old.uniqueConstraints {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			uniqueMapping[def.OID] = id
			if id == def.OID {
				continue
			}
			if d.uniqueConstraints == nil || &d.uniqueConstraints[0] == &old.uniqueConstraints[0] {
				d.uniqueConstraints = append([]uniqueConstraint(nil), old.uniqueConstraints...)
			}
			d.uniqueConstraints[i].OID = id
			recordConstraint(def.OID, id)
			index, ok := next.descriptors[def.Index].(*relationDescriptor)
			if !ok || index.sqlIndex == nil {
				return fmt.Errorf("stuntgres: invalid imported unique index")
			}
			clone := *index
			definition := *index.sqlIndex
			constraint := d.uniqueConstraints[i]
			definition.constraint = uniqueBacking(&constraint)
			clone.sqlIndex = &definition
			next.descriptors[clone.address] = &clone
			changed = true
		}
		for i, def := range old.exclusionConstraints {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if d.exclusionConstraints == nil || &d.exclusionConstraints[0] == &old.exclusionConstraints[0] {
				d.exclusionConstraints = append([]exclusionConstraint(nil), old.exclusionConstraints...)
			}
			d.exclusionConstraints[i].OID = id
			recordConstraint(def.OID, id)
			index, ok := next.descriptors[def.Index].(*relationDescriptor)
			if !ok || index.sqlIndex == nil {
				return fmt.Errorf("stuntgres: invalid imported exclusion index")
			}
			clone := *index
			definition := *index.sqlIndex
			definition.constraint = exclusionBacking(&d.exclusionConstraints[i])
			clone.sqlIndex = &definition
			next.descriptors[clone.address] = &clone
			changed = true
		}
		for i, def := range old.foreignKeys {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if &d.foreignKeys[0] == &old.foreignKeys[0] {
				d.foreignKeys = append([]foreignKeyConstraint(nil), old.foreignKeys...)
			}
			d.foreignKeys[i].OID = id
			recordConstraint(def.OID, id)
			changed = true
		}
		for attr, def := range old.defaults {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if !defaultCopied {
				d.defaults = make(map[int32]columnDefault, len(old.defaults))
				for key, value := range old.defaults {
					d.defaults[key] = value
				}
				defaultCopied = true
			}
			def.OID = id
			d.defaults[attr] = def
			changed = true
		}
		generationCopied := false
		for attr, definition := range old.generations {
			id, err := remint(definition.OID)
			if err != nil {
				return err
			}
			if id == definition.OID {
				continue
			}
			if !generationCopied {
				d.generations = make(map[int32]columnGeneration, len(old.generations))
				for key, value := range old.generations {
					d.generations[key] = value
				}
				generationCopied = true
			}
			definition.OID = id
			d.generations[attr] = definition
			changed = true
		}
		if changed {
			replacement := d
			next.descriptors[d.address] = &replacement
		}
	}
	for _, descriptor := range next.descriptors {
		table, ok := descriptor.(*relationDescriptor)
		if !ok || len(table.uniqueConstraints) == 0 {
			continue
		}
		var changed *relationDescriptor
		for i, constraint := range table.uniqueConstraints {
			parent, exists := uniqueMapping[constraint.Parent]
			if !exists || parent == constraint.Parent {
				continue
			}
			if changed == nil {
				copy := *table
				copy.uniqueConstraints = append([]uniqueConstraint(nil), table.uniqueConstraints...)
				changed = &copy
			}
			changed.uniqueConstraints[i].Parent = parent
		}
		if changed != nil {
			next.descriptors[changed.address] = changed
		}
	}
	if len(constraintMapping) != 0 {
		view := CatalogRead{owner: e, state: next}
		for address, descriptor := range next.descriptors {
			parent, ok := descriptor.(*relationDescriptor)
			if !ok || len(parent.rules) == 0 {
				continue
			}
			if namespace, ok := next.descriptors[parent.namespace].(*namespaceDescriptor); ok && namespace.temporary != (SessionToken{}) {
				continue
			}
			remapped, err := remapRuleConstraintIdentities(view, parent, constraintMapping)
			if err != nil {
				return err
			}
			if remapped != parent {
				next.descriptors[address] = remapped
			}
		}
	}
	return nil
}
