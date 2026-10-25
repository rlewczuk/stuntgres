package engine

import (
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
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current())
}

// ProtocolAuthorizationStatus reads the restored identity and its owner-private
// role root without admitting a statement, refreshing roots or checking rights.
// retired distinguishes a domain-valid cached identity whose role was dropped
// from a foreign/invalid identity. Its last reported transport values remain
// cached; a same-name replacement must never be adopted for that old principal.
func (e *Engine) ProtocolAuthorizationStatus(s *session.Session) (name string, superuser, retired bool, err error) {
	if s == nil || EngineOf(s) != e || s.Transactions == nil {
		return "", false, false, fmt.Errorf("stuntgres: foreign authorization status session")
	}
	s.Transactions.Mu.Lock()
	defer s.Transactions.Mu.Unlock()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.Authorization == nil {
		return "", false, false, fmt.Errorf("stuntgres: missing authorization")
	}
	state := s.Authorization.State
	roles := e.sessionRolesLocked(s).Root.Read(authorization.PrincipalRef{})
	if err := roles.ValidatePrincipal(state.SessionUser); err != nil {
		return "", false, false, err
	}
	if err := roles.ValidatePrincipal(state.CurrentUser); err != nil {
		return "", false, false, err
	}
	selected, sessionExists := roles.Find(state.SessionUser)
	current, currentExists := roles.Find(state.CurrentUser)
	if !sessionExists || !currentExists {
		return "", false, true, nil
	}
	return selected.Name, current.Superuser, false, nil
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
	inputs     *AuthorityInputs         // Nil unless the engine built the view.
	Definition catalog.DefinitionReader // Cooking-only sparse effects; never SQL execution authority.
	settings   *session.SettingsStore
	// Nested schema elements prepend their enclosing namespace without changing settings.
	pathPrefix ids.ObjectAddress
	// Trusted replay permissions may elevate without changing $user semantics.
	namespaceUser authorization.PrincipalRef
	// Validated private Restore definition lookup only; it never changes the
	// named destination owner, session identity, ACLs or creation authority.
	restoreDefinitionLookup bool
	// An owned TEMP table builder may see its namespace before session publication.
	StagedTemporary        ids.ObjectAddress
	Session                *session.Session
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

// CanReadRestrictedSettings uses only the captured effective principal and
// INHERIT closure. SET membership and authenticated identity confer no read.
func (v AuthorizationView) CanReadRestrictedSettings() (bool, error) {
	target, ok := v.Roles.Lookup("pg_read_all_settings")
	if !ok {
		return false, fmt.Errorf("stuntgres: missing pg_read_all_settings role")
	}
	return v.Roles.InheritsRole(v.State.CurrentUser, target.Ref)
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
	return ReferencePath(v)
}

// trustedRestoreForm names the statement form a trusted Restore runs as the
// bootstrap superuser, or returns "". The CREATE forms need a table
// destination; INSERT needs insert authority (insert is true). The name is
// the authority a rejection names.
func trustedRestoreForm(stmt parser.Statement) (name string, insert bool) {
	switch s := stmt.(type) {
	case *parser.CreateView:
		return "view", false
	case *parser.CreateQueryTable:
		if s.Form == parser.CreateMaterializedView {
			return "materialized view", false
		}
	case *parser.CreateIndex:
		return "index", false
	case *parser.CreatePolicy:
		return "policy", false
	case *parser.Insert:
		return "insertion", true
	}
	return "", false
}

// Statement-local Restore authority must cover both lock resolution and effects.
// INSERT/COMMENT stamp no owner; named directives and session identity stay intact.
func (e *Engine) StatementAuthorizationLocked(op *session.Operation, stmt parser.Statement) (AuthorizationView, error) {
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		return AuthorizationView{}, err
	}
	replay := OperationReplayOrZero(op)
	if name, insert := trustedRestoreForm(stmt); name != "" && (insert && replay.InsertAuthority || !insert && replay.TableDestination) {
		if err := e.trustRestoreBootstrap(&auth, name); err != nil {
			return AuthorizationView{}, err
		}
	}
	if replay.CommentAuthority {
		comment, ok := stmt.(*parser.Comment)
		if !ok || (comment.Kind != parser.CommentTable && comment.Kind != parser.CommentForeignTable && comment.Kind != parser.CommentColumn && comment.Kind != parser.CommentSchema) {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore comment target")
		}
		if err := e.trustRestoreBootstrap(&auth, "comment"); err != nil {
			return AuthorizationView{}, err
		}
	}
	return auth, nil
}

// trustRestoreBootstrap runs auth as the bootstrap superuser and keeps the
// replayed user for namespace checks. The authenticated user must be that
// superuser on an engine with import targets; name is the authority a
// rejection names.
func (e *Engine) trustRestoreBootstrap(auth *AuthorizationView, name string) error {
	bootstrap, err := auth.Roles.Descriptor(auth.State.Authenticated)
	if err != nil || !bootstrap.Superuser || e.ImportTargets == nil {
		return fmt.Errorf("stuntgres: invalid trusted restore %s authority", name)
	}
	auth.namespaceUser = auth.State.CurrentUser
	auth.State.CurrentUser = bootstrap.Ref
	return nil
}

// OperationAuthorization returns op's current authorization view. It takes
// Engine.mu, then Session.mu inside authorizationLocked.
func OperationAuthorization(op *session.Operation) (AuthorizationView, error) {
	if op == nil || op.Self != op || op.Session == nil {
		return AuthorizationView{}, fmt.Errorf("stuntgres: foreign or missing operation")
	}
	e := EngineOf(op.Session)
	if err := e.ValidateOperation(op); err != nil {
		return AuthorizationView{}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.AuthorizationLocked(op)
}

// AuthorityInputs are the engine values an engine-built AuthorizationView
// reads: the engine's domain, which mints path references and matches the
// session's TEMP token, the settings registry, for the boot search_path of a
// view without session settings, and the published storage, which
// StorageLocked reads under Engine.mu. The engine holds one by value; views
// share it by pointer, so building a view allocates nothing.
type AuthorityInputs struct {
	Domain   *ids.Domain
	Settings *settings.Registry
	Storage  *storage.Store
}

// ref mints a reference to a in the engine's domain at the view's catalog
// epoch. Unlike View.Ref it does not read the view's owner, so a view with a
// missing or foreign owner still reaches the check that rejects it.
func (v AuthorizationView) ref(a ids.ObjectAddress) ids.ObjectRef {
	return v.inputs.Domain.Ref(v.Catalog.Epoch(), a)
}

// AuthorityView is v as an engine-built view. It is the one constructor of
// the engine inputs, so a view and its copies and recaptures cannot drift.
func (e *Engine) AuthorityView(v access.View) AuthorizationView {
	return AuthorizationView{View: v, inputs: &e.Authority}
}

// operationAuthority is v with op's environment: the definition reader,
// session settings, session and materialized population. It is shared by
// authorizationLocked and the DML disclosure and write-binding checks over a
// captured access.View.
func (e *Engine) operationAuthority(op *session.Operation, v access.View) AuthorizationView {
	s := op.Session
	auth := e.AuthorityView(v)
	auth.Definition, auth.settings, auth.Session = op.Definition, s.Settings, s
	auth.materializedPopulation = OperationReplayOrZero(op).MaterializedPopulation
	return auth
}

// Lock order is Engine.mu then Session.mu. Views retain only immutable values.
func (e *Engine) AuthorizationLocked(op *session.Operation) (AuthorizationView, error) {
	s := op.Session
	s.Mu.Lock()
	defer s.Mu.Unlock()
	a := s.Authorization
	if a == nil {
		return AuthorizationView{}, fmt.Errorf("stuntgres: missing authorization")
	}
	if err := e.refreshOperationRolesLocked(op); err != nil {
		return AuthorizationView{}, err
	}
	view := e.Catalog.Current()
	if s.Transactions != nil {
		view = e.PrivateCatalogLocked(s.Transactions.Catalog)
	}
	r := e.RoleStore.Read(&e.Catalog.Owner, view, e.OperationRolesLocked(op))
	bootstrap, err := r.Descriptor(a.State.Authenticated)
	if err != nil {
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
	if OperationReplayOrZero(op).MaterializedOwner != (authorization.PrincipalRef{}) {
		if _, err := r.Descriptor(OperationReplayOrZero(op).MaterializedOwner); err != nil {
			return AuthorizationView{}, err
		}
		identity.CurrentUser = OperationReplayOrZero(op).MaterializedOwner
	}
	authorization := e.operationAuthority(op, access.View{Owner: &e.Catalog.Owner, PairedCatalog: view, RoleRecord: e.OperationRolesLocked(op), Catalog: view, Roles: r, DatabasePredecessor: e.Catalog.State.Get(e.Catalog.Owner.DatabaseAddress()), State: identity, SessionRoleState: a.State, Revision: a.Revision})
	if OperationReplayOrZero(op).TableDestination {
		if !bootstrap.Superuser || e.ImportTargets == nil {
			return AuthorizationView{}, fmt.Errorf("stuntgres: invalid trusted restore definition lookup authority")
		}
		authorization.restoreDefinitionLookup = true
	}
	if _, exists := r.Find(identity.CurrentUser); !exists {
		authorization.MissingCurrent = identity.CurrentUser
	}
	return authorization, nil
}
