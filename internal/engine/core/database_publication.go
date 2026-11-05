package core

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// DatabasePublication owns all fallible preparation. InstallLocked runs only in
// the surrounding catalog/row publisher's callback-free final installation.
type DatabasePublication struct {
	Roots   catalog.SharedProjection
	Targets map[string]*Engine
	Renames map[*Engine]string
}

func (e *Engine) PrepareDatabasePublicationLocked(next *catalog.State) (DatabasePublication, error) {
	p := DatabasePublication{}
	for oldName, target := range e.Cluster.Targets {
		old := target.Catalog.State.MustDatabase(target.Catalog.DatabaseAddress())
		descriptor, ok := next.Get(old.Address()).(*catalog.Database)
		if !ok {
			return p, fmt.Errorf("stuntgres: lost registered database authority")
		}
		if descriptor.Name() == oldName {
			continue
		}
		if err := target.DatabaseBusyLocked(nil, false); err != nil {
			return p, err
		}
		if p.Targets == nil {
			p.Targets = make(map[string]*Engine, len(e.Cluster.Targets))
			p.Renames = make(map[*Engine]string)
			for name, runtime := range e.Cluster.Targets {
				p.Targets[name] = runtime
			}
		}
		delete(p.Targets, oldName)
		p.Renames[target] = descriptor.Name()
	}
	for target, name := range p.Renames {
		if p.Targets[name] != nil {
			return DatabasePublication{}, SQLError("42P04", fmt.Sprintf(`database "%s" already exists`, name))
		}
		p.Targets[name] = target
	}
	p.Roots = e.Catalog.ProjectSharedLocked(next, e.Cluster.Catalogs)
	return p, nil
}

func (p DatabasePublication) InstallLocked(e *Engine) {
	p.Roots.InstallLocked()
	if p.Targets != nil {
		e.Cluster.Targets = p.Targets
		for target, name := range p.Renames {
			target.DatabaseName = name
		}
	}
}

// DatabaseTargetLocked resolves a projected database identity to its stable
// runtime, including a transaction-private name which is not registered yet.
func (e *Engine) DatabaseTargetLocked(address ids.ObjectAddress) *Engine {
	for _, target := range e.Cluster.Targets {
		if target.Catalog.DatabaseAddress() == address {
			return target
		}
	}
	return nil
}

// DatabaseBusyLocked counts canonical physical admissions, including reserved,
// initializing and ending connections. Only an exact consumed issuer is exempt.
// The caller holds cluster metadata; AdmissionMu never nests a Session mutex.
func (e *Engine) DatabaseBusyLocked(issuer *session.Session, source bool) error {
	e.Cluster.AdmissionMu.Lock()
	count := e.Cluster.admissions[e]
	if issuer != nil {
		if payload := PayloadOf(issuer); payload != nil {
			a := payload.admission
			if a != nil && a.self == a && a.owner == issuer && a.cluster == e.Cluster && a.target == e && a.database == e.Catalog.DatabaseAddress() && a.state == 1 {
				count--
			}
		}
	}
	e.Cluster.AdmissionMu.Unlock()
	prepared := e.prepared.Len()
	if count == 0 && prepared == 0 {
		return nil
	}
	prefix := "database"
	if source {
		prefix = "source database"
	}
	message := fmt.Sprintf(`%s "%s" is being accessed by other users`, prefix, e.DatabaseName)
	detail := ""
	switch {
	case count > 0 && prepared > 0:
		detail = fmt.Sprintf("There are %d other session(s) and %d prepared transaction(s) using the database.", count, prepared)
	case count > 0:
		if count == 1 {
			detail = "There is 1 other session using the database."
		} else {
			detail = fmt.Sprintf("There are %d other sessions using the database.", count)
		}
	default:
		if prepared == 1 {
			detail = "There is 1 prepared transaction using the database."
		} else {
			detail = fmt.Sprintf("There are %d prepared transactions using the database.", prepared)
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55006", Message: message, Detail: detail}
}
