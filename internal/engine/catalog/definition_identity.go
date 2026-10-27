package catalog

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/ids"
)

// errDefinitionOIDFound stops the visit in DefinitionOIDUsed.
var errDefinitionOIDFound = errors.New("definition OID found")

// VisitDefinitionOIDs calls visit with each identity the descriptor owns
// besides its own address: aggregate helpers, domain constraints, and a
// relation's view rule, rules, triggers (including FK RI identities), policies,
// constraints, defaults and generation expressions. It returns the first error visit returns.
func VisitDefinitionOIDs(descriptor Descriptor, visit func(ids.OID) error) error {
	d, relation := descriptor.(*Relation)
	if !relation {
		if proc, ok := descriptor.(*Procedure); ok && proc.Aggregate != nil && proc.Implementation != nil {
			for _, helper := range proc.Aggregate.Helpers {
				if err := visit(helper.ID); err != nil {
					return err
				}
			}
		}
		if domain, ok := descriptor.(*Type); ok {
			if domain.DomainNotNull != nil {
				if err := visit(domain.DomainNotNull.OID); err != nil {
					return err
				}
			}
			for _, check := range domain.DomainChecks {
				if err := visit(check.OID); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if d.View != nil {
		if err := visit(d.View.RuleOID()); err != nil {
			return err
		}
	}
	// Indexed, not visited: a callback would make visit escape.
	if d.Rules != nil {
		for i := range d.Rules.Len() {
			if err := visit(d.Rules.OID(i)); err != nil {
				return err
			}
		}
	}
	if d.Triggers != nil {
		for i := range d.Triggers.Len() {
			if err := visit(d.Triggers.OID(i)); err != nil {
				return err
			}
		}
	}
	for _, p := range d.Security.Policies {
		if err := visit(p.OID); err != nil {
			return err
		}
	}
	for _, c := range d.Constraints.NotNull {
		if err := visit(c.OID); err != nil {
			return err
		}
	}
	for _, c := range d.Constraints.Check {
		if err := visit(c.OID); err != nil {
			return err
		}
	}
	for _, c := range d.Constraints.Unique {
		if err := visit(c.OID); err != nil {
			return err
		}
	}
	for _, c := range d.Constraints.Exclusion {
		if err := visit(c.OID); err != nil {
			return err
		}
	}
	for _, c := range d.Constraints.ForeignKeys {
		if err := visit(c.OID); err != nil {
			return err
		}
		for _, trigger := range c.TriggerOIDs {
			if err := visit(trigger); err != nil {
				return err
			}
		}
	}
	for _, c := range d.ColumnMeta.Defaults {
		if err := visit(c.OID); err != nil {
			return err
		}
	}
	for _, c := range d.ColumnMeta.Generations {
		if err := visit(c.OID); err != nil {
			return err
		}
	}
	return nil
}

// DefinitionOIDUsed reports whether the descriptor owns id, besides its own
// address.
func DefinitionOIDUsed(descriptor Descriptor, id ids.OID) bool {
	return VisitDefinitionOIDs(descriptor, func(owned ids.OID) error {
		if owned == id {
			return errDefinitionOIDFound
		}
		return nil
	}) != nil
}

// ReserveDefinitionOIDs reserves identities owned by the descriptor, not the
// descriptor's own address. DefinitionOIDUsed probes the same owned identities.
func ReserveDefinitionOIDs(reserved map[ids.OID]bool, descriptor Descriptor) {
	_ = VisitDefinitionOIDs(descriptor, func(id ids.OID) error {
		reserved[id] = true
		return nil
	})
}

var errInvalidDefinitionIdentity = fmt.Errorf("stuntgres: invalid stored definition identity")

// ValidateDefinitionOIDs checks that every owned definition identity is
// nonzero and differs from every descriptor address (outside the large-object
// class) and every other owned identity.
func ValidateDefinitionOIDs(view Read) error {
	// Sized for every descriptor, so the scan below never grows the set.
	used := make(map[ids.OID]struct{}, view.DescriptorCount())
	for address := range view.Descriptors() {
		if address.ClassID != ids.LargeObjectClassID {
			used[address.ObjectID] = struct{}{}
		}
	}
	claim := func(id ids.OID) error {
		if _, taken := used[id]; id == 0 || taken {
			return errInvalidDefinitionIdentity
		}
		used[id] = struct{}{}
		return nil
	}
	for _, descriptor := range view.Descriptors() {
		if err := VisitDefinitionOIDs(descriptor, claim); err != nil {
			return err
		}
	}
	return nil
}

// errDefinitionScanNeeded makes ValidateStagedDefinitionOIDs fall back to the
// catalog scan.
var errDefinitionScanNeeded = errors.New("definition identity scan needed")

// ValidateStagedDefinitionOIDs is ValidateDefinitionOIDs(read) for read, the
// candidate applied to its base, given that the base passes it. Only staged
// descriptors can then break it. A staged identity above the base's OID
// ceiling, or one the same address already used in the base, is used by no
// unchanged descriptor, so the staged identities are checked against each
// other. Any other staged identity falls back to the catalog scan.
func (c *Candidate) ValidateStagedDefinitionOIDs(read Read) error {
	base := c.Base.state
	if base == nil {
		return ValidateDefinitionOIDs(read)
	}
	ceiling := base.oidCeiling
	addresses := make(map[ids.OID]struct{}, len(c.descriptors))
	for a, d := range c.descriptors {
		if d == nil || a.ClassID == ids.LargeObjectClassID {
			continue
		}
		if _, existed := base.findDescriptor(a); !existed && a.ObjectID <= ceiling {
			return ValidateDefinitionOIDs(read)
		}
		addresses[a.ObjectID] = struct{}{}
	}
	claimed := make(map[ids.OID]struct{}, len(c.descriptors))
	for a, d := range c.descriptors {
		if d == nil {
			continue
		}
		old, _ := base.findDescriptor(a)
		err := VisitDefinitionOIDs(d, func(id ids.OID) error {
			_, taken := claimed[id]
			_, address := addresses[id]
			if id == 0 || taken || address {
				return errInvalidDefinitionIdentity
			}
			if id <= ceiling && (old == nil || !DefinitionOIDUsed(old, id)) {
				return errDefinitionScanNeeded
			}
			claimed[id] = struct{}{}
			return nil
		})
		if err == errDefinitionScanNeeded {
			return ValidateDefinitionOIDs(read)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
