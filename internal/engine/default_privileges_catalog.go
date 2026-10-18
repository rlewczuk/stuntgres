package engine

import (
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func (s *CatalogStage) creationACL(owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	for _, namespace := range []ids.ObjectAddress{{}, schema} {
		key := defaultACLKey{Creator: owner, Schema: namespace, Class: class}
		if s.defaultReads == nil {
			s.defaultReads = make(map[defaultACLKey]*defaultACLRecord)
		}
		if _, exists := s.defaultReads[key]; !exists {
			s.defaultReads[key] = s.base.state.defaultACLs[key]
		}
		if schema == (ids.ObjectAddress{}) {
			break
		}
	}
	return creationDefaultACL(s.base, owner, schema, class)
}

// CreationDefaultACL normalizes baseline-equal defaults. Types retain a
// materialized baseline for effective USAGE checks, so remember at creation
// whether that value represents raw catalog absence rather than an explicit ACL.
func (s *CatalogStage) creationTypeACL(owner authorization.PrincipalRef, schema ids.ObjectAddress) (authorization.ACLValue, bool) {
	acl := s.creationACL(owner, schema, authorization.DefaultTypes)
	return acl, authorization.MatchesDefaultBaseline(acl, owner, authorization.DefaultTypes, false)
}
func (e *Engine) defaultACLChangeLocked(c *catalogChanges, key defaultACLKey, record *defaultACLRecord) {
	if c.defaultOriginals == nil {
		c.defaultOriginals = make(map[defaultACLKey]*defaultACLRecord)
		c.defaultACLs = make(map[defaultACLKey]*defaultACLRecord)
	}
	if _, exists := c.defaultOriginals[key]; !exists {
		c.defaultOriginals[key] = e.catalog.state.defaultACLs[key]
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
		if _, removed := s.removals[key.Schema]; !removed {
			continue
		}
		if _, observed := s.defaultReads[key]; !observed {
			return CatalogConflict{}
		}
	}
	return nil
}
func (s *CatalogStage) removeSchemaDefaults(schema ids.ObjectAddress) {
	for key, record := range s.base.state.defaultACLs {
		if key.Schema != schema {
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
		*deps = append(*deps, roleDependency{kind: kind, description: prefix + description})
	}
	return false
}
func (e *Engine) creationACLChangesLocked(c *catalogChanges, view CatalogRead, owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	for _, namespace := range []ids.ObjectAddress{{}, schema} {
		key := defaultACLKey{Creator: owner, Schema: namespace, Class: class}
		if _, private := c.defaultACLs[key]; !private {
			if c.defaultReads == nil {
				c.defaultReads = make(map[defaultACLKey]*defaultACLRecord)
			}
			if _, exists := c.defaultReads[key]; !exists {
				c.defaultReads[key] = e.catalog.state.defaultACLs[key]
			}
		}
		if schema == (ids.ObjectAddress{}) {
			break
		}
	}
	return creationDefaultACL(view, owner, schema, class)
}

// Projection removes namespace-owned directives with their namespaces. Physical
// retirement removes their directives while retaining empty namespace metadata.
// Invalid already-orphaned keys remain for validation.
func pruneNamespaceDefaults(source, next *catalogState, retired []ids.ObjectAddress) {
	copied := false
	for key := range next.defaultACLs {
		if key.Schema == (ids.ObjectAddress{}) || source.Get(key.Schema) == nil || next.Get(key.Schema) != nil && !slices.Contains(retired, key.Schema) {
			continue
		}
		if !copied {
			next.defaultACLs = cloneDefaultACLs(next.defaultACLs)
			copied = true
		}
		delete(next.defaultACLs, key)
	}
}

func remapDefaultSchemas(state *catalogState, mapping map[ids.ObjectAddress]ids.ObjectAddress) {
	original := state.defaultACLs
	copied := false
	for key, record := range original {
		destination, changed := mapping[key.Schema]
		if !changed {
			continue
		}
		if !copied {
			state.defaultACLs = cloneDefaultACLs(original)
			copied = true
		}
		delete(state.defaultACLs, key)
		key.Schema = destination
		state.defaultACLs[key] = record
	}
}
