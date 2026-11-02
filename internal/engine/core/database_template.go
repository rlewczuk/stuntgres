package core

import (
	"fmt"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

type databaseTemplateReaders struct {
	count int
	done  chan struct{}
}

// DatabaseTemplateLease excludes new physical source admissions, not other
// copies. Identity and once-only release belong exclusively to AdmissionMu.
// The caller holds source replacement read protection for the lease lifetime.
type DatabaseTemplateLease struct {
	self     *DatabaseTemplateLease
	cluster  *DatabaseCluster
	source   *Engine
	readers  *databaseTemplateReaders
	released bool
}

// AcquireDatabaseTemplateLocked requires the shared Mu. Physical reservations
// include startup and terminal cleanup; only the actual consumed executor token
// is excluded. No Session mutex is taken under AdmissionMu.
func (e *Engine) AcquireDatabaseTemplateLocked(executor *session.Session) (*DatabaseTemplateLease, error) {
	cluster := e.Cluster
	if cluster == nil || e.Mu != &cluster.Mu || e.RoleStore != &cluster.Roles || cluster.Targets[e.DatabaseName] != e {
		return nil, fmt.Errorf("stuntgres: unregistered template source")
	}
	cluster.AdmissionMu.Lock()
	defer cluster.AdmissionMu.Unlock()
	users := cluster.admissions[e]
	if executor != nil {
		if payload := PayloadOf(executor); payload != nil {
			a := payload.admission
			if a != nil && a.self == a && a.owner == executor && a.cluster == cluster && a.target == e && a.database == e.Catalog.DatabaseAddress() && a.state == 1 {
				users--
			}
		}
	}
	prepared := e.prepared.Len()
	if users != 0 || prepared != 0 {
		var detail string
		switch {
		case users != 0 && prepared != 0:
			detail = fmt.Sprintf("There are %d other session(s) and %d prepared transaction(s) using the database.", users, prepared)
		case users == 1:
			detail = "There is 1 other session using the database."
		case users > 1:
			detail = fmt.Sprintf("There are %d other sessions using the database.", users)
		case prepared == 1:
			detail = "There is 1 prepared transaction using the database."
		default:
			detail = fmt.Sprintf("There are %d prepared transactions using the database.", prepared)
		}
		return nil, &sqlstate.Error{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55006",
			Message: fmt.Sprintf("source database \"%s\" is being accessed by other users", e.DatabaseName), Detail: detail,
		}
	}
	if cluster.templateCopies == nil {
		cluster.templateCopies = make(map[*Engine]*databaseTemplateReaders)
	}
	readers := cluster.templateCopies[e]
	if readers == nil {
		readers = &databaseTemplateReaders{done: make(chan struct{})}
		cluster.templateCopies[e] = readers
	}
	readers.count++
	lease := &DatabaseTemplateLease{cluster: cluster, source: e, readers: readers}
	lease.self = lease
	return lease, nil
}

// Release never acquires Mu, so canonical physical cleanup and copy cleanup
// cannot invert the metadata/admission lock order. Foreign/copied handles lack
// release authority even if all their visible fields compare equal.
func (l *DatabaseTemplateLease) Release() {
	if l == nil || l.self != l || l.cluster == nil {
		return
	}
	l.cluster.AdmissionMu.Lock()
	defer l.cluster.AdmissionMu.Unlock()
	if l.released || l.cluster.templateCopies[l.source] != l.readers {
		return
	}
	l.released = true
	l.readers.count--
	if l.readers.count == 0 {
		delete(l.cluster.templateCopies, l.source)
		close(l.readers.done)
	}
}
