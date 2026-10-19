package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func (s *CatalogStage) creationACL(owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	for _, namespace := range []ids.ObjectAddress{{}, schema} {
		key := catalog.DefaultACLKey{Creator: owner, Schema: namespace, Class: class}
		if s.DefaultReads == nil {
			s.DefaultReads = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
		}
		if _, exists := s.DefaultReads[key]; !exists {
			s.DefaultReads[key] = s.Base.State().DefaultACL(key)
		}
		if schema == (ids.ObjectAddress{}) {
			break
		}
	}
	return catalog.CreationDefaultACL(s.Base, owner, schema, class)
}

// CreationDefaultACL normalizes baseline-equal defaults. Types retain a
// materialized baseline for effective USAGE checks, so remember at creation
// whether that value represents raw catalog absence rather than an explicit ACL.
func (s *CatalogStage) creationTypeACL(owner authorization.PrincipalRef, schema ids.ObjectAddress) (authorization.ACLValue, bool) {
	acl := s.creationACL(owner, schema, authorization.DefaultTypes)
	return acl, authorization.MatchesDefaultBaseline(acl, owner, authorization.DefaultTypes, false)
}
func (e *Engine) defaultACLChangeLocked(c *catalogChanges, key catalog.DefaultACLKey, record *catalog.DefaultACLRecord) {
	if c.defaultOriginals == nil {
		c.defaultOriginals = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
		c.defaultACLs = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
	}
	if _, exists := c.defaultOriginals[key]; !exists {
		c.defaultOriginals[key] = e.catalog.state.DefaultACL(key)
	}
	c.defaultACLs[key] = record
	c.cachedBase = nil
	c.cached = catalog.Read{}
}
func cloneDefaultChanges(source *catalogChanges, next *catalogChanges) {
	if len(source.defaultACLs) != 0 {
		next.defaultACLs = catalog.CloneDefaultACLs(source.defaultACLs)
		next.defaultOriginals = catalog.CloneDefaultACLs(source.defaultOriginals)
	}
	if len(source.defaultReads) != 0 {
		next.defaultReads = catalog.CloneDefaultACLs(source.defaultReads)
	}
}
func (e *Engine) stageDefaultChangesLocked(s *CatalogStage, c *catalogChanges) {
	for key, record := range s.DefaultReads {
		if _, private := c.defaultACLs[key]; private {
			continue
		}
		if c.defaultReads == nil {
			c.defaultReads = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
		}
		if _, exists := c.defaultReads[key]; !exists {
			c.defaultReads[key] = record
		}
	}
	for key, record := range s.DefaultACLs {
		e.defaultACLChangeLocked(c, key, record)
	}
}

func (s *CatalogStage) defaultPreflight(current *catalog.State) error {
	if !catalog.DefaultRecordsUnchanged(current, s.DefaultReads) {
		return catalog.Conflict{}
	}
	for key := range current.DefaultACLs() {
		if _, removed := s.Removals[key.Schema]; !removed {
			continue
		}
		if _, observed := s.DefaultReads[key]; !observed {
			return catalog.Conflict{}
		}
	}
	return nil
}
func (s *CatalogStage) removeSchemaDefaults(schema ids.ObjectAddress) {
	for key, record := range s.Base.State().DefaultACLs() {
		if key.Schema != schema {
			continue
		}
		if s.DefaultACLs == nil {
			s.DefaultACLs = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
		}
		if s.DefaultReads == nil {
			s.DefaultReads = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
		}
		s.DefaultReads[key] = record
		s.DefaultACLs[key] = nil
	}
}
func defaultRoleDependencies(view catalog.Read, roles authorization.Read, ref authorization.PrincipalRef, deps *[]roleDependency) bool {
	for key, record := range view.State().DefaultACLs() {
		kind := byte('a')
		dependent := key.Creator == ref
		if dependent {
			kind = 'o'
		} else {
			for _, entry := range record.ACL.Entries {
				if entry.Grantor == ref || !entry.Grantee.Public && entry.Grantee.Role == ref {
					dependent = true
					break
				}
			}
		}
		if !dependent {
			continue
		}
		if deps == nil {
			return true
		}
		creator, _ := roles.Descriptor(key.Creator)
		class := [...]string{"", "relations", "sequences", "functions", "types", "schemas", "large objects"}[key.Class]
		description := "default privileges on new " + class + " belonging to role " + creator.Name
		if key.Schema != (ids.ObjectAddress{}) {
			description += " in schema " + view.Get(key.Schema).Name()
		}
		prefix := "privileges for "
		if kind == 'o' {
			prefix = "owner of "
		}
		*deps = append(*deps, roleDependency{Kind: kind, Description: prefix + description})
	}
	return false
}
func (e *Engine) creationACLChangesLocked(c *catalogChanges, view catalog.Read, owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	for _, namespace := range []ids.ObjectAddress{{}, schema} {
		key := catalog.DefaultACLKey{Creator: owner, Schema: namespace, Class: class}
		if _, private := c.defaultACLs[key]; !private {
			if c.defaultReads == nil {
				c.defaultReads = make(map[catalog.DefaultACLKey]*catalog.DefaultACLRecord)
			}
			if _, exists := c.defaultReads[key]; !exists {
				c.defaultReads[key] = e.catalog.state.DefaultACL(key)
			}
		}
		if schema == (ids.ObjectAddress{}) {
			break
		}
	}
	return catalog.CreationDefaultACL(view, owner, schema, class)
}
