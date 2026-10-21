package engine

import (
	"stuntgres/internal/engine/catalog"
)

func validateTypeProviders(view catalog.Read, d catalog.Descriptor) error {
	switch d := d.(type) {
	case *catalog.Type:
		if d.SQLType.IsDomain() {
			base := d.SQLType.DomainBase()
			if err := ValidateUsageCollation(view, base); err != nil {
				return err
			}
			actual, ok := view.LookupType(base.Type.ID())
			if !ok || actual != base.Type {
				return catalog.Conflict{}
			}
		}
		if d.SQLType.IsArray() {
			actual, ok := view.LookupType(d.SQLType.Element().ID())
			if !ok || actual != d.SQLType.Element() {
				return catalog.Conflict{}
			}
		}
	case *catalog.Relation:
		for _, c := range d.Columns {
			if err := ValidateUsageCollation(view, c.Usage); err != nil {
				return err
			}
			actual, ok := view.LookupType(c.Usage.Type.ID())
			if !ok || actual != c.Usage.Type {
				return catalog.Conflict{}
			}
		}
	}
	return nil
}
