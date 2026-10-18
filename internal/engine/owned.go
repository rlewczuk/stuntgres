package engine

import (
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func ownedSources(auth AuthorizationView, specs []parser.RoleSpec, reassign bool) (map[authorization.PrincipalRef]bool, authorization.PrincipalRef, error) {
	refs := make(map[authorization.PrincipalRef]bool, len(specs))
	protected := authorization.PrincipalRef{}
	for _, spec := range specs {
		ref, err := authorization.MembershipRole(auth.roles, auth.state, spec)
		if err != nil {
			return nil, authorization.PrincipalRef{}, err
		}
		role, err := auth.roles.Descriptor(ref)
		if err != nil {
			return nil, authorization.PrincipalRef{}, err
		}
		allowed, err := auth.roles.InheritsRole(auth.state.CurrentUser, ref)
		if err != nil {
			return nil, authorization.PrincipalRef{}, err
		}
		if !allowed {
			message := "permission denied to drop objects"
			detail := "Only roles with privileges of role \"" + role.Name + "\" may drop objects owned by it."
			if reassign {
				message = "permission denied to reassign objects"
				detail = "Only roles with privileges of role \"" + role.Name + "\" may reassign objects owned by it."
			}
			return nil, authorization.PrincipalRef{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: message, Detail: detail}
		}
		if (role.Predefined() || ref == auth.engine.authorizationDomain.Principal(authorization.BootstrapPrincipalID)) && protected == (authorization.PrincipalRef{}) {
			protected = ref
		}
		refs[ref] = true
	}
	return refs, protected, nil
}
func ownedRoots(catalog CatalogRead, sources map[authorization.PrincipalRef]bool) []ObjectAddress {
	var roots []ObjectAddress
	for a, d := range catalog.state.descriptors {
		owner, _, _ := descriptorACL(d)
		if sources[owner] && ownershipRoot(catalog, d) {
			roots = append(roots, a)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].ObjectID != roots[j].ObjectID {
			return roots[i].ObjectID < roots[j].ObjectID
		}
		return roots[i].ClassID < roots[j].ClassID
	})
	return roots
}

// Ownership locks follow canonical associations, not generated names. Include
// independent roots and their backing relations, indexes and owned sequences.
func ownershipRelationLocks(catalog CatalogRead, roots []ObjectAddress) []ObjectAddress {
	group := make(map[ObjectAddress]bool, len(roots))
	for _, a := range roots {
		switch d := catalog.state.descriptors[a].(type) {
		case *relationDescriptor:
			group[a] = true
		case *typeDescriptor:
			if d.owningRelation != (ObjectAddress{}) {
				group[d.owningRelation] = true
			}
		}
	}
	for a, d := range catalog.state.descriptors {
		r, ok := d.(*relationDescriptor)
		if !ok {
			continue
		}
		if r.sqlIndex != nil && group[r.sqlIndex.parent] {
			group[a] = true
		}
		if r.sequence != nil {
			parent := r.sequence.owned
			parent.SubID = 0
			if group[parent] {
				group[a] = true
			}
		}
	}
	locks := make([]ObjectAddress, 0, len(group))
	for a := range group {
		locks = append(locks, a)
	}
	sort.Slice(locks, func(i, j int) bool { return locks[i].ObjectID < locks[j].ObjectID })
	return locks
}
func (e *Engine) executeOwned(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return fail(err)
	}
	var specs []parser.RoleSpec
	reassign, target, mode := false, authorization.PrincipalRef{}, parser.DropRestrict
	switch s := stmt.(type) {
	case *parser.ReassignOwned:
		reassign = true
		specs = s.Sources
	case *parser.DropOwned:
		specs = s.Sources
		mode = s.Mode
	}
	sources, protected, err := ownedSources(auth, specs, reassign)
	if err != nil {
		return fail(err)
	}
	if reassign {
		s := stmt.(*parser.ReassignOwned)
		target, err = authorization.MembershipRole(auth.roles, auth.state, s.Target)
		if err != nil {
			return fail(err)
		}
		allowed, err := auth.roles.InheritsRole(auth.state.CurrentUser, target)
		if err != nil {
			return fail(err)
		}
		if !allowed {
			role, _ := auth.roles.Descriptor(target)
			return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied to reassign objects", Detail: fmt.Sprintf(`Only roles with privileges of role "%s" may reassign objects to it.`, role.Name)})
		}
	}
	// PostgreSQL resolves and authorizes utility operands before entering the
	// shared-dependency handler that refuses protected sources.
	if protected != (authorization.PrincipalRef{}) {
		role, _ := auth.roles.Descriptor(protected)
		noun := "drop objects owned by"
		if reassign {
			noun = "reassign ownership of objects owned by"
		}
		return fail(sqlError("2BP01", fmt.Sprintf(`cannot %s role %s because they are required by the database system`, noun, role.Name)))
	}
	if reassign && len(sources) == 1 && sources[target] {
		e.mu.Unlock()
		scope.completed = true
		return Result{}, scope.op.Context().Err()
	}
	roots := ownedRoots(auth.catalog, sources)
	if reassign {
		local := roots[:0]
		for _, address := range roots {
			if address.ClassID != ForeignMappingClassID {
				local = append(local, address)
			}
		}
		roots = local
	}
	primaryRoot := ObjectAddress{}
	if !reassign {
		local := roots[:0]
		for _, a := range roots {
			if a.ClassID != DatabaseClassID {
				local = append(local, a)
			}
		}
		roots = local
		if len(local) == 1 {
			primaryRoot = local[0]
		}
		path, _ := e.referencePath(auth)
		pending := make(map[ObjectAddress]bool, len(roots))
		for _, a := range roots {
			pending[a] = true
		}
		for _, a := range roots {
			var extension ObjectAddress
			switch d := auth.catalog.state.descriptors[a].(type) {
			case *procedureDescriptor:
				extension = d.extension
			case *tsDictionaryDescriptor:
				extension = d.extension
			}
			if extension != (ObjectAddress{}) && !pending[extension] {
				d := auth.catalog.state.descriptors[extension].(*extensionDescriptor)
				return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + dependencyObject(auth.catalog, a, path) + " because extension " + dependencyIdentifier(d.name) + " requires it", Hint: "You can drop extension " + dependencyIdentifier(d.name) + " instead."})
			}
		}
		end := len(roots)
		for _, a := range roots[:end] {
			if extension, ok := auth.catalog.state.descriptors[a].(*extensionDescriptor); ok {
				for _, member := range extension.members {
					if !pending[member] {
						roots = append(roots, member)
						pending[member] = true
					}
				}
			}
		}
	}
	stage := e.ownershipStage(auth, scope.op)
	stage.dependencyPath, _ = e.referencePath(auth)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	// Pin roots and automatic associations before producing private candidates.
	for _, a := range ownershipRelationLocks(stage.base, roots) {
		if err = scope.op.Context().Err(); err != nil {
			return Result{}, err
		}
		ref := ObjectRef{owner: e.domain, Epoch: stage.base.Epoch(), Address: a}
		if err = scope.acquireSequence(ref, accessExclusive, true); err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	providersChanged := false
	if reassign {
		for _, a := range roots {
			d := stage.base.state.descriptors[a]
			owner, _, _ := descriptorACL(d)
			if owner == target {
				continue
			}
			// PostgreSQL's schema and generic collation handlers retain their
			// own eligibility checks; relation/type bulk recursion does not SET.
			switch d.(type) {
			case *foreignDescriptor:
				foreign := d.(*foreignDescriptor)
				err = foreignOwnershipEligibility(auth, foreign, target)
				if err == nil && foreign.address.ClassID == ForeignServerClassID {
					stage.domainBases = append(stage.domainBases, auth.catalog.state.descriptors[foreign.wrapper])
				}
			case *namespaceDescriptor:
				err = auth.Check(AccessRequirement{Object: ObjectRef{owner: e.domain, Epoch: stage.base.Epoch(), Address: a}, OwnerOnly: true})
				if err == nil {
					err = ownershipSuccessor(auth, target)
				}
				if err == nil {
					err = ownershipDatabaseCreate(auth)
				}
			case *collationDescriptor, *conversionDescriptor, *operatorDescriptor, *databaseDescriptor, *extensionDescriptor, *procedureDescriptor, *tsDictionaryDescriptor:
				err = auth.Check(AccessRequirement{Object: ObjectRef{owner: e.domain, Epoch: stage.base.Epoch(), Address: a}, OwnerOnly: true})
				if err == nil {
					err = ownershipEligibility(auth, target, d.Namespace())
				}
			}
			if err != nil {
				break
			}
			if err = stageOwnership(stage, d, target); err != nil {
				break
			}
			switch d.(type) {
			case *procedureDescriptor, *operatorDescriptor, *tsDictionaryDescriptor:
				providersChanged = true
			}
		}
		if err == nil && providersChanged {
			candidate := e.stageRead(stage).state
			err = e.reownCatalogExpressions(candidate, nil, false)
			if err == nil {
				for a, d := range candidate.descriptors {
					if old := stage.descriptors[a]; old != nil {
						if old == d {
							continue
						}
					} else if stage.base.state.descriptors[a] == d {
						continue
					}
					if err = stage.Replace(d); err != nil {
						break
					}
				}
			}
		}
	} else {
		for _, a := range roots {
			if err = stage.removeObjectLocked(ObjectRef{owner: e.domain, Epoch: stage.base.Epoch(), Address: a}, a.ClassID == NamespaceClassID); err != nil {
				break
			}
		}
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if !reassign {
		// PostgreSQL collects actual policy and grantor-membership deletions
		// alongside ownership roots. Prepare cleanup once, still entirely private,
		// before discovering consumers of the complete pending deletion union.
		if err = stageOwnedCleanup(scope.op.Context(), stage, auth.roles, auth.roleRecord, sources); err != nil {
			return Result{}, err
		}
		if primaryRoot != (ObjectAddress{}) && ownedCleanupAddsDeletionRoot(stage, sources) {
			primaryRoot = ObjectAddress{}
		}
		if err = scope.stageExpressionDependents(stage, roots, mode, nil); err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "2BP01" {
				if primaryRoot != (ObjectAddress{}) {
					diagnostic.Message = "cannot drop " + dependencyObject(stage.base, primaryRoot, stage.dependencyPath) + " because other objects depend on it"
				} else {
					diagnostic.Message = "cannot drop desired object(s) because other objects depend on them"
				}
			}
			return Result{}, err
		}
		if err = scope.stageOwnedSequences(stage, roots, mode, nil); err != nil {
			return Result{}, err
		}
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	result, err := e.publishOwnership(scope, stage)
	if err == nil && !reassign && mode == parser.DropCascade && len(stage.viewCascadeNames) > 0 {
		message, detail := stage.viewCascadeNames[0], ""
		if len(stage.viewCascadeNames) > 1 {
			message = fmt.Sprintf("drop cascades to %d other objects", len(stage.viewCascadeNames))
			detail = strings.Join(stage.viewCascadeNames, "\n")
		}
		scope.op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
	}
	return result, err
}
