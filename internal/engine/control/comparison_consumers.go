package control

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
)

func checkComparisonNamespace(ctx context.Context, view catalog.Read, roles authorization.Read, owner authorization.PrincipalRef, namespace ids.ObjectAddress) error {
	ns, ok := view.Namespace(namespace)
	if !ok || ns.Temporary != (ids.SessionToken{}) {
		return InvalidComparisonContext()
	}
	actor, err := roles.Descriptor(owner)
	if err != nil {
		return err
	}
	if actor.Superuser {
		return nil
	}
	principals, err := roles.RoleClosure(ctx, owner, true)
	if err != nil {
		return err
	}
	rights := authorization.ACLRights(ns.ACL, ns.Owner, principals, authorization.UsagePrivilege, false) | access.PredefinedPrivileges(ns, principals)
	if rights&authorization.UsagePrivilege == 0 {
		return fmt.Errorf("stuntgres: comparison binding owner lacks namespace USAGE")
	}
	return nil
}

// visitBindingConsumers associates actual typed roots with the DDL sites already
// used for cast replay. It does not inspect generated SQL or quoted routine text.
func (s *dumpScript) visitBindingConsumers(visit func(castConsumer, authorization.PrincipalRef, expr.Expr) error) error {
	query := func(site castConsumer, owner authorization.PrincipalRef, root plan.Node) error {
		var failure error
		plan.VisitCTEQuery(root, func(plan.Node) {}, func(node expr.Expr) {
			if failure == nil {
				failure = visit(site, owner, node)
			}
		}, true)
		return failure
	}
	for _, descriptor := range s.CatalogView.Descriptors() {
		if ns, ok := s.CatalogView.Namespace(descriptor.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) {
			continue
		}
		event := dump.Event{OID: descriptor.Address().ObjectID}
		switch d := descriptor.(type) {
		case *catalog.Relation:
			if v := stored.ViewOf(d); v != nil {
				if err := query(castConsumer{Event: event}, d.Owner, v.Query().Root); err != nil {
					return err
				}
			}
			if d.SQLIndex != nil {
				for _, key := range d.SQLIndex.Keys {
					if key.Derived != nil {
						if err := visit(castConsumer{Event: event}, d.Owner, key.Derived.Execution); err != nil {
							return err
						}
					}
				}
				if d.SQLIndex.Predicate != nil {
					if err := visit(castConsumer{Event: event}, d.Owner, d.SQLIndex.Predicate.Execution); err != nil {
						return err
					}
				}
			}
			at, tableExists := s.TableIndices[event.OID]
			for attribute, def := range d.ColumnMeta.Defaults {
				site := castConsumer{Event: event}
				if tableExists {
					provenance := s.Tables[at].ColumnProvenance(attribute)
					if !provenance.ExplicitDefault && !(s.DeferDefaults && s.Tables[at].DeferredDefinitions[attribute]) {
						continue
					}
					if !provenance.Local {
						site.Attribute = attribute
					}
				}
				if tableExists && s.DeferDefaults && s.Tables[at].DeferredDefinitions[attribute] {
					site.Event.Phase = dump.Defaults
					site.Attribute = attribute
				}
				if d.Foreign != nil && s.ForeignDefaults[event.OID][attribute] {
					site.Attribute = attribute
					site.ForeignDefault = true
				}
				site = s.attributeConsumer(site, attribute, "")
				if err := visit(site, d.Owner, def.Expression); err != nil {
					return err
				}
			}
			for attribute, generation := range d.ColumnMeta.Generations {
				site := castConsumer{Event: event}
				if tableExists {
					provenance := s.Tables[at].ColumnProvenance(attribute)
					if s.Tables[at].RecipePlaceholders {
						site.Event.Phase = dump.InheritanceRecipes
						site.Attribute = attribute
					} else if !provenance.Local || !provenance.ExplicitGeneration {
						continue
					}
				}
				site = s.attributeConsumer(site, attribute, "")
				if err := visit(site, d.Owner, generation.Expression); err != nil {
					return err
				}
			}
			for _, check := range d.Constraints.Check {
				if !table.DumpCheckDeclaration(s.OriginalCatalog, d, check) {
					continue
				}
				site := castConsumer{Event: event}
				if check.NotValid {
					site.Event.Phase = dump.InvalidConstraints
					site.Check = check.Name
				} else if tableExists && s.Tables[at].DeferredChecks[check.Name] {
					site.Event.Phase = dump.InheritanceChecks
					site.Check = check.Name
				}
				site = s.attributeConsumer(site, 0, check.Name)
				if err := visit(site, d.Owner, check.Expression); err != nil {
					return err
				}
			}
		case *catalog.Type:
			if d.DomainDefault != nil {
				if err := visit(castConsumer{Event: event}, d.Owner, d.DomainDefault.Expression); err != nil {
					return err
				}
			}
			for _, check := range d.DomainChecks {
				site := castConsumer{Event: event}
				if check.NotValid || s.deferredDomainChecks[event.OID][check.Name] {
					site.Event.Phase = dump.DomainChecks
					site.Check = check.Name
				}
				if err := visit(site, d.Owner, check.Expression); err != nil {
					return err
				}
			}
		case *catalog.Procedure:
			if d.SQL != nil && d.SQLDefaults != nil {
				for slot := range d.SQL.Inputs {
					if node, ok := d.SQLDefaults.Default(slot); ok {
						if err := visit(castConsumer{Event: event}, d.Owner, node); err != nil {
							return err
						}
					}
				}
			}
			if d.SQL != nil && d.SQL.Standard {
				program, err := stored.RoutineProgramOf(d)
				if err != nil {
					return err
				}
				for i := range program.Len() {
					binding := program.Action(i).Binding()
					var failure error
					stored.VisitRuleActionExpressions(binding, func(node expr.Expr) {
						if failure == nil {
							failure = visit(castConsumer{Event: event}, d.Owner, node)
						}
					})
					if failure != nil {
						return failure
					}
					if binding.Query != nil {
						if err := query(castConsumer{Event: event}, d.Owner, binding.Query.Root); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	return nil
}

func (s *dumpScript) prepareComparisonBindings(ctx context.Context) error {
	err := s.visitBindingConsumers(func(site castConsumer, owner authorization.PrincipalRef, node expr.Expr) error {
		namespace, err := stored.ComparisonBindingNamespace(s.CatalogView, node)
		if err != nil {
			return err
		}
		if namespace == (ids.ObjectAddress{}) {
			return nil
		}
		if err := checkComparisonNamespace(ctx, s.CatalogView, s.DomainRoles, owner, namespace); err != nil {
			return err
		}
		if old := s.comparisonBindings[site]; old != nil {
			if old.CubeNamespace != namespace {
				return InvalidComparisonContext()
			}
			return nil
		}
		if s.comparisonBindings == nil {
			s.comparisonBindings = make(map[castConsumer]*dump.DefinitionBinding)
		}
		s.comparisonBindings[site] = &dump.DefinitionBinding{CubeNamespace: namespace}
		addresses := [3]ids.ObjectAddress{namespace}
		count := 1
		for _, name := range []string{"cube", "hstore"} {
			installation, exists := s.CatalogView.Extension(s.CatalogView.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: name}))
			if exists && installation.TargetNamespace == namespace {
				// The typed capture above authenticates the provider. Retain
				// every installed comparison provider in this namespace so a
				// shared replay binding orders after both installations.
				addresses[count] = installation.Address()
				count++
			}
		}
		if count == 1 {
			return InvalidComparisonContext()
		}
		for _, address := range addresses[:count] {
			oid, err := predecessor(s.CatalogView, address, ids.ObjectAddress{})
			if err != nil {
				return err
			}
			if _, ordered := s.Graph[site.Event]; ordered && oid != 0 {
				s.Graph.Add(site.Event, dump.Event{OID: oid})
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, groups := range [][]dump.Group{s.policyGroups, s.ruleGroups} {
		for _, group := range groups {
			if group.Binding == nil {
				continue
			}
			owner, exists := s.DomainRoles.Lookup(group.Owner)
			if !exists {
				return InvalidComparisonContext()
			}
			if err := checkComparisonNamespace(ctx, s.CatalogView, s.DomainRoles, owner.Ref, group.Binding.CubeNamespace); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *dumpScript) emitBound(w *dumpWriter, site castConsumer, sql string) error {
	return w.emitDefinition(s.CatalogView, dump.Statement{SQL: sql, Binding: s.comparisonBindings[site]})
}
