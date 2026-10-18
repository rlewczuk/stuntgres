package engine

import (
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
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
func ownedRoots(catalog CatalogRead, sources map[authorization.PrincipalRef]bool) []ids.ObjectAddress {
	var roots []ids.ObjectAddress
	for a, d := range catalog.Descriptors() {
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
func ownershipRelationLocks(catalog CatalogRead, roots []ids.ObjectAddress) []ids.ObjectAddress {
	group := make(map[ids.ObjectAddress]bool, len(roots))
	for _, a := range roots {
		switch d := catalog.Get(a).(type) {
		case *relationDescriptor:
			group[a] = true
		case *typeDescriptor:
			if d.OwningRelation != (ids.ObjectAddress{}) {
				group[d.OwningRelation] = true
			}
		}
	}
	for a, d := range catalog.Descriptors() {
		r, ok := d.(*relationDescriptor)
		if !ok {
			continue
		}
		if r.SQLIndex != nil && group[r.SQLIndex.Parent] {
			group[a] = true
		}
		if r.Sequence != nil {
			parent := r.Sequence.Owned
			parent.SubID = 0
			if group[parent] {
				group[a] = true
			}
		}
	}
	locks := make([]ids.ObjectAddress, 0, len(group))
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
			if address.ClassID != ids.ForeignMappingClassID {
				local = append(local, address)
			}
		}
		roots = local
	}
	primaryRoot := ids.ObjectAddress{}
	if !reassign {
		local := roots[:0]
		for _, a := range roots {
			if a.ClassID != ids.DatabaseClassID {
				local = append(local, a)
			}
		}
		roots = local
		if len(local) == 1 {
			primaryRoot = local[0]
		}
		path, _ := e.referencePath(auth)
		pending := make(map[ids.ObjectAddress]bool, len(roots))
		for _, a := range roots {
			pending[a] = true
		}
		for _, a := range roots {
			var extension ids.ObjectAddress
			switch d := auth.catalog.Get(a).(type) {
			case *procedureDescriptor:
				extension = d.Extension
			case *tsDictionaryDescriptor:
				extension = d.Extension
			}
			if extension != (ids.ObjectAddress{}) && !pending[extension] {
				d := auth.catalog.MustExtension(extension)
				return fail(&sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + dependencyObject(auth.catalog, a, path) + " because extension " + dependencyIdentifier(d.Name()) + " requires it", Hint: "You can drop extension " + dependencyIdentifier(d.Name()) + " instead."})
			}
		}
		end := len(roots)
		for _, a := range roots[:end] {
			if extension, ok := auth.catalog.Extension(a); ok {
				for _, member := range extension.Members {
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
		ref := e.catalog.Domain.Ref(stage.base.Epoch(), a)
		if err = scope.acquireSequence(ref, lock.AccessExclusive, true); err != nil {
			return Result{}, err
		}
	}
	e.mu.Lock()
	providersChanged := false
	if reassign {
		for _, a := range roots {
			d := stage.base.Get(a)
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
				if err == nil && foreign.Address().ClassID == ids.ForeignServerClassID {
					stage.domainBases = append(stage.domainBases, auth.catalog.Get(foreign.Wrapper))
				}
			case *namespaceDescriptor:
				err = auth.Check(AccessRequirement{Object: e.catalog.Domain.Ref(stage.base.Epoch(), a), OwnerOnly: true})
				if err == nil {
					err = ownershipSuccessor(auth, target)
				}
				if err == nil {
					err = ownershipDatabaseCreate(auth)
				}
			case *collationDescriptor, *conversionDescriptor, *operatorDescriptor, *databaseDescriptor, *extensionDescriptor, *procedureDescriptor, *tsDictionaryDescriptor:
				err = auth.Check(AccessRequirement{Object: e.catalog.Domain.Ref(stage.base.Epoch(), a), OwnerOnly: true})
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
				for a, d := range candidate.Descriptors() {
					if old := stage.descriptors[a]; old != nil {
						if old == d {
							continue
						}
					} else if stage.base.Get(a) == d {
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
			if err = stage.removeObjectLocked(e.catalog.Domain.Ref(stage.base.Epoch(), a), a.ClassID == ids.NamespaceClassID); err != nil {
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
		if primaryRoot != (ids.ObjectAddress{}) && ownedCleanupAddsDeletionRoot(stage, sources) {
			primaryRoot = ids.ObjectAddress{}
		}
		if err = scope.stageExpressionDependents(stage, roots, mode, nil); err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "2BP01" {
				if primaryRoot != (ids.ObjectAddress{}) {
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
