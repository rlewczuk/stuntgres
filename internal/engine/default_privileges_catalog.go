package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func (e *Engine) defaultACLChangeLocked(c *session.Journal, key catalog.DefaultACLKey, record *catalog.DefaultACLRecord) {
	c.RecordDefaultACL(e.catalog.state, key, record)
}

func (e *Engine) creationACLChangesLocked(c *session.Journal, view catalog.Read, owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	return c.ObserveCreationACL(e.catalog.state, view, owner, schema, class)
}
