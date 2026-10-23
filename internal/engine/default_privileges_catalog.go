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
func (e *Engine) stageDefaultChangesLocked(s *CatalogStage, c *session.Journal) {
	for key, record := range s.DefaultReads() {
		if _, private := c.DefaultACLs[key]; private {
			continue
		}
		if c.DefaultReads == nil {
			c.DefaultReads = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
		}
		if _, exists := c.DefaultReads[key]; !exists {
			c.DefaultReads[key] = record
		}
	}
	for key, record := range s.DefaultACLs() {
		e.defaultACLChangeLocked(c, key, record)
	}
}

func (e *Engine) creationACLChangesLocked(c *session.Journal, view catalog.Read, owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	return c.ObserveCreationACL(e.catalog.state, view, owner, schema, class)
}
