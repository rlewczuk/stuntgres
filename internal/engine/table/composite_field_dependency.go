package table

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// CompositeFieldDependencies drops or rejects the CHECK constraints of tables
// and domains that use attribute of composite type source. A domain DEFAULT
// that uses it takes the whole expression-dependency path instead. Notices
// for CASCADE go through provider.Notice in discovery order.
func CompositeFieldDependencies(provider DropDependencyReader, source *catalog.Relation, attribute int32, mode parser.DropMode) error {
	var relations []*catalog.Relation
	var domains []*catalog.Type
	target := source.Address()
	target.SubID = attribute
	defaultDependent := false
	for address, descriptor := range provider.Descriptors() {
		if provider.Removed(address) {
			continue
		}
		if current := provider.Replacement(address); current != nil {
			descriptor = current
		}
		if relation, ok := descriptor.(*catalog.Relation); ok && len(relation.Constraints.Check) != 0 {
			relations = append(relations, relation)
		}
		if domain, ok := descriptor.(*catalog.Type); ok && (len(domain.DomainChecks) != 0 || domain.DomainDefault != nil) {
			if len(domain.DomainChecks) != 0 {
				domains = append(domains, domain)
			}
			if domain.DomainDefault != nil {
				for _, ref := range domain.DomainDefault.Dependencies {
					if ref.Address == target {
						defaultDependent = true
						break
					}
				}
			}
		}
	}
	if defaultDependent {
		// Defaults own their type, unlike a separately removable CHECK. Use
		// the ordinary authenticated whole-domain/column/view closure before
		// the composite descriptor and its stored expressions are retagged.
		return provider.ExpressionDependencies([]ids.ObjectAddress{target}, mode, target)
	}
	slices.SortFunc(relations, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	var details []string
	for _, relation := range relations {
		var kept []catalog.CheckConstraint
		for _, check := range relation.Constraints.Check {
			if !plan.ExpressionUsesCompositeField(check.Expression, source.RowType, attribute) {
				kept = append(kept, check)
				continue
			}
			label := fmt.Sprintf("constraint %s on table %s", catalog.DependencyIdentifier(check.Name), catalog.DependencyIdentifier(relation.Name()))
			details = append(details, fmt.Sprintf("%s depends on column %s of composite type %s", label, catalog.DependencyIdentifier(source.Columns[source.IndexForAttribute(attribute)].Name), catalog.DependencyIdentifier(source.Name())))
			if mode == parser.DropCascade {
				compositeFieldNotice(provider, "drop cascades to "+label)
			}
		}
		if len(kept) == len(relation.Constraints.Check) || mode != parser.DropCascade {
			continue
		}
		if !provider.TypedFieldCheckOwner(relation) {
			if err := provider.InheritanceAlterGuard(relation); err != nil {
				return err
			}
		}
		next := *relation
		revision, err := provider.Revision(next.Revision())
		if err != nil {
			return err
		}
		next.ObjectRevision, next.Constraints.Check = revision, kept
		if next.Inheritance != nil {
			graph := *next.Inheritance
			graph.Checks = maps.Clone(graph.Checks)
			for name := range graph.Checks {
				if !slices.ContainsFunc(kept, func(check catalog.CheckConstraint) bool { return check.Name == name }) {
					delete(graph.Checks, name)
				}
			}
			next.Inheritance = &graph
		}
		if err := provider.Replace(&next); err != nil {
			return err
		}
	}
	slices.SortFunc(domains, func(a, b *catalog.Type) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	for _, domain := range domains {
		for _, check := range domain.DomainChecks {
			if !plan.ExpressionUsesCompositeField(check.Expression, source.RowType, attribute) {
				continue
			}
			label := fmt.Sprintf("constraint %s", catalog.DependencyIdentifier(check.Name))
			details = append(details, fmt.Sprintf("%s depends on column %s of composite type %s", label, catalog.DependencyIdentifier(source.Columns[source.IndexForAttribute(attribute)].Name), catalog.DependencyIdentifier(source.Name())))
			if mode == parser.DropCascade {
				if err := RemoveDomainCheck(provider, domain, check.OID); err != nil {
					return err
				}
				compositeFieldNotice(provider, "drop cascades to "+label)
			}
		}
	}
	if len(details) != 0 && mode == parser.DropRestrict {
		diagnostic := sqlError("2BP01", fmt.Sprintf("cannot drop column %s of composite type %s because other objects depend on it", catalog.DependencyIdentifier(source.Columns[source.IndexForAttribute(attribute)].Name), catalog.DependencyIdentifier(source.Name()))).(*sqlstate.Error)
		diagnostic.Detail = strings.Join(details, "\n")
		diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		return diagnostic
	}
	return nil
}

// Ordinary composite ALTER owns these immediate notices rather than table/view
// cascade publication. Preparation records the same notice for its real phase.
func compositeFieldNotice(provider DropDependencyReader, message string) {
	provider.Notice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message})
}

// RemoveDomainCheck replaces domain d, as the drop already changed it, with a
// copy without its CHECK constraint oid and a new revision.
func RemoveDomainCheck(provider DropDependencyReader, d *catalog.Type, oid ids.OID) error {
	if replacement, ok := provider.Replacement(d.Address()).(*catalog.Type); ok {
		d = replacement
	}
	next, err := typedef.WithoutDomainCheck(d, oid)
	if err != nil {
		return err
	}
	next.ObjectRevision, err = provider.Revision(d.ObjectRevision)
	if err != nil {
		return err
	}
	return provider.Replace(next)
}
