package engine

import (
	"fmt"
	"maps"
	"slices"
	"strings"
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

func stageCompositeFieldChecks(stage *CatalogStage, source *relationDescriptor, attribute int32, mode parser.DropMode, scope *StatementScope) error {
	return stageDropProvider(nil, stage, scope, nil, nil).compositeFieldChecks(source, attribute, mode)
}

func (provider *dropProvider) compositeFieldChecks(source *relationDescriptor, attribute int32, mode parser.DropMode) error {
	var relations []*relationDescriptor
	var domains []*typeDescriptor
	for _, descriptor := range provider.descriptors() {
		if relation, ok := descriptor.(*relationDescriptor); ok && len(relation.checkConstraints) != 0 {
			relations = append(relations, relation)
		}
		if domain, ok := descriptor.(*typeDescriptor); ok && len(domain.domainChecks) != 0 {
			domains = append(domains, domain)
		}
	}
	slices.SortFunc(relations, func(a, b *relationDescriptor) int {
		if a.address.ObjectID < b.address.ObjectID {
			return -1
		}
		if a.address.ObjectID > b.address.ObjectID {
			return 1
		}
		return 0
	})
	var details []string
	for _, relation := range relations {
		var kept []checkConstraint
		for _, check := range relation.checkConstraints {
			if !expressionUsesCompositeField(check.expression, source.rowType, attribute) {
				kept = append(kept, check)
				continue
			}
			label := fmt.Sprintf("constraint %s on table %s", dependencyIdentifier(check.Name), dependencyIdentifier(relation.name))
			details = append(details, fmt.Sprintf("%s depends on column %s of composite type %s", label, dependencyIdentifier(source.columns[source.indexForAttribute(attribute)].Name), dependencyIdentifier(source.name)))
			if mode == parser.DropCascade {
				provider.compositeFieldNotice("drop cascades to " + label)
			}
		}
		if len(kept) == len(relation.checkConstraints) || mode != parser.DropCascade {
			continue
		}
		if provider.stage == nil || !typedFieldCheckOwner(provider.stage, relation) {
			if err := provider.inheritanceAlterGuard(relation); err != nil {
				return err
			}
		}
		next := *relation
		revision, err := provider.revision(next.revision)
		if err != nil {
			return err
		}
		next.revision, next.checkConstraints = revision, kept
		if next.inheritance != nil {
			graph := *next.inheritance
			graph.checks = maps.Clone(graph.checks)
			for name := range graph.checks {
				if !slices.ContainsFunc(kept, func(check checkConstraint) bool { return check.Name == name }) {
					delete(graph.checks, name)
				}
			}
			next.inheritance = &graph
		}
		if err := provider.replace(&next); err != nil {
			return err
		}
	}
	slices.SortFunc(domains, func(a, b *typeDescriptor) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	for _, domain := range domains {
		for _, check := range domain.domainChecks {
			if !expressionUsesCompositeField(check.expression, source.rowType, attribute) {
				continue
			}
			label := fmt.Sprintf("constraint %s", dependencyIdentifier(check.Name))
			details = append(details, fmt.Sprintf("%s depends on column %s of composite type %s", label, dependencyIdentifier(source.columns[source.indexForAttribute(attribute)].Name), dependencyIdentifier(source.name)))
			if mode == parser.DropCascade {
				if err := provider.removeDomainCheck(domain, check.OID); err != nil {
					return err
				}
				provider.compositeFieldNotice("drop cascades to " + label)
			}
		}
	}
	if len(details) != 0 && mode == parser.DropRestrict {
		diagnostic := sqlError("2BP01", fmt.Sprintf("cannot drop column %s of composite type %s because other objects depend on it", dependencyIdentifier(source.columns[source.indexForAttribute(attribute)].Name), dependencyIdentifier(source.name))).(*sqlstate.Error)
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
func typedFieldCheckOwner(stage *CatalogStage, relation *relationDescriptor) bool {
	if stage.typedSource == (ids.ObjectAddress{}) || stage.typedObserved[relation.address] == nil {
		return false
	}
	if relation.ofType == stage.typedSource {
		return true
	}
	if relation.inheritance != nil {
		for _, address := range relation.inheritance.parents {
			parent, ok := stage.base.Relation(address)
			if ok && typedFieldCheckOwner(stage, parent) {
				return true
			}
		}
	}
	return false
}
