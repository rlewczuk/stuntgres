package engine

import (
	"fmt"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// structuralNull only recognizes transparent coercions of a literal NULL.
// Other expressions remain definitions even when evaluation would return NULL.
func structuralNull(node expr.Expr) bool {
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

func (e *Engine) assignDefinitionOIDsLocked(d *catalog.Relation) error {
	for i := range d.NotNullConstraints {
		if d.NotNullConstraints[i].OID == 0 {
			id, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			d.NotNullConstraints[i].OID = id
		}
	}
	for _, attr := range d.Attributes {
		def, ok := d.Defaults[attr]
		if ok && def.OID == 0 {
			id, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			def.OID = id
			d.Defaults[attr] = def
		}
	}
	for _, attr := range d.Attributes {
		definition, exists := d.Generations[attr]
		if exists && definition.OID == 0 {
			id, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			definition.OID = id
			d.Generations[attr] = definition
		}
	}
	for i := range d.CheckConstraints {
		if d.CheckConstraints[i].OID == 0 {
			id, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			d.CheckConstraints[i].OID = id
		}
	}
	for i := range d.UniqueConstraints {
		if d.UniqueConstraints[i].OID == 0 {
			id, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			d.UniqueConstraints[i].OID = id
		}
	}
	for i := range d.ForeignKeys {
		if d.ForeignKeys[i].OID == 0 {
			id, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			d.ForeignKeys[i].OID = id
		}
	}
	return nil
}

func definitionOIDUsed(descriptor catalog.Descriptor, id ids.OID) bool {
	d, relation := descriptor.(*catalog.Relation)
	if !relation {
		if proc, ok := descriptor.(*catalog.Procedure); ok && proc.Aggregate != nil {
			for _, helper := range proc.Aggregate.Helpers {
				if helper.ID == id {
					return true
				}
			}
		}
		if domain, ok := descriptor.(*catalog.Type); ok {
			for _, check := range domain.DomainChecks {
				if check.OID == id {
					return true
				}
			}
		}
		return false
	}
	if d.View != nil && d.View.RuleOID() == id {
		return true
	}
	for _, r := range rulesOf(d) {
		if r.OID == id {
			return true
		}
	}
	for _, p := range d.Security.Policies {
		if p.OID == id {
			return true
		}
	}
	for _, c := range d.NotNullConstraints {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.CheckConstraints {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.UniqueConstraints {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.ExclusionConstraints {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.ForeignKeys {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.Defaults {
		if c.OID == id {
			return true
		}
	}
	for _, definition := range d.Generations {
		if definition.OID == id {
			return true
		}
	}
	return false
}

// Descriptor and owned-definition OIDs share one identity space. Replacement
// must reserve both before allocating any collision replacement.
func reserveDefinitionOIDs(reserved map[ids.OID]bool, descriptor catalog.Descriptor) {
	d, relation := descriptor.(*catalog.Relation)
	if !relation {
		if proc, ok := descriptor.(*catalog.Procedure); ok && proc.Aggregate != nil {
			for _, helper := range proc.Aggregate.Helpers {
				reserved[helper.ID] = true
			}
		}
		if domain, ok := descriptor.(*catalog.Type); ok {
			for _, check := range domain.DomainChecks {
				reserved[check.OID] = true
			}
		}
		return
	}
	if d.View != nil {
		reserved[d.View.RuleOID()] = true
	}
	for _, r := range rulesOf(d) {
		reserved[r.OID] = true
	}
	for _, p := range d.Security.Policies {
		reserved[p.OID] = true
	}
	for _, c := range d.NotNullConstraints {
		reserved[c.OID] = true
	}
	for _, c := range d.CheckConstraints {
		reserved[c.OID] = true
	}
	for _, c := range d.UniqueConstraints {
		reserved[c.OID] = true
	}
	for _, c := range d.ExclusionConstraints {
		reserved[c.OID] = true
	}
	for _, c := range d.ForeignKeys {
		reserved[c.OID] = true
	}
	for _, c := range d.Defaults {
		reserved[c.OID] = true
	}
	for _, c := range d.Generations {
		reserved[c.OID] = true
	}
}

func validateDefinitionOIDs(view catalog.Read) error {
	used := make(map[ids.OID]bool)
	for address := range view.Descriptors() {
		used[address.ObjectID] = true
	}
	claim := func(id ids.OID) error {
		if id == 0 || used[id] {
			return fmt.Errorf("stuntgres: invalid stored definition identity")
		}
		used[id] = true
		return nil
	}
	for _, descriptor := range view.Descriptors() {
		if proc, ok := descriptor.(*catalog.Procedure); ok && proc.Aggregate != nil {
			for _, helper := range proc.Aggregate.Helpers {
				if err := claim(helper.ID); err != nil {
					return err
				}
			}
		}
		if domain, ok := descriptor.(*catalog.Type); ok {
			for _, check := range domain.DomainChecks {
				if err := claim(check.OID); err != nil {
					return err
				}
			}
		}
		d, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if d.View != nil {
			if err := claim(d.View.RuleOID()); err != nil {
				return err
			}
		}
		for _, r := range rulesOf(d) {
			if err := claim(r.OID); err != nil {
				return err
			}
		}
		for _, p := range d.Security.Policies {
			if err := claim(p.OID); err != nil {
				return err
			}
		}
		for _, c := range d.NotNullConstraints {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.CheckConstraints {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.UniqueConstraints {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.ExclusionConstraints {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.ForeignKeys {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.Defaults {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, definition := range d.Generations {
			if err := claim(definition.OID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Replacement retains temporary objects and imports persistent definitions.
// Reserve the entire candidate before allocating collision replacements.
func (e *Engine) remapDefinitionOIDsLocked(next *catalog.State) error {
	reserved := make(map[ids.OID]bool)
	var relations []*catalog.Relation
	var domains []*catalog.Type
	for address, descriptor := range next.Descriptors() {
		reserved[address.ObjectID] = true
		if proc, ok := descriptor.(*catalog.Procedure); ok && proc.Aggregate != nil {
			reserveDefinitionOIDs(reserved, proc)
		}
		if domain, ok := descriptor.(*catalog.Type); ok && len(domain.DomainChecks) != 0 {
			domains = append(domains, domain)
			reserveDefinitionOIDs(reserved, domain)
		}
		if d, ok := descriptor.(*catalog.Relation); ok {
			relations = append(relations, d)
			reserveDefinitionOIDs(reserved, d)
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].Address().ObjectID < relations[j].Address().ObjectID })
	claimed := make(map[ids.OID]bool)
	for address, descriptor := range next.Descriptors() {
		claimed[address.ObjectID] = true
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
			for _, r := range rulesOf(d) {
				claimed[r.OID] = true
			}
			for _, p := range d.Security.Policies {
				claimed[p.OID] = true
			}
			for _, definition := range d.NotNullConstraints {
				claimed[definition.OID] = true
			}
			for _, definition := range d.CheckConstraints {
				claimed[definition.OID] = true
			}
			for _, definition := range d.UniqueConstraints {
				claimed[definition.OID] = true
			}
			for _, definition := range d.ExclusionConstraints {
				claimed[definition.OID] = true
			}
			for _, definition := range d.ForeignKeys {
				claimed[definition.OID] = true
			}
			for _, definition := range d.Defaults {
				claimed[definition.OID] = true
			}
			for _, definition := range d.Generations {
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
			candidate, err := e.catalog.allocateOIDLocked()
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
		if len(old.NotNullConstraints)+len(old.CheckConstraints)+len(old.UniqueConstraints)+len(old.ExclusionConstraints)+len(old.ForeignKeys)+len(old.Defaults)+len(old.Generations)+len(old.Security.Policies)+len(rulesOf(old)) == 0 {
			continue
		}
		d := *old
		changed, nullCopied, checkCopied, defaultCopied := false, false, false, false
		for i, r := range rulesOf(old) {
			id, err := remint(r.OID)
			if err != nil {
				return err
			}
			if id == r.OID {
				continue
			}
			if len(rulesOf(&d)) != 0 && &rulesOf(&d)[0] == &rulesOf(old)[0] {
				d.Rules = rulesPayloadOf(append([]ruleDefinition(nil), rulesOf(old)...))
			}
			rulesOf(&d)[i].OID = id
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
		for i, def := range old.NotNullConstraints {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if !nullCopied {
				d.NotNullConstraints = append([]catalog.NotNullConstraint(nil), old.NotNullConstraints...)
				nullCopied = true
			}
			d.NotNullConstraints[i].OID = id
			recordConstraint(def.OID, id)
			changed = true
		}
		for i, def := range old.CheckConstraints {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if !checkCopied {
				d.CheckConstraints = append([]catalog.CheckConstraint(nil), old.CheckConstraints...)
				checkCopied = true
			}
			d.CheckConstraints[i].OID = id
			recordConstraint(def.OID, id)
			changed = true
		}
		for i, def := range old.UniqueConstraints {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			uniqueMapping[def.OID] = id
			if id == def.OID {
				continue
			}
			if d.UniqueConstraints == nil || &d.UniqueConstraints[0] == &old.UniqueConstraints[0] {
				d.UniqueConstraints = append([]catalog.UniqueConstraint(nil), old.UniqueConstraints...)
			}
			d.UniqueConstraints[i].OID = id
			recordConstraint(def.OID, id)
			index, ok := next.Relation(def.Index)
			if !ok || index.SQLIndex == nil {
				return fmt.Errorf("stuntgres: invalid imported unique index")
			}
			clone := *index
			definition := *index.SQLIndex
			constraint := d.UniqueConstraints[i]
			definition.Constraint = uniqueBacking(&constraint)
			clone.SQLIndex = &definition
			next.SetDescriptor(clone.Address(), &clone)
			changed = true
		}
		for i, def := range old.ExclusionConstraints {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if d.ExclusionConstraints == nil || &d.ExclusionConstraints[0] == &old.ExclusionConstraints[0] {
				d.ExclusionConstraints = append([]catalog.ExclusionConstraint(nil), old.ExclusionConstraints...)
			}
			d.ExclusionConstraints[i].OID = id
			recordConstraint(def.OID, id)
			index, ok := next.Relation(def.Index)
			if !ok || index.SQLIndex == nil {
				return fmt.Errorf("stuntgres: invalid imported exclusion index")
			}
			clone := *index
			definition := *index.SQLIndex
			definition.Constraint = exclusionBacking(&d.ExclusionConstraints[i])
			clone.SQLIndex = &definition
			next.SetDescriptor(clone.Address(), &clone)
			changed = true
		}
		for i, def := range old.ForeignKeys {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if &d.ForeignKeys[0] == &old.ForeignKeys[0] {
				d.ForeignKeys = append([]catalog.ForeignKeyConstraint(nil), old.ForeignKeys...)
			}
			d.ForeignKeys[i].OID = id
			recordConstraint(def.OID, id)
			changed = true
		}
		for attr, def := range old.Defaults {
			id, err := remint(def.OID)
			if err != nil {
				return err
			}
			if id == def.OID {
				continue
			}
			if !defaultCopied {
				d.Defaults = make(map[int32]catalog.ColumnDefault, len(old.Defaults))
				for key, value := range old.Defaults {
					d.Defaults[key] = value
				}
				defaultCopied = true
			}
			def.OID = id
			d.Defaults[attr] = def
			changed = true
		}
		generationCopied := false
		for attr, definition := range old.Generations {
			id, err := remint(definition.OID)
			if err != nil {
				return err
			}
			if id == definition.OID {
				continue
			}
			if !generationCopied {
				d.Generations = make(map[int32]catalog.ColumnGeneration, len(old.Generations))
				for key, value := range old.Generations {
					d.Generations[key] = value
				}
				generationCopied = true
			}
			definition.OID = id
			d.Generations[attr] = definition
			changed = true
		}
		if changed {
			replacement := d
			next.SetDescriptor(d.Address(), &replacement)
		}
	}
	for _, descriptor := range next.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || len(table.UniqueConstraints) == 0 && len(table.ExclusionConstraints) == 0 {
			continue
		}
		var changed *catalog.Relation
		for i, constraint := range table.UniqueConstraints {
			parent, exists := uniqueMapping[constraint.Parent]
			if !exists || parent == constraint.Parent {
				continue
			}
			if changed == nil {
				copy := *table
				copy.UniqueConstraints = append([]catalog.UniqueConstraint(nil), table.UniqueConstraints...)
				changed = &copy
			}
			changed.UniqueConstraints[i].Parent = parent
		}
		for i, constraint := range table.ExclusionConstraints {
			parent, exists := constraintMapping[ids.ObjectAddress{ClassID: 2606, ObjectID: constraint.Parent}]
			if !exists || parent.ObjectID == constraint.Parent {
				continue
			}
			if changed == nil {
				copy := *table
				changed = &copy
			}
			if changed.ExclusionConstraints == nil || &changed.ExclusionConstraints[0] == &table.ExclusionConstraints[0] {
				changed.ExclusionConstraints = append([]catalog.ExclusionConstraint(nil), table.ExclusionConstraints...)
			}
			changed.ExclusionConstraints[i].Parent = parent.ObjectID
		}
		if changed != nil {
			next.SetDescriptor(changed.Address(), changed)
		}
	}
	if len(constraintMapping) != 0 {
		view := e.catalog.read(next)
		for address, descriptor := range next.Descriptors() {
			parent, ok := descriptor.(*catalog.Relation)
			if !ok || len(rulesOf(parent)) == 0 {
				continue
			}
			if namespace, ok := next.Namespace(parent.Namespace()); ok && namespace.Temporary != (ids.SessionToken{}) {
				continue
			}
			remapped, err := remapRuleConstraintIdentities(view, parent, constraintMapping)
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
