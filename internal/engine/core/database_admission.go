package core

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

type DatabaseSelector struct {
	Name     string
	Explicit bool
}

// DatabaseAdmission is a single-use physical connection reservation. Identity,
// state and occupancy belong to the cluster's AdmissionMu; target and captured
// startup authority are immutable. Copying this value never copies authority.
type DatabaseAdmission struct {
	self           *DatabaseAdmission
	cluster        *DatabaseCluster
	target         *Engine
	database       ids.ObjectAddress
	principal      authorization.PrincipalRef
	token          ids.SessionToken
	ticket         *lock.Ticket
	name           string
	roles          authorization.Read
	record         *access.RoleRecord
	owner          *session.Session   // Exact physical owner, once bound during construction.
	state          uint8              // 0 reserved, 1 consumed, 2 released
	revoked        bool               // FORCE, unlike an ordinary caller abandonment.
	previous, next *DatabaseAdmission // AdmissionMu; no second identity registry.
}

func (a *DatabaseAdmission) Target() *Engine {
	if a == nil || a.self != a {
		return nil
	}
	return a.target
}

// unlinkLocked transfers no authority: the same owning token is removed once
// under AdmissionMu, either by ownerless FORCE revocation or terminal release.
func (a *DatabaseAdmission) unlinkLocked() {
	if a.previous != nil {
		a.previous.next = a.next
	} else {
		a.cluster.admissionHead = a.next
	}
	if a.next != nil {
		a.next.previous = a.previous
	} else {
		a.cluster.admissionTail = a.previous
	}
	a.previous, a.next = nil, nil
}

func databaseAdmissionError(code, message string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: code, Message: message}
}

func (e *Engine) AdmitDatabase(ctx context.Context, principal authorization.PrincipalRef, id ids.SessionID, selector DatabaseSelector) (*DatabaseAdmission, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if id == 0 {
		return nil, errors.New("stuntgres: zero session identity")
	}
	e.Mu.Lock()
	cluster := e.Cluster
	if cluster == nil || e.Mu != &cluster.Mu || e.RoleStore != &cluster.Roles {
		e.Mu.Unlock()
		return nil, errors.New("stuntgres: unregistered database admission")
	}
	descriptor, err := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).Descriptor(principal)
	if err != nil {
		e.Mu.Unlock()
		return nil, err
	}
	if !descriptor.Login {
		e.Mu.Unlock()
		return nil, role.LoginError(descriptor.Name)
	}
	target := cluster.selectDatabaseLocked(selector)
	if target == nil {
		name := selector.Name
		if !selector.Explicit && cluster.DefaultTarget != nil {
			name = cluster.DefaultTarget.DatabaseName
		}
		e.Mu.Unlock()
		return nil, databaseAdmissionError("3D000", fmt.Sprintf(`database "%s" does not exist`, name))
	}
	token := target.Catalog.Domain.Session(id)
	resource := e.DatabaseResource(target.Catalog.DatabaseAddress())
	e.Mu.Unlock()
	ticket, err := e.Locks.RegisterSession(token)
	if err != nil {
		return nil, e.databaseAdmissionLockError(target, selector, err)
	}
	transferred := false
	defer func() {
		if !transferred {
			e.Locks.End(ticket)
		}
	}()
	for {
		// An established copy lease excludes startup before it enters the
		// ordinary lock queue. Queuing RX behind that copy's Share would also
		// put compatible later copies behind RX through normal FIFO fairness.
		e.Mu.Lock()
		if cluster.selectDatabaseLocked(selector) != target {
			name := selector.Name
			if !selector.Explicit {
				name = target.DatabaseName
			}
			e.Mu.Unlock()
			return nil, databaseAdmissionError("3D000", fmt.Sprintf(`database "%s" does not exist`, name))
		}
		cluster.AdmissionMu.Lock()
		var wait <-chan struct{}
		if readers := cluster.templateCopies[target]; readers != nil {
			wait = readers.done
		}
		cluster.AdmissionMu.Unlock()
		e.Mu.Unlock()
		if wait == nil {
			if err := e.Locks.Acquire(ctx, ticket, resource, lock.RowExclusive); err != nil {
				return nil, e.databaseAdmissionLockError(target, selector, err)
			}
			// Recheck the exact selector, principal and lease after the real
			// grant, and release RX before any lease-generation wait.
			a, next, err := e.admitDatabase(ctx, principal, selector, target, token, ticket)
			e.Locks.ReleaseDatabaseAdmission(ticket, resource)
			if next == nil {
				if err == nil {
					transferred = true
				}
				return a, err
			}
			wait = next
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// A queued startup ticket can be retired by DROP before it becomes an owning
// reservation. Translate only that exact missing-incarnation outcome, leaving
// context, lock and live-target failures unchanged.
func (e *Engine) databaseAdmissionLockError(target *Engine, selector DatabaseSelector, err error) error {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if e.Cluster.selectDatabaseLocked(selector) != target || target.retired {
		name := selector.Name
		if !selector.Explicit {
			name = target.DatabaseName
		}
		return databaseAdmissionError("3D000", fmt.Sprintf(`database "%s" does not exist`, name))
	}
	return err
}

func (e *Engine) admitDatabase(ctx context.Context, principal authorization.PrincipalRef, selector DatabaseSelector, expected *Engine, token ids.SessionToken, ticket *lock.Ticket) (*DatabaseAdmission, <-chan struct{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	cluster := e.Cluster
	if cluster == nil || e.Mu != &cluster.Mu || e.RoleStore != &cluster.Roles {
		return nil, nil, errors.New("stuntgres: unregistered database admission")
	}
	descriptor, err := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).Descriptor(principal)
	if err != nil {
		return nil, nil, err
	}
	if !descriptor.Login {
		return nil, nil, role.LoginError(descriptor.Name)
	}
	target := cluster.selectDatabaseLocked(selector)
	if target == nil || target != expected {
		return nil, nil, databaseAdmissionError("3D000", fmt.Sprintf(`database "%s" does not exist`, selector.Name))
	}
	if target.Cluster != cluster || target.Mu != e.Mu || cluster.Targets[target.DatabaseName] != target || target.RoleStore != e.RoleStore {
		return nil, nil, errors.New("stuntgres: foreign database admission target")
	}
	if target.DatabaseNameError != nil {
		return nil, nil, target.DatabaseNameError
	}
	if target.retired || target.dropAttempt != nil {
		return nil, nil, databaseAdmissionError("3D000", fmt.Sprintf(`database "%s" does not exist`, target.DatabaseName))
	}
	view := target.Catalog.Current()
	roles := target.RoleStore.PublishedRead(&target.Catalog.Owner, view)
	database := view.MustDatabase(target.Catalog.DatabaseAddress())
	if database.Name() != target.DatabaseName || database.ConnectionLimit < -1 {
		return nil, nil, errors.New("stuntgres: invalid database admission descriptor")
	}
	if !database.AllowConnections {
		return nil, nil, databaseAdmissionError("55000", fmt.Sprintf(`database "%s" is not currently accepting connections`, database.Name()))
	}
	if err := access.CheckDatabaseConnect(ctx, view, roles, principal); err != nil {
		return nil, nil, err
	}
	cluster.AdmissionMu.Lock()
	defer cluster.AdmissionMu.Unlock()
	if readers := cluster.templateCopies[target]; readers != nil {
		return nil, readers.done, nil
	}
	if !descriptor.Superuser && database.ConnectionLimit >= 0 && cluster.admissions[target] >= int(database.ConnectionLimit) {
		return nil, nil, databaseAdmissionError("53300", fmt.Sprintf(`too many connections for database "%s"`, database.Name()))
	}
	if !descriptor.Superuser && descriptor.ConnectionLimit >= 0 && cluster.principalAdmissions[principal] >= int(descriptor.ConnectionLimit) {
		return nil, nil, role.ConnectionLimitError(descriptor.Name)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	a := &DatabaseAdmission{cluster: cluster, target: target, database: database.Address(), name: database.Name(), principal: principal, roles: roles, record: target.RoleStore.Published, token: token, ticket: ticket}
	a.self = a
	if cluster.admissions == nil {
		cluster.admissions = make(map[*Engine]int)
	}
	cluster.admissions[target]++
	if cluster.principalAdmissions == nil {
		cluster.principalAdmissions = make(map[authorization.PrincipalRef]int)
	}
	cluster.principalAdmissions[principal]++
	a.previous = cluster.admissionTail
	if cluster.admissionTail != nil {
		cluster.admissionTail.next = a
	} else {
		cluster.admissionHead = a
	}
	cluster.admissionTail = a
	return a, nil, nil
}

func (a *DatabaseAdmission) claim(e *Engine, id ids.SessionID) error {
	if a == nil || a.self != a || a.cluster == nil || a.target != e || e.Cluster != a.cluster || e.Catalog.DatabaseAddress() != a.database || a.token != e.Catalog.Domain.Session(id) {
		return errors.New("stuntgres: invalid database admission")
	}
	a.cluster.AdmissionMu.Lock()
	defer a.cluster.AdmissionMu.Unlock()
	if a.revoked {
		return databaseAdmissionError("3D000", fmt.Sprintf(`database "%s" does not exist`, a.name))
	}
	if a.state != 0 {
		return errors.New("stuntgres: database admission already submitted or released")
	}
	a.state = 1
	return nil
}

// Release abandons only an unsubmitted owning reservation. A submitted token is
// released solely by canonical terminal cleanup, never a caller's deferred Release.
func (a *DatabaseAdmission) Release() { a.release(false) }

func (a *DatabaseAdmission) release(consumed bool) {
	if a == nil || a.self != a || a.cluster == nil {
		return
	}
	a.cluster.AdmissionMu.Lock()
	if a.state == 2 || (a.state == 1 && !consumed) || (a.state == 0 && consumed) {
		a.cluster.AdmissionMu.Unlock()
		return
	}
	a.state = 2
	a.owner = nil
	a.unlinkLocked()
	a.cluster.admissions[a.target]--
	if a.cluster.admissions[a.target] == 0 {
		delete(a.cluster.admissions, a.target)
	}
	a.cluster.principalAdmissions[a.principal]--
	if a.cluster.principalAdmissions[a.principal] == 0 {
		delete(a.cluster.principalAdmissions, a.principal)
	}
	ticket := a.ticket
	a.ticket = nil
	a.cluster.AdmissionMu.Unlock()
	if ticket != nil {
		a.target.Locks.End(ticket)
	}
}

func (a *DatabaseAdmission) defaults() map[string]session.CapturedDefault {
	return capturedRoleDefaults(a.record, a.database, a.principal)
}

func (e *Engine) DatabaseResource(address ids.ObjectAddress) lock.Resource {
	return lock.Resource{ObjectRef: e.Cluster.RoleDomain.Ref(1, address)}
}

func (s *StatementScope) AcquireDatabase(address ids.ObjectAddress) error {
	if address.ClassID != ids.DatabaseClassID || address.ObjectID == 0 || address.SubID != 0 {
		return errors.New("stuntgres: invalid database utility identity")
	}
	return s.AcquireRoleResource(s.Engine().DatabaseResource(address), lock.AccessExclusive)
}

// ACL writers exclude rename without hiding committed rights from startup.
// Concurrent writers/copies retain exact predecessor and authority validation.
func (s *StatementScope) AcquireDatabaseACL(address ids.ObjectAddress) error {
	if address.ClassID != ids.DatabaseClassID || address.ObjectID == 0 || address.SubID != 0 {
		return errors.New("stuntgres: invalid database ACL identity")
	}
	return s.AcquireRoleResource(s.Engine().DatabaseResource(address), lock.AccessShare)
}

func (s *StatementScope) AcquireDatabaseName(name string) error {
	e := s.Engine()
	resource := lock.Resource{ObjectRef: e.Cluster.RoleDomain.Ref(1, ids.ObjectAddress{}), Name: ids.NameKey{ClassID: ids.DatabaseClassID, Name: name}}
	return s.AcquireRoleResource(resource, lock.AccessExclusive)
}
