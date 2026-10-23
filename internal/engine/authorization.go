package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
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
		return authorization.PrincipalRef{}, role.LoginError(name)
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

// The binding walkers in analyze check through the view's entry points.
var _ analyze.Privileges = AuthorizationView{}

// AuthorizationView is statement-level authority. It embeds the captured
// access.View and adds the session and statement inputs: the engine inputs
// (authorityInputs), the session settings and the path inputs. It holds no
// engine: identity checks compare the view's catalog owner. Its Check, CanUse,
// CheckCreate, Effective and CheckColumns are entry points that pass the view
// itself as the access.Environment.
type AuthorizationView struct {
	access.View
	inputs     *authorityInputs         // Nil unless the engine built the view.
	definition catalog.DefinitionReader // Cooking-only sparse effects; never SQL execution authority.
	settings   *session.SettingsStore
	// Nested schema elements prepend their enclosing namespace without changing settings.
	pathPrefix ids.ObjectAddress
	// Trusted replay permissions may elevate without changing $user semantics.
	namespaceUser authorization.PrincipalRef
	// An owned TEMP table builder may see its namespace before session publication.
	stagedTemporary        ids.ObjectAddress
	session                *session.Session
	materializedPopulation bool
}

// live reports whether the engine built v (authorityView); zero views and
// unit fixtures are not live.
func (v AuthorizationView) live() bool { return v.inputs != nil }

func (v AuthorizationView) Effective(ref ids.ObjectRef, grantOptions bool) (authorization.Privileges, error) {
	return v.View.Effective(v, ref, grantOptions)
}
func (v AuthorizationView) Check(req access.Requirement) error { return v.View.Check(v, req) }
func (v AuthorizationView) CanUse(ref ids.ObjectRef) error     { return v.View.CanUse(v, ref) }
func (v AuthorizationView) CheckCreate(ref ids.ObjectRef) error {
	return v.View.CheckCreate(v, ref)
}
func (v AuthorizationView) CheckColumns(ref ids.ObjectRef, relation *catalog.Relation, mask authorization.Privileges, indices []int) error {
	return v.View.CheckColumns(v, ref, relation, mask, indices)
}

// WithCurrentUser returns the view running as principal, which also moves the
// $user entry of its reference path. The principal is validated after the
// switch, as the query source walker always did.
func (v AuthorizationView) WithCurrentUser(principal authorization.PrincipalRef) (analyze.Privileges, error) {
	v.State.CurrentUser = principal
	return v, v.Roles.ValidatePrincipal(principal)
}

// CheckViewRowMarks checks the stored and inherited row marks of a view
// source with the current user as invoker. Without either it checks nothing.
func (v AuthorizationView) CheckViewRowMarks(relation *catalog.Relation, mark plan.RowMark) error {
	if mark.Mode == 0 && !stored.ViewHasRowMarks(relation, v.Catalog) {
		return nil
	}
	return view.CheckRowMarks(v.View, v, relation, mark, v.State.CurrentUser)
}

// TemporaryNamespace implements access.Environment: the session's own TEMP
// namespace as this view sees it.
func (v AuthorizationView) TemporaryNamespace() ids.ObjectAddress { return temporaryNamespace(v) }

// ReferencePath implements access.Environment: the live search path, read
// under the session settings mutex.
func (v AuthorizationView) ReferencePath() ([]ids.ObjectAddress, error) {
	return referencePath(v)
}

// Statement-local Restore authority must cover both lock resolution and effects.
// INSERT/COMMENT stamp no owner; named directives and session identity stay intact.
func (e *Engine) statementAuthorizationLocked(op *session.Operation, stmt parser.Statement) (AuthorizationView, error) {
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return AuthorizationView{}, err
	}
	if _, view := stmt.(*parser.CreateView); view && operationReplayOrZero(op).tableDestination {
		bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore view authority")
		}
		auth.namespaceUser = auth.State.CurrentUser
		auth.State.CurrentUser = bootstrap.Ref
	}
	if creation, ok := stmt.(*parser.CreateQueryTable); ok && creation.Form == parser.CreateMaterializedView && operationReplayOrZero(op).tableDestination {
		bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore materialized view authority")
		}
		auth.namespaceUser = auth.State.CurrentUser
		auth.State.CurrentUser = bootstrap.Ref
	}
	if _, index := stmt.(*parser.CreateIndex); index && operationReplayOrZero(op).tableDestination {
		bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore index authority")
		}
		auth.namespaceUser = auth.State.CurrentUser
		auth.State.CurrentUser = bootstrap.Ref
	}
	if _, policy := stmt.(*parser.CreatePolicy); policy && operationReplayOrZero(op).tableDestination {
		bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore policy authority")
		}
		auth.namespaceUser = auth.State.CurrentUser
		auth.State.CurrentUser = bootstrap.Ref
	}
	if _, insert := stmt.(*parser.Insert); insert && operationReplayOrZero(op).insertAuthority {
		bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
		if err != nil || !bootstrap.Superuser || e.importTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore insertion authority")
		}
		auth.namespaceUser = auth.State.CurrentUser
		auth.State.CurrentUser = bootstrap.Ref
	}
	if operationReplayOrZero(op).commentAuthority {
		comment, ok := stmt.(*parser.Comment)
		if !ok || (comment.Kind != parser.CommentTable && comment.Kind != parser.CommentForeignTable && comment.Kind != parser.CommentColumn && comment.Kind != parser.CommentSchema) {
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

// operationAuthorization returns op's current authorization view. It takes
// Engine.mu, then Session.mu inside authorizationLocked.
func operationAuthorization(op *session.Operation) (AuthorizationView, error) {
	if op == nil || op.Self != op || op.Session == nil {
		return AuthorizationView{}, fmt.Errorf("stuntgres: foreign or missing operation")
	}
	e := engineOf(op.Session)
	if err := e.validateOperation(op); err != nil {
		return AuthorizationView{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.authorizationLocked(op)
}

// authorityInputs are the engine values an engine-built AuthorizationView
// reads: the engine's domain, which mints path references and matches the
// session's TEMP token, the settings registry, for the boot search_path of a
// view without session settings, and the published storage, which
// StorageLocked reads under Engine.mu. The engine holds one by value; views
// share it by pointer, so building a view allocates nothing.
type authorityInputs struct {
	domain   *ids.Domain
	settings *settings.Registry
	storage  *storage.Store
}

// ref mints a reference to a in the engine's domain at the view's catalog
// epoch. Unlike View.Ref it does not read the view's owner, so a view with a
// missing or foreign owner still reaches the check that rejects it.
func (v AuthorizationView) ref(a ids.ObjectAddress) ids.ObjectRef {
	return v.inputs.domain.Ref(v.Catalog.Epoch(), a)
}

// authorityView is v as an engine-built view. It is the one constructor of
// the engine inputs, so a view and its copies and recaptures cannot drift.
func (e *Engine) authorityView(v access.View) AuthorizationView {
	return AuthorizationView{View: v, inputs: &e.authority}
}

// operationAuthority is v with op's environment: the definition reader,
// session settings, session and materialized population. It is shared by
// authorizationLocked and the DML disclosure and write-binding checks over a
// captured access.View.
func (e *Engine) operationAuthority(op *session.Operation, v access.View) AuthorizationView {
	s := op.Session
	auth := e.authorityView(v)
	auth.definition, auth.settings, auth.session = op.Definition, s.Settings, s
	auth.materializedPopulation = operationReplayOrZero(op).materializedPopulation
	return auth
}

// Lock order is Engine.mu then Session.mu. Views retain only immutable values.
func (e *Engine) authorizationLocked(op *session.Operation) (AuthorizationView, error) {
	s := op.Session
	s.Mu.Lock()
	defer s.Mu.Unlock()
	a := s.Authorization
	if a == nil {
		return AuthorizationView{}, fmt.Errorf("stuntgres: missing authorization")
	}
	view := e.catalog.read(e.catalog.state)
	if s.Transactions != nil {
		view = e.privateCatalogLocked(s.Transactions.Catalog)
	}
	r := e.roles.read(e.catalog.owner(), view, e.operationRolesLocked(op))
	if _, err := r.Descriptor(a.State.Authenticated); err != nil {
		return AuthorizationView{}, err
	}
	// PREPARE can detach a newly created role while preserving SET ROLE's ID.
	// Admission validates that ID's authority, not its current name visibility.
	for _, ref := range [...]authorization.PrincipalRef{a.State.SessionUser, a.State.CurrentUser} {
		if err := r.ValidatePrincipal(ref); err != nil {
			return AuthorizationView{}, err
		}
	}
	identity := a.State
	if operationReplayOrZero(op).materializedOwner != (authorization.PrincipalRef{}) {
		if _, err := r.Descriptor(operationReplayOrZero(op).materializedOwner); err != nil {
			return AuthorizationView{}, err
		}
		identity.CurrentUser = operationReplayOrZero(op).materializedOwner
	}
	authorization := e.operationAuthority(op, access.View{Owner: e.catalog.owner(), PairedCatalog: view, RoleRecord: e.operationRolesLocked(op), Catalog: view, Roles: r, DatabasePredecessor: e.catalog.state.Get(catalog.CurrentDatabaseAddress), State: identity, SessionRoleState: a.State, Revision: a.Revision})
	if _, exists := r.Find(identity.CurrentUser); !exists {
		authorization.MissingCurrent = identity.CurrentUser
	}
	return authorization, nil
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
	next, err := access.WithObjectACL(d, ref.Address.SubID, acl)
	if err != nil {
		return err
	}
	if err := access.ValidateACL(e.roles.read(e.catalog.owner(), e.catalog.read(e.catalog.state), e.roles.published), next); err != nil {
		return err
	}
	if e.roles.published.Revision == ^uint64(0) {
		return fmt.Errorf("stuntgres: authorization revision exhausted")
	}
	state := catalog.CloneState(e.catalog.state)
	if parameter, ok := next.(*catalog.Parameter); ok {
		catalog.InstallParameterGrant(state, parameter)
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
