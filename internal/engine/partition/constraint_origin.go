package partition

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
)

// ValidateConstraintOrigins validates owned identities, not a same-name
// inference of history. A local child fact may legitimately have no parent fact.
func ValidateConstraintOrigins(view catalog.Read, d *catalog.Relation) error {
	invalid := func() error { return fmt.Errorf("stuntgres: invalid partition constraint provenance") }
	validate := func(p *catalog.InheritedConstraint, name string, attribute int32, check bool) error {
		if p == nil {
			return nil
		}
		if len(p.Retired) != 0 || len(p.Origins) > 1 || len(p.Origins) == 0 && !p.Local {
			return invalid()
		}
		if len(p.Origins) == 0 {
			return nil
		}
		origin := p.Origins[0]
		if d.Membership == nil || origin.Parent != d.Membership.Parent || origin.Constraint == 0 {
			return invalid()
		}
		parent, ok := view.Relation(origin.Parent)
		if !ok {
			return invalid()
		}
		if check {
			if origin.Attribute != 0 || p.Local {
				return invalid()
			}
			for _, c := range parent.Constraints.Check {
				if c.OID == origin.Constraint && c.Name == name && !c.NoInherit {
					return nil
				}
			}
		} else {
			slot := d.IndexForAttribute(attribute)
			if slot < 0 {
				return invalid()
			}
			ps, ok := parent.Index[d.Columns[slot].Name]
			if !ok || origin.Attribute != parent.Attributes[ps] {
				return invalid()
			}
			for _, c := range parent.Constraints.NotNull {
				if c.OID == origin.Constraint && c.Attribute == origin.Attribute && !c.NoInherit {
					return nil
				}
			}
		}
		return invalid()
	}
	for _, c := range d.Constraints.Check {
		if err := validate(c.PartitionOrigin, c.Name, 0, true); err != nil {
			return err
		}
	}
	for _, c := range d.Constraints.NotNull {
		if err := validate(c.PartitionOrigin, c.Name, c.Attribute, false); err != nil {
			return err
		}
	}
	return nil
}
