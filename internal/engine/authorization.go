package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func (e *Engine) Roles() authorization.Read {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published)
}
func (e *Engine) ResolveLogin(name string) (authorization.PrincipalRef, error) {
	d, ok := e.Roles().Lookup(name)
	if !ok {
		return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: authenticated role %q does not exist", name)
	}
	if !d.Login {
		return authorization.PrincipalRef{}, roleLoginError(name)
	}
	return d.Ref, nil
}

// SetRoleReferences publishes copied persistent dependencies owned by another
// internal feature. An empty payload unregisters that feature's references.
func (e *Engine) SetRoleReferences(ctx context.Context, feature string, refs []authorization.PrincipalRef) error {
	if feature == "" {
		return fmt.Errorf("stuntgres: empty role reference owner")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ref := range refs {
		if _, err := (e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published)).Descriptor(ref); err != nil {
			return err
		}
	}
	return e.roles.setReferencesLocked(ctx, feature, refs)
}

type sessionAuthorization struct {
	session  *Session
	state    authorization.RoleState
	revision uint64
}

func (a *sessionAuthorization) EndSession(token ids.SessionToken, reason EndReason) error {
	if a.session == nil || a.session.token != token {
		return fmt.Errorf("stuntgres: foreign authorization attachment")
	}
	a.session.mu.Lock()
	defer a.session.mu.Unlock()
	if a.session.authorization == a {
		a.session.authorization = nil
	}
	return nil
}
func (op *Operation) RoleState() authorization.RoleState {
	if op == nil || op.self != op || op.session == nil {
		return authorization.RoleState{}
	}
	s := op.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.authorization == nil {
		return authorization.RoleState{}
	}
	return s.authorization.state
}

// AuthorizationView is statement-level authority. It embeds the captured
// access.View and adds the session and statement inputs: the engine (for
// identity checks and pass-throughs), the session settings and the path
// inputs. Its Check, CanUse, CheckCreate, Effective and checkColumns are entry
// points that pass the view itself as the access.Environment.
type AuthorizationView struct {
	access.View
	engine     *Engine
	definition catalog.DefinitionReader // Cooking-only sparse effects; never SQL execution authority.
	settings   *sessionSettings
	// Nested schema elements prepend their enclosing namespace without changing settings.
	pathPrefix ids.ObjectAddress
	// Trusted replay permissions may elevate without changing $user semantics.
	namespaceUser authorization.PrincipalRef
	// An owned TEMP table builder may see its namespace before session publication.
	stagedTemporary        ids.ObjectAddress
	session                *Session
	materializedPopulation bool
}

func (v AuthorizationView) Effective(ref ids.ObjectRef, grantOptions bool) (authorization.Privileges, error) {
	return v.View.Effective(v, ref, grantOptions)
}
func (v AuthorizationView) Check(req access.Requirement) error { return v.View.Check(v, req) }
func (v AuthorizationView) CanUse(ref ids.ObjectRef) error     { return v.View.CanUse(v, ref) }
func (v AuthorizationView) CheckCreate(ref ids.ObjectRef) error {
	return v.View.CheckCreate(v, ref)
}
func (v AuthorizationView) checkColumns(b analyze.Binding, mask authorization.Privileges, indices []int) error {
	return v.View.CheckColumns(v, b.Ref, b.Relation, mask, indices)
}

// TemporaryNamespace implements access.Environment: the session's own TEMP
// namespace as this view sees it.
func (v AuthorizationView) TemporaryNamespace() ids.ObjectAddress { return temporaryNamespace(v) }

// ReferencePath implements access.Environment: the live search path, read
// under the session settings mutex.
func (v AuthorizationView) ReferencePath() ([]ids.ObjectAddress, error) {
	return v.engine.referencePath(v)
}

// Statement-local Restore authority must cover both lock resolution and effects.
// INSERT/COMMENT stamp no owner; named directives and session identity stay intact.
func (e *Engine) statementAuthorizationLocked(op *Operation, stmt parser.Statement) (AuthorizationView, error) {
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return AuthorizationView{}, err
	}
	if _, view := stmt.(*parser.CreateView); view && op.replayOrZero().tableDestination {
		bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore view authority")
		}
		auth.namespaceUser = auth.State.CurrentUser
		auth.State.CurrentUser = bootstrap.Ref
	}
	if creation, ok := stmt.(*parser.CreateQueryTable); ok && creation.Form == parser.CreateMaterializedView && op.replayOrZero().tableDestination {
		bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore materialized view authority")
		}
		auth.namespaceUser = auth.State.CurrentUser
		auth.State.CurrentUser = bootstrap.Ref
	}
	if _, index := stmt.(*parser.CreateIndex); index && op.replayOrZero().tableDestination {
		bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore index authority")
		}
		auth.namespaceUser = auth.State.CurrentUser
		auth.State.CurrentUser = bootstrap.Ref
	}
	if _, policy := stmt.(*parser.CreatePolicy); policy && op.replayOrZero().tableDestination {
		bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore policy authority")
		}
		auth.namespaceUser = auth.State.CurrentUser
		auth.State.CurrentUser = bootstrap.Ref
	}
	if _, insert := stmt.(*parser.Insert); insert && op.replayOrZero().insertAuthority {
		bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore insertion authority")
		}
		auth.namespaceUser = auth.State.CurrentUser
		auth.State.CurrentUser = bootstrap.Ref
	}
	if op.replayOrZero().commentAuthority {
		comment, ok := stmt.(*parser.Comment)
		if !ok || (comment.Kind != parser.CommentTable && comment.Kind != parser.CommentColumn && comment.Kind != parser.CommentSchema) {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore comment target")
		}
		bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore comment authority")
		}
		auth.namespaceUser = auth.State.CurrentUser
		auth.State.CurrentUser = bootstrap.Ref
	}
	return auth, nil
}

func (op *Operation) Authorization() (AuthorizationView, error) {
	if op == nil || op.self != op || op.session == nil {
		return AuthorizationView{}, fmt.Errorf("stuntgres: foreign or missing operation")
	}
	e := op.session.Engine()
	if err := e.validateOperation(op); err != nil {
		return AuthorizationView{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.authorizationLocked(op)
}

// Lock order is Engine.mu then Session.mu. Views retain only immutable values.
func (e *Engine) authorizationLocked(op *Operation) (AuthorizationView, error) {
	s := op.session
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.authorization
	if a == nil {
		return AuthorizationView{}, fmt.Errorf("stuntgres: missing authorization")
	}
	view := e.catalog.read(e.catalog.state)
	if s.transactions != nil {
		view = e.privateCatalogLocked(s.transactions.catalog)
	}
	r := e.roles.read(e.catalog.owner(), view, e.operationRolesLocked(op))
	if _, err := r.Descriptor(a.state.Authenticated); err != nil {
		return AuthorizationView{}, err
	}
	// PREPARE can detach a newly created role while preserving SET ROLE's ID.
	// Admission validates that ID's authority, not its current name visibility.
	for _, ref := range [...]authorization.PrincipalRef{a.state.SessionUser, a.state.CurrentUser} {
		if err := r.ValidatePrincipal(ref); err != nil {
			return AuthorizationView{}, err
		}
	}
	identity := a.state
	if op.replayOrZero().materializedOwner != (authorization.PrincipalRef{}) {
		if _, err := r.Descriptor(op.replayOrZero().materializedOwner); err != nil {
			return AuthorizationView{}, err
		}
		identity.CurrentUser = op.replayOrZero().materializedOwner
	}
	authorization := AuthorizationView{View: access.View{Owner: e.catalog.owner(), PairedCatalog: view, RoleRecord: e.operationRolesLocked(op), Catalog: view, Roles: r, DatabasePredecessor: e.catalog.state.Get(catalog.CurrentDatabaseAddress), State: identity, SessionRoleState: a.state, Revision: a.revision}, engine: e, definition: op.definition, settings: s.settings, session: s, materializedPopulation: op.replayOrZero().materializedPopulation}
	if _, exists := r.Find(identity.CurrentUser); !exists {
		authorization.MissingCurrent = identity.CurrentUser
	}
	return authorization, nil
}
func (v AuthorizationView) checkBinding(b analyze.Binding, stmt parser.Statement) error {
	if b.ViewWrite != nil {
		return v.checkViewWrite(b, stmt)
	}
	switch stmt.(type) {
	case *parser.Select, *parser.DeclareCursor, *parser.CreateQueryTable:
		return v.checkQuerySources(b.QuerySources)
	}
	if b.Relation == nil {
		return nil
	}
	switch stmt.(type) {
	case *parser.Insert:
		if err := v.checkColumns(b, authorization.InsertPrivilege, b.Targets); err != nil {
			return err
		}
		if b.Insert.Conflict != nil && len(b.Insert.Conflict.Reads) != 0 {
			if err := v.checkColumns(b, authorization.SelectPrivilege, b.Insert.Conflict.Reads); err != nil {
				return err
			}
		}
		if b.Insert.Conflict != nil && b.Insert.Conflict.Update != nil {
			if err := v.checkColumns(b, authorization.UpdatePrivilege, b.Insert.Conflict.UpdateTargets); err != nil {
				return err
			}
			if len(b.Insert.Conflict.ActionReads) != 0 {
				if err := v.checkColumns(b, authorization.SelectPrivilege, b.Insert.Conflict.ActionReads); err != nil {
					return err
				}
			}
		}
		if b.Returning != nil && (b.Returning.ReadRequired || len(b.Returning.Reads) != 0) {
			if err := v.checkColumns(b, authorization.SelectPrivilege, b.Returning.Reads); err != nil {
				return err
			}
		}
		return v.checkQuerySources(b.QuerySources)
	case *parser.Merge:
		if b.Merge.HasDelete {
			if err := v.Check(access.Requirement{Object: b.Ref, Privileges: authorization.DeletePrivilege}); err != nil {
				return err
			}
		}
		if b.Merge.HasInsert {
			if err := v.checkColumns(b, authorization.InsertPrivilege, b.Merge.InsertTargets); err != nil {
				return err
			}
		}
		if len(b.Merge.UpdateTargets) != 0 {
			if err := v.checkColumns(b, authorization.UpdatePrivilege, b.Merge.UpdateTargets); err != nil {
				return err
			}
		}
		if b.Merge.HasNothing || len(b.UsedColumns) != 0 || b.Returning != nil && b.Returning.ReadRequired {
			if err := v.checkColumns(b, authorization.SelectPrivilege, b.UsedColumns); err != nil {
				return err
			}
		}
		return v.checkQuerySources(b.QuerySources)
	case *parser.Delete:
		if err := v.Check(access.Requirement{Object: b.Ref, Privileges: authorization.DeletePrivilege}); err != nil {
			return err
		}
		if len(b.UsedColumns) != 0 {
			if err := v.checkColumns(b, authorization.SelectPrivilege, b.UsedColumns); err != nil {
				return err
			}
		}
		return v.checkQuerySources(b.QuerySources)
	case *parser.Update:
		if err := v.checkColumns(b, authorization.UpdatePrivilege, b.Targets); err != nil {
			return err
		}
		if len(b.UsedColumns) != 0 {
			if err := v.checkColumns(b, authorization.SelectPrivilege, b.UsedColumns); err != nil {
				return err
			}
		}
		return v.checkQuerySources(b.QuerySources)
	default:
		return v.checkColumns(b, authorization.SelectPrivilege, b.UsedColumns)
	}
}

func (v AuthorizationView) checkQuerySources(sources []exec.Leaf) error {
	for _, source := range sources {
		if source.AnalysisOnly {
			continue
		}
		sourceAuth := v
		if source.Principal != (authorization.PrincipalRef{}) {
			sourceAuth.State.CurrentUser = source.Principal
			if err := sourceAuth.Roles.ValidatePrincipal(source.Principal); err != nil {
				return err
			}
		}
		if err := sourceAuth.checkColumns(analyze.Binding{Ref: source.Ref, Relation: source.Relation}, authorization.SelectPrivilege, source.Uses); err != nil {
			return err
		}
	}
	for _, source := range sources {
		sourceAuth := v
		if source.Principal != (authorization.PrincipalRef{}) {
			sourceAuth.State.CurrentUser = source.Principal
			if err := sourceAuth.Roles.ValidatePrincipal(source.Principal); err != nil {
				return err
			}
		}
		if exec.EffectiveQueryRowMark(source.Relation, source.Mark).Mode != 0 {
			if err := sourceAuth.checkColumns(analyze.Binding{Ref: source.Ref, Relation: source.Relation}, authorization.UpdatePrivilege, nil); err != nil {
				return err
			}
		}
		if source.Relation.RelationKind == catalog.ViewRelation && (source.Mark.Mode != 0 || viewHasStoredRowMarks(source.Relation, v.Catalog)) {
			if err := sourceAuth.checkViewRowMarks(source.Relation, source.Mark, sourceAuth.State.CurrentUser); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetObjectACL publishes an owned immutable ACL payload for internal features.
func (e *Engine) SetObjectACL(ctx context.Context, ref ids.ObjectRef, acl authorization.ACLValue) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() {
		return fmt.Errorf("stuntgres: foreign or stale ACL object")
	}
	addr := ref.Address
	addr.SubID = 0
	d, ok := e.catalog.state.Find(addr)
	if !ok {
		return fmt.Errorf("stuntgres: missing ACL object")
	}
	if !acl.Explicit && len(acl.Entries) != 0 {
		return fmt.Errorf("stuntgres: entries in absent ACL")
	}
	acl.Entries = append([]authorization.ACLEntry(nil), acl.Entries...)
	var next catalog.Descriptor
	switch d := d.(type) {
	case *catalog.Database:
		if ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: database subobject")
		}
		n := *d
		n.ACL = acl
		next = &n
	case *catalog.Namespace:
		if ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: namespace subobject")
		}
		n := *d
		n.ACL = acl
		next = &n
	case *catalog.Type:
		if ref.Address.SubID != 0 || d.SQLType.IsArray() {
			return fmt.Errorf("stuntgres: invalid type ACL target")
		}
		n := *d
		n.ACL = acl
		n.ACLSyntheticDefault = false
		next = &n
	case *catalog.Procedure:
		if ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: procedure subobject")
		}
		n := *d
		n.ACL = acl
		next = &n
	case *catalog.Parameter:
		if ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: parameter subobject")
		}
		n := *d
		n.ACL = acl
		next = &n
	case *catalog.Tablespace:
		if ref.Address.SubID != 0 {
			return fmt.Errorf("stuntgres: tablespace subobject")
		}
		n := *d
		n.ACL = acl
		next = &n
	case *catalog.Relation:
		if ref.Address.SubID < 0 || ref.Address.SubID > 0 && !d.HasAttribute(ref.Address.SubID) {
			return fmt.Errorf("stuntgres: invalid ACL column")
		}
		n := *d
		if ref.Address.SubID == 0 {
			n.Security.ACL = acl
		} else {
			n.Security.ColumnACL = make(map[int32]authorization.ACLValue, len(d.Security.ColumnACL)+1)
			for k, v := range d.Security.ColumnACL {
				n.Security.ColumnACL[k] = v
			}
			if acl.Explicit {
				n.Security.ColumnACL[ref.Address.SubID] = acl
			} else {
				delete(n.Security.ColumnACL, ref.Address.SubID)
			}
		}
		next = &n
	default:
		return fmt.Errorf("stuntgres: unsupported ACL object")
	}
	if err := access.ValidateACL(e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published), next); err != nil {
		return err
	}
	if e.roles.published.Revision == ^uint64(0) {
		return fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	state := catalog.CloneState(e.catalog.state)
	if parameter, ok := next.(*catalog.Parameter); ok {
		installParameterGrant(state, parameter)
	} else {
		state.SetDescriptor(addr, next)
	}
	roles := e.roles.published.Advance()
	if err := ctx.Err(); err != nil {
		return err
	}
	if n, ok := next.(*catalog.Relation); ok && n.RelationKind == catalog.SequenceRelation && ref.Address.SubID == 0 {
		stamp, err := e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		n.SequencePlanStamp = stamp
	}
	if _, ok := next.(*catalog.Namespace); ok {
		if err := state.AdvanceNamespaceGeneration(); err != nil {
			return err
		}
	}
	e.catalog.state = state
	e.roles.published = roles
	return nil
}
