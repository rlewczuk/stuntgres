package engine

import "stuntgres/internal/engine/authorization"

func (s *CatalogStage) creationACL(owner authorization.PrincipalRef, schema ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	for _, namespace := range []ObjectAddress{{}, schema} {
		key := defaultACLKey{creator: owner, schema: namespace, class: class}
		if s.defaultReads == nil {
			s.defaultReads = make(map[defaultACLKey]*defaultACLRecord)
		}
		if _, exists := s.defaultReads[key]; !exists {
			s.defaultReads[key] = s.base.state.defaultACLs[key]
		}
		if schema == (ObjectAddress{}) {
			break
		}
	}
	return creationDefaultACL(s.base, owner, schema, class)
}
func (e *Engine) defaultACLChangeLocked(c *catalogChanges, key defaultACLKey, record *defaultACLRecord) {
	if c.defaultOriginals == nil {
		c.defaultOriginals = make(map[defaultACLKey]*defaultACLRecord)
		c.defaultACLs = make(map[defaultACLKey]*defaultACLRecord)
	}
	if _, exists := c.defaultOriginals[key]; !exists {
		c.defaultOriginals[key] = e.state.defaultACLs[key]
	}
	c.defaultACLs[key] = record
	c.cachedBase = nil
	c.cached = CatalogRead{}
}
func cloneDefaultChanges(source *catalogChanges, next *catalogChanges) {
	if len(source.defaultACLs) != 0 {
		next.defaultACLs = cloneDefaultACLs(source.defaultACLs)
		next.defaultOriginals = cloneDefaultACLs(source.defaultOriginals)
	}
	if len(source.defaultReads) != 0 {
		next.defaultReads = cloneDefaultACLs(source.defaultReads)
	}
}
func (e *Engine) stageDefaultChangesLocked(s *CatalogStage, c *catalogChanges) {
	for key, record := range s.defaultReads {
		if _, private := c.defaultACLs[key]; private {
			continue
		}
		if c.defaultReads == nil {
			c.defaultReads = make(map[defaultACLKey]*defaultACLRecord)
		}
		if _, exists := c.defaultReads[key]; !exists {
			c.defaultReads[key] = record
		}
	}
	for key, record := range s.defaultACLs {
		e.defaultACLChangeLocked(c, key, record)
	}
}
func defaultRecordsUnchanged(current *catalogState, reads map[defaultACLKey]*defaultACLRecord) bool {
	for key, record := range reads {
		if current.defaultACLs[key] != record {
			return false
		}
	}
	return true
}
func (s *CatalogStage) defaultPreflight(current *catalogState) error {
	if !defaultRecordsUnchanged(current, s.defaultReads) {
		return CatalogConflict{}
	}
	for key := range current.defaultACLs {
		if _, removed := s.removals[key.schema]; !removed {
			continue
		}
		if _, observed := s.defaultReads[key]; !observed {
			return CatalogConflict{}
		}
	}
	return nil
}
func (s *CatalogStage) removeSchemaDefaults(schema ObjectAddress) {
	for key, record := range s.base.state.defaultACLs {
		if key.schema != schema {
			continue
		}
		if s.defaultACLs == nil {
			s.defaultACLs = make(map[defaultACLKey]*defaultACLRecord)
		}
		if s.defaultReads == nil {
			s.defaultReads = make(map[defaultACLKey]*defaultACLRecord)
		}
		s.defaultReads[key] = record
		s.defaultACLs[key] = nil
	}
}
func defaultRoleDependencies(view CatalogRead, roles authorization.Read, ref authorization.PrincipalRef, deps *[]roleDependency) bool {
	for key, record := range view.state.defaultACLs {
		kind := byte('a')
		dependent := key.creator == ref
		if dependent {
			kind = 'o'
		} else {
			for _, entry := range record.acl.Entries {
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
		creator, _ := roles.Descriptor(key.creator)
		class := [...]string{"", "relations", "sequences", "functions", "types", "schemas", "large objects"}[key.class]
		description := "default privileges on new " + class + " belonging to role " + creator.Name
		if key.schema != (ObjectAddress{}) {
			description += " in schema " + view.state.descriptors[key.schema].Name()
		}
		prefix := "privileges for "
		if kind == 'o' {
			prefix = "owner of "
		}
		*deps = append(*deps, roleDependency{kind: kind, description: prefix + description})
	}
	return false
}
func (e *Engine) creationACLChangesLocked(c *catalogChanges, view CatalogRead, owner authorization.PrincipalRef, schema ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	for _, namespace := range []ObjectAddress{{}, schema} {
		key := defaultACLKey{creator: owner, schema: namespace, class: class}
		if _, private := c.defaultACLs[key]; !private {
			if c.defaultReads == nil {
				c.defaultReads = make(map[defaultACLKey]*defaultACLRecord)
			}
			if _, exists := c.defaultReads[key]; !exists {
				c.defaultReads[key] = e.state.defaultACLs[key]
			}
		}
		if schema == (ObjectAddress{}) {
			break
		}
	}
	return creationDefaultACL(view, owner, schema, class)
}

// Projection and session retirement remove namespace-owned directives along
// with their namespaces. Invalid already-orphaned keys remain for validation.
func pruneRemovedNamespaceDefaults(source, next *catalogState) {
	copied := false
	for key := range next.defaultACLs {
		if key.schema == (ObjectAddress{}) || source.descriptors[key.schema] == nil || next.descriptors[key.schema] != nil {
			continue
		}
		if !copied {
			next.defaultACLs = cloneDefaultACLs(next.defaultACLs)
			copied = true
		}
		delete(next.defaultACLs, key)
	}
}

func remapDefaultSchemas(state *catalogState, mapping map[ObjectAddress]ObjectAddress) {
	original := state.defaultACLs
	copied := false
	for key, record := range original {
		destination, changed := mapping[key.schema]
		if !changed {
			continue
		}
		if !copied {
			state.defaultACLs = cloneDefaultACLs(original)
			copied = true
		}
		delete(state.defaultACLs, key)
		key.schema = destination
		state.defaultACLs[key] = record
	}
}
