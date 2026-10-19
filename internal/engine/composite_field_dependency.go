package engine

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Field dependencies retain logical attribute identities, not field spellings.
func expressionUsesCompositeField(node expr.Expr, id sqltype.ID, attribute int32) bool {
	if field, ok := node.(fieldExpression); ok && field.base.ResultUsage().Type.BaseType().ID() == id && field.attribute == attribute {
		return true
	}
	found := false
	boundExpressionChildren(node, func(child expr.Expr) {
		if !found {
			found = expressionUsesCompositeField(child, id, attribute)
		}
	})
	return found
}

func stageCompositeFieldChecks(stage *CatalogStage, source *catalog.Relation, attribute int32, mode parser.DropMode, scope *StatementScope) error {
	return stageDropProvider(nil, stage, scope, nil, nil).compositeFieldChecks(source, attribute, mode)
}

func (provider *dropProvider) compositeFieldChecks(source *catalog.Relation, attribute int32, mode parser.DropMode) error {
	var relations []*catalog.Relation
	var domains []*catalog.Type
	for _, descriptor := range provider.descriptors() {
		if relation, ok := descriptor.(*catalog.Relation); ok && len(relation.Constraints.Check) != 0 {
			relations = append(relations, relation)
		}
		if domain, ok := descriptor.(*catalog.Type); ok && len(domain.DomainChecks) != 0 {
			domains = append(domains, domain)
		}
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
			if !expressionUsesCompositeField(check.Expression, source.RowType, attribute) {
				kept = append(kept, check)
				continue
			}
			label := fmt.Sprintf("constraint %s on table %s", catalog.DependencyIdentifier(check.Name), catalog.DependencyIdentifier(relation.Name()))
			details = append(details, fmt.Sprintf("%s depends on column %s of composite type %s", label, catalog.DependencyIdentifier(source.Columns[source.IndexForAttribute(attribute)].Name), catalog.DependencyIdentifier(source.Name())))
			if mode == parser.DropCascade {
				provider.compositeFieldNotice("drop cascades to " + label)
			}
		}
		if len(kept) == len(relation.Constraints.Check) || mode != parser.DropCascade {
			continue
		}
		if provider.stage == nil || !typedFieldCheckOwner(provider.stage, relation) {
			if err := provider.inheritanceAlterGuard(relation); err != nil {
				return err
			}
		}
		next := *relation
		revision, err := provider.revision(next.Revision())
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
		if err := provider.replace(&next); err != nil {
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
			if !expressionUsesCompositeField(check.Expression, source.RowType, attribute) {
				continue
			}
			label := fmt.Sprintf("constraint %s", catalog.DependencyIdentifier(check.Name))
			details = append(details, fmt.Sprintf("%s depends on column %s of composite type %s", label, catalog.DependencyIdentifier(source.Columns[source.IndexForAttribute(attribute)].Name), catalog.DependencyIdentifier(source.Name())))
			if mode == parser.DropCascade {
				if err := provider.removeDomainCheck(domain, check.OID); err != nil {
					return err
				}
				provider.compositeFieldNotice("drop cascades to " + label)
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
func (provider *dropProvider) compositeFieldNotice(message string) {
	notice := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message}
	if provider.prepared != nil {
		provider.prepared.recordEffect(dropEffect{missing: &notice})
	} else if provider.scope != nil {
		provider.scope.op.emitNotice(levelNotice, notice)
	}
}

// Only the already admitted typed propagation closure may prune inherited CHECKs.
func typedFieldCheckOwner(stage *CatalogStage, relation *catalog.Relation) bool {
	if stage.typedSource == (ids.ObjectAddress{}) || stage.typedObserved[relation.Address()] == nil {
		return false
	}
	if relation.OfType == stage.typedSource {
		return true
	}
	if relation.Inheritance != nil {
		for _, address := range relation.Inheritance.Parents {
			parent, ok := stage.Base.Relation(address)
			if ok && typedFieldCheckOwner(stage, parent) {
				return true
			}
		}
	}
	return false
}
