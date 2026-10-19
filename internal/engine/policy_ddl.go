package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) executePolicy(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	op := scope.op
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	auth, err := e.statementAuthorizationLocked(op, stmt)
	if err != nil {
		return fail(err)
	}
	var table parser.ObjectName
	var name string
	var rawRoles []parser.GrantGrantee
	var rawUsing, rawCheck parser.Expr
	create, alter, drop := false, false, false
	switch s := stmt.(type) {
	case *parser.CreatePolicy:
		table, name, rawRoles, rawUsing, rawCheck = s.Table, s.Name.Text, s.Roles, s.Using, s.Check
		create = true
	case *parser.AlterPolicy:
		table, name, rawRoles, rawUsing, rawCheck = s.Table, s.Name.Text, s.Roles, s.Using, s.Check
		alter = true
	case *parser.DropPolicy:
		table, name = s.Table, s.Name.Text
		drop = true
	}
	if err = policyPreflight(op, auth, stmt, false); err != nil {
		return fail(err)
	}
	roles, _, err := resolvePolicyRoles(auth, rawRoles)
	if err != nil {
		return fail(err)
	}
	ref, err := e.resolveRelationReference(auth, table, true)
	if s, ok := stmt.(*parser.DropPolicy); ok && s.IfExists && renameMissing(err) {
		message := fmt.Sprintf(`relation "%s" does not exist, skipping`, table.Parts[len(table.Parts)-1].Text)
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "3F000" {
			message = fmt.Sprintf(`schema "%s" does not exist, skipping`, table.Parts[0].Text)
		}
		return e.finishPolicyNoticeLocked(scope, message)
	}
	if err != nil {
		return fail(err)
	}
	if ref != scope.lockedRef {
		return fail(ids.ErrCatalogReplaced)
	}
	old := auth.Catalog.MustRelation(ref.Address)
	index := policyIndex(old, name)
	if drop && index < 0 {
		if stmt.(*parser.DropPolicy).IfExists {
			return e.finishPolicyNoticeLocked(scope, fmt.Sprintf(`policy "%s" for relation "%s" does not exist, skipping`, name, old.Name()))
		}
		return fail(policyMissing(name, old))
	}
	if err = auth.Check(AccessRequirement{Object: ref, OwnerOnly: true, OwnerNoun: policyOwnerNoun(stmt)}); err != nil {
		return fail(err)
	}
	if old.RelationKind == catalog.CatalogRelation {
		return fail(sqlError("42501", fmt.Sprintf(`permission denied: "%s" is a system catalog`, old.Name())))
	}
	if old.RelationKind != catalog.TableRelation && old.RelationKind != catalog.PartitionedRelation {
		return fail(sqlError("42809", fmt.Sprintf(`"%s" is not a table`, old.Name())))
	}
	var using, check *policyExpression
	if !drop {
		using, err = e.bindPolicyExpression(scope, auth, old, rawUsing)
		if err == nil {
			check, err = e.bindPolicyExpression(scope, auth, old, rawCheck)
		}
		if err != nil {
			return fail(err)
		}
	}
	next := *old
	next.Security.Policies = slices.Clone(old.Security.Policies)
	if create {
		if index >= 0 {
			return fail(policyDuplicate(name, old))
		}
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return fail(err)
		}
		s := stmt.(*parser.CreatePolicy)
		next.Security.Policies = append(next.Security.Policies, catalog.PolicyDefinition{
			OID: oid, Name: name, Permissive: s.Permissive, Command: catalog.PolicyCommand(s.Command),
			Roles: roles, Using: policyPayloadOf(using), Check: policyPayloadOf(check),
		})
	} else if alter {
		s := stmt.(*parser.AlterPolicy)
		if s.Rename != nil && policyIndex(old, s.Rename.Text) >= 0 {
			return fail(policyDuplicate(s.Rename.Text, old))
		}
		if index < 0 {
			return fail(policyMissing(name, old))
		}
		if err = policyClauses(old.Security.Policies[index].Command, rawUsing, rawCheck, false); err != nil {
			return fail(err)
		}
		p := &next.Security.Policies[index]
		if s.Rename != nil {
			p.Name = s.Rename.Text
		}
		if rawRoles != nil {
			p.Roles = roles
		}
		if rawUsing != nil {
			p.Using = policyPayloadOf(using)
		}
		if rawCheck != nil {
			p.Check = policyPayloadOf(check)
		}
	} else {
		next.Security.Policies = append(next.Security.Policies[:index], next.Security.Policies[index+1:]...)
	}
	slices.SortFunc(next.Security.Policies, func(a, b catalog.PolicyDefinition) int { return strings.Compare(a.Name, b.Name) })
	next.ObjectRevision, err = e.catalog.revisionLocked()
	if err != nil {
		return fail(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.session)
	for _, p := range next.Security.Policies {
		for _, expression := range []*policyExpression{policyExpressionOf(p.Using), policyExpressionOf(p.Check)} {
			if expression == nil {
				continue
			}
			for _, leaf := range expression.presentation.sources {
				if descriptor, ok := auth.Catalog.Descriptor(leaf.ref.Address); ok {
					stage.querySources = append(stage.querySources, descriptor)
				}
			}
		}
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	if err = stage.Replace(&next); err == nil {
		var prepared *PreparedCatalogPublication
		prepared, err = e.ValidateCatalogStage(op.Context(), stage)
		if err == nil {
			_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: ref.Epoch})
		}
	}
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	return Result{}, nil
}

// Engine.mu is held on entry and released on every return. Even a missing-object
// success observes final cancellation/session/epoch fences before its notice.
func (e *Engine) finishPolicyNoticeLocked(scope *StatementScope, message string) (Result, error) {
	op := scope.op
	op.session.mu.Lock()
	err := publicationOperationLocked(e, op)
	op.session.mu.Unlock()
	if err == nil {
		err = op.Context().Err()
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message})
	return Result{}, nil
}
