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

// RulePlan retains one rule definition between engine-owned admission phases.
// Every method uses the Host lock contract; none admits locks or publishes.
type RulePlan struct {
	stmt      parser.Statement
	ref       ids.ObjectRef
	old       *catalog.Relation
	next      catalog.Relation
	index     int
	parts     stored.RuleParts
	providers int
	skip      string
	sources   []catalog.Descriptor
}

// BeginRule checks the exact admitted event identity and definition preflight.
func BeginRule(h Host, auth ddl.Authority, stmt parser.Statement, locked ids.ObjectRef) (*RulePlan, error) {
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
			return &RulePlan{skip: fmt.Sprintf(`relation "%s" does not exist, skipping`, catalog.ObjectNameText(tbl))}, nil
		}
		return nil, err
	}
	if ref != locked {
		return nil, ids.ErrCatalogReplaced
	}
	catalogAccess := h.AccessView(auth)
	old := catalogAccess.Catalog.MustRelation(ref.Address)
	index := RuleIndex(old, name)
	if s, ok := stmt.(*parser.DropRule); ok && index < 0 && !(old.View != nil && name == "_RETURN") {
		if s.IfExists {
			return &RulePlan{skip: fmt.Sprintf(`rule "%s" for relation "%s" does not exist, skipping`, name, old.Name())}, nil
		}
		return nil, RuleMissing(name, old)
	}
	if err := catalogAccess.Check(auth, access.Requirement{Object: ref, OwnerOnly: true, OwnerNoun: PolicyOwnerNoun(stmt)}); err != nil {
		return nil, err
	}
	if old.RelationKind == catalog.MaterializedViewRelation {
		return nil, sqlError("0A000", "rules on materialized views are not supported")
	}
	if old.RelationKind != catalog.TableRelation && old.RelationKind != catalog.PartitionedRelation && old.RelationKind != catalog.ViewRelation {
		return nil, analyze.TableKindError(old, "change")
	}
	next := *old
	next.Rules = stored.RulesPayload(slices.Clone(stored.RulesOf(old)))
	p := &RulePlan{stmt: stmt, ref: ref, old: old, next: next, index: index}
	switch s := stmt.(type) {
	case *parser.CreateRule:
		if strings.EqualFold(s.Event, "SELECT") {
			if err := checkSelectRule(s, old, name); err != nil {
				return nil, err
			}
		} else {
			if name == "_RETURN" {
				return nil, sqlError("42P17", "non-view rule must not be named \"_RETURN\"")
			}
			if index >= 0 && !s.Replace {
				return nil, RuleDuplicate(name, old)
			}
			p.parts = stored.RuleParts{Name: name, Event: strings.ToUpper(s.Event), Instead: s.Instead, Mode: catalog.RuleOrigin}
		}
	case *parser.AlterRule:
		if old.View != nil && name == "_RETURN" {
			return nil, sqlError("42P17", "renaming an ON SELECT rule is not allowed")
		}
		if index < 0 {
			return nil, RuleMissing(name, old)
		}
		if RuleIndex(old, s.Rename.Text) >= 0 || old.View != nil && s.Rename.Text == "_RETURN" {
			return nil, RuleDuplicate(s.Rename.Text, old)
		}
		stored.RulesOf(&p.next)[index].Name = s.Rename.Text
	case *parser.DropRule:
		if old.View != nil && name == "_RETURN" {
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop rule _RETURN on view %s because view %s requires it", old.Name(), old.Name()), Hint: fmt.Sprintf("You can drop view %s instead.", old.Name())}
		}
		p.next.Rules = stored.RulesPayload(append(stored.RulesOf(&p.next)[:index], stored.RulesOf(&p.next)[index+1:]...))
	}
	return p, nil
}

// Finish consumes the already frozen definition without binding it again.
func (p *RulePlan) Finish(h Host, auth ddl.Authority) (RelationChange, error) {
	if p.skip != "" {
		return RelationChange{Skip: p.skip}, nil
	}
	if _, create := p.stmt.(*parser.CreateRule); create && p.parts.Event != "" {
		var err error
		p.parts.References, err = RuleReferences(h.Context(), h.AccessView(auth).Catalog, stored.NewRule(p.parts))
		if err != nil {
			return RelationChange{}, err
		}
		if p.index >= 0 {
			p.parts.OID = stored.RulesOf(p.old)[p.index].OID
			p.parts.Mode = stored.RulesOf(p.old)[p.index].Mode()
			stored.RulesOf(&p.next)[p.index] = stored.NewRule(p.parts)
		} else {
			p.parts.OID, err = h.AllocateOID()
			if err != nil {
				return RelationChange{}, err
			}
			p.next.Rules = stored.RulesPayload(append(stored.RulesOf(&p.next), stored.NewRule(p.parts)))
		}
		for _, address := range p.parts.References {
			if address.ClassID != ids.RelationClassID {
				continue
			}
			address.SubID = 0
			source := h.AccessView(auth).Catalog.MustRelation(address)
			if !slices.Contains(p.sources, catalog.Descriptor(source)) {
				p.sources = append(p.sources, source)
			}
		}
	}
	slices.SortFunc(stored.RulesOf(&p.next), func(a, b stored.Rule) int { return strings.Compare(a.Name, b.Name) })
	var err error
	p.next.ObjectRevision, err = h.Revision()
	if err != nil {
		return RelationChange{}, err
	}
	if p.next.View != p.old.View {
		p.next.View = stored.ViewOf(&p.next).With(func(v *stored.ViewParts) { v.DefinitionRevision = p.next.Revision() })
	}
	change := RelationChange{Ref: p.ref, Next: &p.next, Sources: p.sources}
	if _, drop := p.stmt.(*parser.DropRule); drop {
		change.Deleted = ids.ObjectAddress{ClassID: 2618, ObjectID: stored.RulesOf(p.old)[p.index].OID}
	} else if _, create := p.stmt.(*parser.CreateRule); create {
		change.CommandAddress = ids.ObjectAddress{ClassID: 2618, ObjectID: p.parts.OID}
		if p.parts.Event == "" {
			// ON SELECT replaces the view's real _RETURN rule rather than
			// constructing an ordinary rule payload.
			change.CommandAddress.ObjectID = stored.ViewOf(&p.next).RuleOID()
		}
		change.CommandTag = "CREATE RULE"
	} else {
		change.CommandAddress = ids.ObjectAddress{ClassID: 2618, ObjectID: stored.RulesOf(p.old)[p.index].OID}
		change.CommandTag = "ALTER RULE"
	}
	return change, nil
}

// checkSelectRule preserves the structural checks before source admission.
func checkSelectRule(s *parser.CreateRule, old *catalog.Relation, name string) error {
	if old.RelationKind != catalog.ViewRelation {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`relation "%s" cannot have ON SELECT rules`, old.Name()), Detail: "This operation is not supported for tables."}
	}
	if !s.Instead || len(s.Actions) == 0 {
		return sqlError("0A000", "rules on SELECT must have action INSTEAD SELECT")
	}
	if len(s.Actions) > 1 {
		return sqlError("0A000", "multiple actions for rules on SELECT are not implemented")
	}
	_, ok := s.Actions[0].(*parser.Select)
	if !ok {
		return sqlError("0A000", "rules on SELECT must have action INSTEAD SELECT")
	}
	if s.Condition != nil {
		return sqlError("0A000", "event qualifications are not implemented for rules on SELECT")
	}
	if name != "_RETURN" {
		return sqlError("42P17", fmt.Sprintf(`view rule for "%s" must be named "_RETURN"`, old.Name()))
	}
	if !s.Replace {
		return sqlError("55000", fmt.Sprintf(`"%s" is already a view`, old.Name()))
	}
	return nil
}

func (p *RulePlan) bindSelectAction(h Host, auth ddl.Authority, input sqltype.InputEnvironment) (analyze.Binding, error) {
	s := p.stmt.(*parser.CreateRule)
	old, ref := p.old, p.ref
	query := s.Actions[0].(*parser.Select)
	creation := &parser.CreateView{Name: s.Table, Query: query, Replace: true, Pos: s.Pos}
	var analysis parser.Statement = creation
	if parser.HasModifyingCTE(query) {
		analysis = query
	}
	b, err := h.DescribeRule(auth, analysis, ids.ObjectRef{}, input, &analyze.RuleNamespace{Relation: old, Ref: ref, Event: "SELECT"})
	if err != nil {
		return b, err
	}
	if parser.HasModifyingCTE(query) {
		return b, sqlError("0A000", "rules on SELECT must not contain data-modifying statements in WITH")
	}
	if err := RuleResultCheck(old, b.Query.Root.Schema(), true); err != nil {
		return b, err
	}
	current := stored.ViewOf(old)
	temporary := current.Temporary() != (ids.SessionToken{})
	definition, err := FreezeDefinition(h, h.AccessView(auth).Catalog, h.TemporaryOwner(auth), query, b, current.Barrier(), current.Invoker(), current.RuleOID(), catalog.ViewRelation, temporary)
	if err != nil {
		return b, err
	}
	definition = definition.With(func(p *stored.ViewParts) {
		p.Options = current.Options()
		p.CheckOption = current.CheckOption()
	})
	p.next.View = stored.ViewPayload(definition)
	for _, source := range b.QuerySources {
		p.sources = append(p.sources, source.Relation)
	}
	return b, nil
}
