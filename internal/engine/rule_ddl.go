package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) executeRule(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	auth, err := e.statementAuthorizationLocked(scope.op, stmt)
	if err != nil {
		return fail(err)
	}
	var table parser.ObjectName
	var name string
	switch s := stmt.(type) {
	case *parser.CreateRule:
		table, name = s.Table, s.Name.Text
	case *parser.AlterRule:
		table, name = s.Table, s.Name.Text
	case *parser.DropRule:
		table, name = s.Table, s.Name.Text
	}
	ref, err := e.resolveRelationReference(auth, table, true)
	if err != nil {
		if s, ok := stmt.(*parser.DropRule); ok && s.IfExists && renameMissing(err) {
			return e.finishPolicyNoticeLocked(scope, fmt.Sprintf(`relation "%s" does not exist, skipping`, catalog.ObjectNameText(table)))
		}
		return fail(err)
	}
	if ref != scope.lockedRef {
		return fail(ids.ErrCatalogReplaced)
	}
	old := auth.Catalog.MustRelation(ref.Address)
	index := ruleIndex(old, name)
	if s, ok := stmt.(*parser.DropRule); ok && index < 0 && !(old.View != nil && name == "_RETURN") {
		if s.IfExists {
			return e.finishPolicyNoticeLocked(scope, fmt.Sprintf(`rule "%s" for relation "%s" does not exist, skipping`, name, old.Name()))
		}
		return fail(ruleMissing(name, old))
	}
	if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return fail(err)
	}
	if old.RelationKind == catalog.MaterializedViewRelation {
		return fail(sqlError("0A000", "rules on materialized views are not supported"))
	}
	if old.RelationKind != catalog.TableRelation && old.RelationKind != catalog.PartitionedRelation && old.RelationKind != catalog.ViewRelation {
		return fail(analyze.TableKindError(old, "change"))
	}
	next := *old
	next.Rules = rulesPayloadOf(slices.Clone(rulesOf(old)))
	var sources []exec.Leaf
	switch s := stmt.(type) {
	case *parser.CreateRule:
		if strings.EqualFold(s.Event, "SELECT") {
			if old.RelationKind != catalog.ViewRelation {
				return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`relation "%s" cannot have ON SELECT rules`, old.Name()), Detail: "This operation is not supported for tables."})
			}
			if !s.Instead || len(s.Actions) == 0 {
				return fail(sqlError("0A000", "rules on SELECT must have action INSTEAD SELECT"))
			}
			if len(s.Actions) > 1 {
				return fail(sqlError("0A000", "multiple actions for rules on SELECT are not implemented"))
			}
			query, ok := s.Actions[0].(*parser.Select)
			if !ok {
				return fail(sqlError("0A000", "rules on SELECT must have action INSTEAD SELECT"))
			}
			if s.Condition != nil {
				return fail(sqlError("0A000", "event qualifications are not implemented for rules on SELECT"))
			}
			if name != "_RETURN" {
				return fail(sqlError("42P17", fmt.Sprintf(`view rule for "%s" must be named "_RETURN"`, old.Name())))
			}
			if !s.Replace {
				return fail(sqlError("55000", fmt.Sprintf(`"%s" is already a view`, old.Name())))
			}
			creation := &parser.CreateView{Name: s.Table, Query: query, Replace: true, Pos: s.Pos}
			var analysis parser.Statement = creation
			if parser.HasModifyingCTE(query) {
				analysis = query
			}
			b, err := describeWithRule(scope.op.Context(), e.analysisHost(scope.op), analysis, auth, ids.ObjectRef{}, scope.input, nil, nil, &analyze.RuleNamespace{Relation: old, Ref: ref, Event: "SELECT"})
			if err != nil {
				return fail(err)
			}
			if parser.HasModifyingCTE(query) {
				return fail(sqlError("0A000", "rules on SELECT must not contain data-modifying statements in WITH"))
			}
			if err := ruleResultCheck(old, b.Query.Root.Schema(), true); err != nil {
				return fail(err)
			}
			temporary := viewOf(old).temporary != (ids.SessionToken{})
			definition, err := e.freezeViewDefinitionLocked(auth, query, b, viewOf(old).barrier, viewOf(old).invoker, viewOf(old).rule, catalog.ViewRelation, temporary)
			if err != nil {
				return fail(err)
			}
			definition.options = viewOf(old).options
			definition.checkOption = viewOf(old).checkOption
			next.View = viewPayloadOf(definition)
			sources = b.QuerySources
		} else {
			if name == "_RETURN" {
				return fail(sqlError("42P17", "non-view rule must not be named \"_RETURN\""))
			}
			if index >= 0 && !s.Replace {
				return fail(ruleDuplicate(name, old))
			}
			definition, err := e.bindRuleDefinitionLocked(scope, auth, old, ref, s)
			if err != nil {
				return fail(err)
			}
			if index >= 0 {
				definition.OID = rulesOf(old)[index].OID
				rulesOf(&next)[index] = definition
			} else {
				definition.OID, err = e.catalog.allocateOIDLocked()
				if err != nil {
					return fail(err)
				}
				next.Rules = rulesPayloadOf(append(rulesOf(&next), definition))
			}
			for _, action := range definition.actions {
				sources = append(sources, action.binding.QuerySources...)
			}
			if definition.conditionPresentation != nil {
				for _, source := range definition.conditionPresentation.sources {
					d := auth.Catalog.MustRelation(source.ref.Address)
					sources = append(sources, exec.Leaf{Ref: source.ref, Relation: d})
				}
			}
		}
	case *parser.AlterRule:
		if old.View != nil && name == "_RETURN" {
			return fail(sqlError("42P17", "renaming an ON SELECT rule is not allowed"))
		}
		if index < 0 {
			return fail(ruleMissing(name, old))
		}
		if ruleIndex(old, s.Rename.Text) >= 0 || old.View != nil && s.Rename.Text == "_RETURN" {
			return fail(ruleDuplicate(s.Rename.Text, old))
		}
		rulesOf(&next)[index].Name = s.Rename.Text
	case *parser.DropRule:
		if old.View != nil && name == "_RETURN" {
			return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: fmt.Sprintf("cannot drop rule _RETURN on view %s because view %s requires it", old.Name(), old.Name()), Hint: fmt.Sprintf("You can drop view %s instead.", old.Name())})
		}
		next.Rules = rulesPayloadOf(append(rulesOf(&next)[:index], rulesOf(&next)[index+1:]...))
	}
	slices.SortFunc(rulesOf(&next), func(a, b ruleDefinition) int { return strings.Compare(a.Name, b.Name) })
	next.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		return fail(err)
	}
	if next.View != old.View {
		viewOf(&next).quoteRevision = next.Revision()
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.session)
	for _, source := range sources {
		stage.querySources = append(stage.querySources, source.Relation)
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	if err = stage.Replace(&next); err == nil {
		var prepared *PreparedCatalogPublication
		prepared, err = e.ValidateCatalogStage(scope.op.Context(), stage)
		if err == nil {
			_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: ref.Epoch})
		}
	}
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	return Result{}, nil
}
