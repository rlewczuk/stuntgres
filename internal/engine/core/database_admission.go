package core

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
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
	self      *DatabaseAdmission
	cluster   *DatabaseCluster
	target    *Engine
	database  ids.ObjectAddress
	principal authorization.PrincipalRef
	roles     authorization.Read
	record    *access.RoleRecord
	state     uint8 // 0 reserved, 1 consumed, 2 released
}

func (a *DatabaseAdmission) Target() *Engine {
	if a == nil || a.self != a {
		return nil
	}
	return a.target
}

func databaseAdmissionError(code, message string) error {
	return &sqlstate.Error{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: code, Message: message}
}

func (e *Engine) AdmitDatabase(ctx context.Context, principal authorization.PrincipalRef, selector DatabaseSelector) (*DatabaseAdmission, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	cluster := e.Cluster
	if cluster == nil || e.Mu != &cluster.Mu || e.RoleStore != &cluster.Roles {
		return nil, errors.New("stuntgres: unregistered database admission")
	}
	descriptor, err := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).Descriptor(principal)
	if err != nil {
		return nil, err
	}
	if !descriptor.Login {
		return nil, role.LoginError(descriptor.Name)
	}
	target := cluster.DefaultTarget
	if selector.Explicit {
		target = cluster.Targets[selector.Name]
	}
	if target == nil {
		return nil, databaseAdmissionError("3D000", fmt.Sprintf(`database "%s" does not exist`, selector.Name))
	}
	if target.Cluster != cluster || target.Mu != e.Mu || cluster.Targets[target.DatabaseName] != target || target.RoleStore != e.RoleStore {
		return nil, errors.New("stuntgres: foreign database admission target")
	}
	if target.DatabaseNameError != nil {
		return nil, target.DatabaseNameError
	}
	view := target.Catalog.Current()
	roles := target.RoleStore.PublishedRead(&target.Catalog.Owner, view)
	database := view.MustDatabase(target.Catalog.DatabaseAddress())
	if database.Name() != target.DatabaseName || database.ConnectionLimit < -1 {
		return nil, errors.New("stuntgres: invalid database admission descriptor")
	}
	if !database.AllowConnections {
		return nil, databaseAdmissionError("55000", fmt.Sprintf(`database "%s" is not currently accepting connections`, database.Name()))
	}
	if err := access.CheckDatabaseConnect(ctx, view, roles, principal); err != nil {
		return nil, err
	}
	cluster.AdmissionMu.Lock()
	defer cluster.AdmissionMu.Unlock()
	if !descriptor.Superuser && database.ConnectionLimit >= 0 && cluster.admissions[target] >= int(database.ConnectionLimit) {
		return nil, databaseAdmissionError("53300", fmt.Sprintf(`too many connections for database "%s"`, database.Name()))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a := &DatabaseAdmission{cluster: cluster, target: target, database: database.Address(), principal: principal, roles: roles, record: target.RoleStore.Published}
	a.self = a
	if cluster.admissions == nil {
		cluster.admissions = make(map[*Engine]int)
	}
	cluster.admissions[target]++
	return a, nil
}

func (a *DatabaseAdmission) claim(e *Engine) error {
	if a == nil || a.self != a || a.cluster == nil || a.target != e || e.Cluster != a.cluster || e.Catalog.DatabaseAddress() != a.database {
		return errors.New("stuntgres: invalid database admission")
	}
	a.cluster.AdmissionMu.Lock()
	defer a.cluster.AdmissionMu.Unlock()
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
	defer a.cluster.AdmissionMu.Unlock()
	if a.state == 2 || (a.state == 1 && !consumed) || (a.state == 0 && consumed) {
		return
	}
	a.state = 2
	a.cluster.admissions[a.target]--
	if a.cluster.admissions[a.target] == 0 {
		delete(a.cluster.admissions, a.target)
	}
}

func (a *DatabaseAdmission) defaults() map[string]session.CapturedDefault {
	return capturedRoleDefaults(a.record, a.database, a.principal)
}
