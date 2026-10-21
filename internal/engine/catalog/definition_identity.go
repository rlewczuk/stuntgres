package catalog

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/ids"
)

// errDefinitionOIDFound stops the rule visit in DefinitionOIDUsed.
var errDefinitionOIDFound = errors.New("definition OID found")

func DefinitionOIDUsed(descriptor Descriptor, id ids.OID) bool {
	d, relation := descriptor.(*Relation)
	if !relation {
		if proc, ok := descriptor.(*Procedure); ok && proc.Aggregate != nil {
			for _, helper := range proc.Aggregate.Helpers {
				if helper.ID == id {
					return true
				}
			}
		}
		if domain, ok := descriptor.(*Type); ok {
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
	if d.HasRules() && d.VisitRules(func(r RuleFact) error {
		if r.OID == id {
			return errDefinitionOIDFound
		}
		return nil
	}) != nil {
		return true
	}
	for _, p := range d.Security.Policies {
		if p.OID == id {
			return true
		}
	}
	for _, c := range d.Constraints.NotNull {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.Constraints.Check {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.Constraints.Unique {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.Constraints.Exclusion {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.Constraints.ForeignKeys {
		if c.OID == id {
			return true
		}
	}
	for _, c := range d.ColumnMeta.Defaults {
		if c.OID == id {
			return true
		}
	}
	for _, definition := range d.ColumnMeta.Generations {
		if definition.OID == id {
			return true
		}
	}
	return false
}

// Descriptor and owned-definition OIDs share one identity space. Replacement
// must reserve both before allocating any collision replacement.
func ReserveDefinitionOIDs(reserved map[ids.OID]bool, descriptor Descriptor) {
	d, relation := descriptor.(*Relation)
	if !relation {
		if proc, ok := descriptor.(*Procedure); ok && proc.Aggregate != nil {
			for _, helper := range proc.Aggregate.Helpers {
				reserved[helper.ID] = true
			}
		}
		if domain, ok := descriptor.(*Type); ok {
			for _, check := range domain.DomainChecks {
				reserved[check.OID] = true
			}
		}
		return
	}
	if d.View != nil {
		reserved[d.View.RuleOID()] = true
	}
	if d.HasRules() {
		_ = d.VisitRules(func(r RuleFact) error {
			reserved[r.OID] = true
			return nil
		})
	}
	for _, p := range d.Security.Policies {
		reserved[p.OID] = true
	}
	for _, c := range d.Constraints.NotNull {
		reserved[c.OID] = true
	}
	for _, c := range d.Constraints.Check {
		reserved[c.OID] = true
	}
	for _, c := range d.Constraints.Unique {
		reserved[c.OID] = true
	}
	for _, c := range d.Constraints.Exclusion {
		reserved[c.OID] = true
	}
	for _, c := range d.Constraints.ForeignKeys {
		reserved[c.OID] = true
	}
	for _, c := range d.ColumnMeta.Defaults {
		reserved[c.OID] = true
	}
	for _, c := range d.ColumnMeta.Generations {
		reserved[c.OID] = true
	}
}

func ValidateDefinitionOIDs(view Read) error {
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
		if proc, ok := descriptor.(*Procedure); ok && proc.Aggregate != nil {
			for _, helper := range proc.Aggregate.Helpers {
				if err := claim(helper.ID); err != nil {
					return err
				}
			}
		}
		if domain, ok := descriptor.(*Type); ok {
			for _, check := range domain.DomainChecks {
				if err := claim(check.OID); err != nil {
					return err
				}
			}
		}
		d, ok := descriptor.(*Relation)
		if !ok {
			continue
		}
		if d.View != nil {
			if err := claim(d.View.RuleOID()); err != nil {
				return err
			}
		}
		if d.HasRules() {
			if err := d.VisitRules(func(r RuleFact) error { return claim(r.OID) }); err != nil {
				return err
			}
		}
		for _, p := range d.Security.Policies {
			if err := claim(p.OID); err != nil {
				return err
			}
		}
		for _, c := range d.Constraints.NotNull {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.Constraints.Check {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.Constraints.Unique {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.Constraints.Exclusion {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.Constraints.ForeignKeys {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, c := range d.ColumnMeta.Defaults {
			if err := claim(c.OID); err != nil {
				return err
			}
		}
		for _, definition := range d.ColumnMeta.Generations {
			if err := claim(definition.OID); err != nil {
				return err
			}
		}
	}
	return nil
}
