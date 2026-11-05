package core

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

var errDatabaseRetired = errors.New("stuntgres: database retired before control publication")

type databaseDropAttempt struct{ target *Engine }

// CheckDatabaseLiveLocked fences an exact runtime, never a name that could have
// been reused. Private construction stages have no DROP attempt or tombstone.
func (e *Engine) CheckDatabaseLiveLocked() error {
	if e.retired || e.dropAttempt != nil {
		return errDatabaseRetired
	}
	if e.Cluster != nil && e.Cluster.Targets[e.DatabaseName] != e {
		return errDatabaseRetired
	}
	return nil
}

func (e *Engine) dropCleanupOwnerLocked(source *session.Session) bool {
	if source == nil || EngineOf(source) != e {
		return false
	}
	e.sessionMu.Lock()
	registered := e.sessions[source.SessionToken] == source
	e.sessionMu.Unlock()
	if !registered {
		return false
	}
	source.Mu.Lock()
	ending := source.State == session.Ending
	source.Mu.Unlock()
	return ending
}

func dropDatabaseError(message, detail string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55006", Message: message, Detail: detail}
}

func (e *Engine) dropRestrictionsLocked(force bool) error {
	_, active := e.LogicalSlotCountLocked()
	if active != 0 {
		detail := "There is 1 active slot."
		if active != 1 {
			detail = fmt.Sprintf("There are %d active slots.", active)
		}
		return dropDatabaseError(fmt.Sprintf(`database "%s" is used by an active logical replication slot`, e.DatabaseName), detail)
	}
	subscriptions := 0
	for _, descriptor := range e.Catalog.State.Descriptors() {
		if subscription, ok := descriptor.(*catalog.Subscription); ok && subscription.Database == e.Catalog.DatabaseAddress() {
			subscriptions++
		}
	}
	if subscriptions != 0 {
		detail := "There is 1 subscription."
		if subscriptions != 1 {
			detail = fmt.Sprintf("There are %d subscriptions.", subscriptions)
		}
		return dropDatabaseError(fmt.Sprintf(`database "%s" is being used by logical replication subscription`, e.DatabaseName), detail)
	}
	if force && e.prepared.Len() != 0 {
		n := e.prepared.Len()
		detail := "There is 1 prepared transaction using the database."
		if n != 1 {
			detail = fmt.Sprintf("There are %d prepared transactions using the database.", n)
		}
		return dropDatabaseError(fmt.Sprintf(`database "%s" is being used by prepared transactions`, e.DatabaseName), detail)
	}
	return nil
}

func (e *Engine) authorizeDatabaseDropLocked(scope *StatementScope, target *Engine) error {
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return err
	}
	database, ok := target.Catalog.State.Get(target.Catalog.DatabaseAddress()).(*catalog.Database)
	if !ok || target.retired {
		return errDatabaseRetired
	}
	allowed, err := auth.Roles.InheritsRole(auth.State.CurrentUser, database.Owner)
	if err != nil {
		return err
	}
	if !allowed {
		return SQLError("42501", "must be owner of database "+database.Name())
	}
	if err := auth.View.SepgsqlCheck(database.Address(), sepgsql.Permission("db_database", "drop")); err != nil {
		return err
	}
	if database.IsTemplate {
		return SQLError("42809", "cannot drop a template database")
	}
	if target == e {
		return SQLError("55006", "cannot drop the currently open database")
	}
	return nil
}

// DropDatabase holds the ordinary AX utility grant, but no metadata, session or
// replacement lock across terminal callbacks and completion joins. AdmissionMu
// indexes the real single-use reservations; it is never held with Session.Mu.
func (e *Engine) DropDatabase(scope *StatementScope, statement *parser.DropDatabaseStmt) (Result, error) {
	ctx := scope.Op.Context()
	e.Mu.Lock()
	target := e.Cluster.Targets[statement.Name]
	e.Mu.Unlock()
	if target != nil {
		if err := scope.AcquireDatabase(target.Catalog.DatabaseAddress()); err != nil {
			return Result{}, err
		}
	}
	e.Mu.Lock()
	lockedTarget := e.Cluster.Targets[statement.Name]
	// Another DROP can remove the target while this issuer waits for AX.
	// Absence has the ordinary missing/IF EXISTS result; a replacement is
	// still a conflict because this grant protects only the old incarnation.
	if lockedTarget != nil && lockedTarget != target {
		e.Mu.Unlock()
		return Result{}, catalog.Conflict{}
	}
	if lockedTarget == nil {
		e.Mu.Unlock()
		if !statement.IfExists {
			return Result{}, SQLError("3D000", fmt.Sprintf(`database "%s" does not exist`, statement.Name))
		}
		scope.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`database "%s" does not exist, skipping`, statement.Name)})
		scope.CompleteUtility()
		return Result{CommandTag: "DROP DATABASE"}, nil
	}
	force := len(statement.Options) != 0
	err := e.authorizeDatabaseDropLocked(scope, target)
	if err == nil {
		err = target.CheckDatabaseLiveLocked()
	}
	if err == nil {
		err = target.dropRestrictionsLocked(force)
	}
	if err == nil && !force {
		err = target.DatabaseBusyLocked(nil, false)
	}
	if err == nil && target.Catalog.State.Epoch() == ^ids.CatalogEpoch(0) {
		err = errors.New("stuntgres: database epoch exhausted")
	}
	if err == nil {
		_, err = e.RoleStore.RevisionCandidateLocked()
	}
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	var owners []*session.Session
	var abandoned []*DatabaseAdmission
	e.Cluster.AdmissionMu.Lock()
	for a := e.Cluster.admissionHead; a != nil; a = a.next {
		if a.target != target {
			continue
		}
		if force {
			err = sessionSignalPermission(auth.Roles, auth.State.CurrentUser, a.principal, functions.SignalTerminate)
			if err != nil {
				break
			}
		}
		if a.owner != nil {
			owners = append(owners, a.owner)
		} else {
			abandoned = append(abandoned, a)
		}
	}
	// All authenticated victims have passed before changing any reservation.
	if err == nil {
		err = ctx.Err()
	}
	attempt := &databaseDropAttempt{target: target}
	var tickets []*lock.Ticket
	if err == nil {
		target.dropAttempt = attempt
		for _, a := range abandoned {
			a.state = 2
			a.revoked = true
			a.unlinkLocked()
			e.Cluster.admissions[target]--
			e.Cluster.principalAdmissions[a.principal]--
			if e.Cluster.principalAdmissions[a.principal] == 0 {
				delete(e.Cluster.principalAdmissions, a.principal)
			}
			if a.ticket != nil {
				tickets = append(tickets, a.ticket)
				a.ticket = nil
			}
		}
		if e.Cluster.admissions[target] == 0 {
			delete(e.Cluster.admissions, target)
		}
	}
	e.Cluster.AdmissionMu.Unlock()
	if err != nil {
		e.Mu.Unlock()
		return Result{}, err
	}
	// Private initialization is registered before callbacks. Causally owned TEMP
	// cleanup is joined by its public parent's completion, never killed separately.
	target.sessionMu.Lock()
	for token, s := range target.sessions {
		if token.Private() != nil && PayloadOf(s).cleanupSource == nil {
			owners = append(owners, s)
		}
	}
	for s := range target.privateStarts {
		if PayloadOf(s).cleanupSource == nil {
			owners = append(owners, s)
		}
	}
	target.sessionMu.Unlock()
	e.Mu.Unlock()
	defer func() {
		e.Mu.Lock()
		if target.dropAttempt == attempt {
			target.dropAttempt = nil
		}
		e.Mu.Unlock()
	}()
	for _, ticket := range tickets {
		e.Locks.End(ticket)
	}
	for _, owner := range owners {
		owner.Mu.Lock()
		owner.TerminateLocked()
		owner.Mu.Unlock()
	}
	for _, owner := range owners {
		if err := owner.WaitEnd(ctx); err != nil {
			return Result{}, err
		}
	}
	// No replacement writer is held while joining session maintenance. Once the
	// cohort is drained, ordinary replacement captures are fenced by dropAttempt.
	target.ReplacementMu.Lock()
	defer target.ReplacementMu.Unlock()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if target.dropAttempt != attempt || e.Cluster.Targets[statement.Name] != target {
		return Result{}, catalog.Conflict{}
	}
	if err := e.authorizeDatabaseDropLocked(scope, target); err != nil {
		return Result{}, err
	}
	// Existing slots can be acquired from a peer database while victims drain.
	// Match the released final slot-removal veto, not a control-lifecycle error.
	if err := target.checkReplacementSlotsLocked(); err != nil {
		return Result{}, err
	}
	if err := target.dropRestrictionsLocked(force); err != nil {
		return Result{}, err
	}
	if err := target.DatabaseBusyLocked(nil, false); err != nil {
		return Result{}, err
	}
	scope.Op.Session.Mu.Lock()
	defer scope.Op.Session.Mu.Unlock()
	if err := PublicationOperationLocked(e, scope.Op); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if err := e.publishDatabaseDropLocked(target, attempt); err != nil {
		return Result{}, err
	}
	scope.CompleteUtility()
	return Result{CommandTag: "DROP DATABASE"}, nil
}

// The only publisher authorized to omit a registered database descriptor.
// General catalog publication retains its lost-descriptor invariant unchanged.
func (e *Engine) publishDatabaseDropLocked(target *Engine, attempt *databaseDropAttempt) error {
	if target.dropAttempt != attempt || attempt.target != target || target.retired {
		return errDatabaseRetired
	}
	address := target.Catalog.DatabaseAddress()
	revision, err := e.RoleStore.RevisionCandidateLocked()
	if err != nil {
		return err
	}
	settings := e.RoleStore.Published.Settings.WithoutDatabase(address)
	roles := e.RoleStore.Published
	if settings != roles.Settings {
		roles = roles.WithSettings(settings)
		roles.Revision = revision
	}
	next := catalog.CloneState(e.Catalog.State)
	descriptor := next.Get(address)
	next.UnbindName(catalog.DescriptorNameKey(descriptor))
	next.RemoveDescriptor(address)
	for key := range next.SecurityLabels() {
		if key.Object == address {
			next.SetSecurityLabel(key, nil)
		}
	}
	roots := e.Catalog.ProjectSharedLocked(next, e.Cluster.Catalogs)
	// The retained runtime is a shared-authority holder, not a seeded database.
	tombstone := catalog.SharedOnlyState(next, target.Catalog.State.Epoch()+1)
	targets := make(map[string]*Engine, len(e.Cluster.Targets)-1)
	for name, runtime := range e.Cluster.Targets {
		if runtime != target {
			targets[name] = runtime
		}
	}
	var shared []ids.ObjectAddress
	for address, relation := range target.Catalog.State.Relations() {
		if catalog.SharedRelation(relation) {
			shared = append(shared, address)
		}
	}
	// From here through retirement there is no fallible work or external I/O.
	roots.InstallLocked()
	target.Catalog.PublishRetirementLocked(tombstone)
	e.RoleStore.PublishLocked(roles)
	if roles.Revision == revision {
		e.RoleStore.AdvanceRevisionFrontierLocked(revision)
	}
	e.Cluster.Targets = targets
	target.retired = true
	target.dropAttempt = nil
	target.dropReplacedSlotsLocked()
	target.fenceSnapshotsLocked()
	target.Storage.ReplaceRelationsLocked(nil)
	target.Storage.ReplaceSequencesLocked(nil)
	target.Storage.ReplaceIndexRootsLocked(nil)
	target.Storage.RelationMetadata = nil
	target.Visibility.RetireLocked()
	target.IndexWaits = index.WaitRegistry{}
	target.Exclusions.ResetLocked()
	target.notifications = session.NotificationRegistry{}
	target.catalogCaptures = catalogrt.CaptureCache{}
	target.VacuumProgress = nil
	target.ClusterProgress = nil
	for id, reservation := range e.Cluster.replicationOrigins {
		if reservation.SubscriptionDatabase == address {
			delete(e.Cluster.replicationOrigins, id)
		}
	}
	target.Foreign.AdoptNamespaces(fdw.Registry{})
	target.Statistics.RetireDatabase(target.Catalog.Domain, shared)
	target.Locks.RetireDomain(target.Catalog.Domain)
	target.reconcileLogicalBuffersLocked()
	return nil
}
