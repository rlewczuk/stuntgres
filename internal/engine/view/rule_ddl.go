package view

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// PlanRule decides CREATE, ALTER or DROP RULE: it resolves the relation
// (locked is the admitted identity), checks ownership and the relation kind,
// binds and freezes a new rule or ON SELECT view query, and returns the
// relation with its rule list changed and a new revision. An IF EXISTS miss
// returns only the NOTICE text in Skip.
func PlanRule(h Host, auth ddl.Authority, stmt parser.Statement, locked ids.ObjectRef, input sqltype.InputEnvironment) (RelationChange, error) {
	var tbl parser.ObjectName
	var name string
	switch s := stmt.(type) {
	case *parser.CreateRule:
		tbl, name = s.Table, s.Name.Text
	case *parser.AlterRule:
		tbl, name = s.Table, s.Name.Text
	case *parser.DropRule:
		tbl, name = s.Table, s.Name.Text
	}
	ref, err := h.ResolveRelation(auth, tbl)
	if err != nil {
		if s, ok := stmt.(*parser.DropRule); ok && s.IfExists && table.RenameMissing(err) {
			return RelationChange{Skip: fmt.Sprintf(`relation "%s" does not exist, skipping`, catalog.ObjectNameText(tbl))}, nil
		}
		return RelationChange{}, err
	}
	if ref != locked {
		return RelationChange{}, ids.ErrCatalogReplaced
	}
	catalogAccess := h.AccessView(auth)
	old := catalogAccess.Catalog.MustRelation(ref.Address)
	index := RuleIndex(old, name)
	if s, ok := stmt.(*parser.DropRule); ok && index < 0 && !(old.View != nil && name == "_RETURN") {
		if s.IfExists {
			return RelationChange{Skip: fmt.Sprintf(`rule "%s" for relation "%s" does not exist, skipping`, name, old.Name())}, nil
		}
		return RelationChange{}, RuleMissing(name, old)
	}
	if err := catalogAccess.Check(auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return RelationChange{}, err
	}
	if old.RelationKind == catalog.MaterializedViewRelation {
		return RelationChange{}, sqlError("0A000", "rules on materialized views are not supported")
	}
	if old.RelationKind != catalog.TableRelation && old.RelationKind != catalog.PartitionedRelation && old.RelationKind != catalog.ViewRelation {
		return RelationChange{}, analyze.TableKindError(old, "change")
	}
	next := *old
	next.Rules = stored.RulesPayload(slices.Clone(stored.RulesOf(old)))
	var sources []catalog.Descriptor
	switch s := stmt.(type) {
	case *parser.CreateRule:
		if strings.EqualFold(s.Event, "SELECT") {
			definition, querySources, err := planSelectRule(h, auth, s, old, ref, name, input)
			if err != nil {
				return RelationChange{}, err
			}
			next.View = stored.ViewPayload(definition)
			sources = querySources
		} else {
			if name == "_RETURN" {
				return RelationChange{}, sqlError("42P17", "non-view rule must not be named \"_RETURN\"")
			}
			if index >= 0 && !s.Replace {
				return RelationChange{}, RuleDuplicate(name, old)
			}
			definition, err := bindRuleDefinition(h, auth, old, ref, s, input)
			if err != nil {
				return RelationChange{}, err
			}
			if index >= 0 {
				definition.OID = stored.RulesOf(old)[index].OID
				stored.RulesOf(&next)[index] = definition
			} else {
				definition.OID, err = h.AllocateOID()
				if err != nil {
					return RelationChange{}, err
				}
				next.Rules = stored.RulesPayload(append(stored.RulesOf(&next), definition))
			}
			for _, action := range definition.Actions() {
				for _, source := range action.Binding().QuerySources {
					sources = append(sources, source.Relation)
				}
			}
			if definition.ConditionPresentation() != nil {
				for _, source := range definition.ConditionPresentation().Sources() {
					sources = append(sources, catalogAccess.Catalog.MustRelation(source.Ref.Address))
				}
			}
		}
	case *parser.AlterRule:
		if old.View != nil && name == "_RETURN" {
			return RelationChange{}, sqlError("42P17", "renaming an ON SELECT rule is not allowed")
		}
		if index < 0 {
			return RelationChange{}, RuleMissing(name, old)
		}
		if RuleIndex(old, s.Rename.Text) >= 0 || old.View != nil && s.Rename.Text == "_RETURN" {
			return RelationChange{}, RuleDuplicate(s.Rename.Text, old)
		}
		stored.RulesOf(&next)[index].Name = s.Rename.Text
	case *parser.DropRule:
		if old.View != nil && name == "_RETURN" {
			return RelationChange{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop rule _RETURN on view %s because view %s requires it", old.Name(), old.Name()), Hint: fmt.Sprintf("You can drop view %s instead.", old.Name())}
		}
		next.Rules = stored.RulesPayload(append(stored.RulesOf(&next)[:index], stored.RulesOf(&next)[index+1:]...))
	}
	slices.SortFunc(stored.RulesOf(&next), func(a, b stored.Rule) int { return strings.Compare(a.Name, b.Name) })
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return RelationChange{}, err
	}
	if next.View != old.View {
		next.View = stored.ViewOf(&next).With(func(p *stored.ViewParts) { p.QuoteRevision = next.Revision() })
	}
	return RelationChange{Ref: ref, Next: &next, Sources: sources}, nil
}

// planSelectRule checks and freezes CREATE OR REPLACE RULE _RETURN AS ON
// SELECT on the view old: the new definition keeps the view's options and
// check option. It returns the definition and its query sources.
func planSelectRule(h Host, auth ddl.Authority, s *parser.CreateRule, old *catalog.Relation, ref ids.ObjectRef, name string, input sqltype.InputEnvironment) (*stored.View, []catalog.Descriptor, error) {
	if old.RelationKind != catalog.ViewRelation {
		return nil, nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`relation "%s" cannot have ON SELECT rules`, old.Name()), Detail: "This operation is not supported for tables."}
	}
	if !s.Instead || len(s.Actions) == 0 {
		return nil, nil, sqlError("0A000", "rules on SELECT must have action INSTEAD SELECT")
	}
	if len(s.Actions) > 1 {
		return nil, nil, sqlError("0A000", "multiple actions for rules on SELECT are not implemented")
	}
	query, ok := s.Actions[0].(*parser.Select)
	if !ok {
		return nil, nil, sqlError("0A000", "rules on SELECT must have action INSTEAD SELECT")
	}
	if s.Condition != nil {
		return nil, nil, sqlError("0A000", "event qualifications are not implemented for rules on SELECT")
	}
	if name != "_RETURN" {
		return nil, nil, sqlError("42P17", fmt.Sprintf(`view rule for "%s" must be named "_RETURN"`, old.Name()))
	}
	if !s.Replace {
		return nil, nil, sqlError("55000", fmt.Sprintf(`"%s" is already a view`, old.Name()))
	}
	creation := &parser.CreateView{Name: s.Table, Query: query, Replace: true, Pos: s.Pos}
	var analysis parser.Statement = creation
	if parser.HasModifyingCTE(query) {
		analysis = query
	}
	b, err := h.DescribeRule(auth, analysis, ids.ObjectRef{}, input, &analyze.RuleNamespace{Relation: old, Ref: ref, Event: "SELECT"})
	if err != nil {
		return nil, nil, err
	}
	if parser.HasModifyingCTE(query) {
		return nil, nil, sqlError("0A000", "rules on SELECT must not contain data-modifying statements in WITH")
	}
	if err := RuleResultCheck(old, b.Query.Root.Schema(), true); err != nil {
		return nil, nil, err
	}
	current := stored.ViewOf(old)
	temporary := current.Temporary() != (ids.SessionToken{})
	definition, err := FreezeDefinition(h, h.AccessView(auth).Catalog, h.TemporaryOwner(auth), query, b, current.Barrier(), current.Invoker(), current.RuleOID(), catalog.ViewRelation, temporary)
	if err != nil {
		return nil, nil, err
	}
	definition = definition.With(func(p *stored.ViewParts) {
		p.Options = current.Options()
		p.CheckOption = current.CheckOption()
	})
	sources := make([]catalog.Descriptor, 0, len(b.QuerySources))
	for _, source := range b.QuerySources {
		sources = append(sources, source.Relation)
	}
	return definition, sources, nil
}
