package catalog

import (
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func DefaultRecordsUnchanged(current *State, reads map[DefaultACLKey]*DefaultACLRecord) bool {
	for key, record := range reads {
		if current.defaultACLs[key] != record {
			return false
		}
	}
	return true
}

// Projection removes namespace-owned directives with their namespaces. Physical
// retirement removes their directives while retaining empty namespace metadata.
// Invalid already-orphaned keys remain for validation.
func PruneNamespaceDefaults(source, next *State, retired []ids.ObjectAddress) {
	copied := false
	for key := range next.defaultACLs {
		if key.Schema == (ids.ObjectAddress{}) || source.Get(key.Schema) == nil || next.Get(key.Schema) != nil && !slices.Contains(retired, key.Schema) {
			continue
		}
		if !copied {
			next.defaultACLs = CloneDefaultACLs(next.defaultACLs)
			copied = true
		}
		delete(next.defaultACLs, key)
	}
}

func RemapDefaultSchemas(state *State, mapping map[ids.ObjectAddress]ids.ObjectAddress) {
	original := state.defaultACLs
	copied := false
	for key, record := range original {
		destination, changed := mapping[key.Schema]
		if !changed {
			continue
		}
		if !copied {
			state.defaultACLs = CloneDefaultACLs(original)
			copied = true
		}
		delete(state.defaultACLs, key)
		key.Schema = destination
		state.defaultACLs[key] = record
	}
}

// CreationACL returns the default ACL of a new object of class that owner
// creates in schema, and records the global and schema default records it read
// as the candidate's observations. The root calls it under Engine.mu.
func (c *Candidate) CreationACL(owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	for _, namespace := range []ids.ObjectAddress{{}, schema} {
		key := DefaultACLKey{Creator: owner, Schema: namespace, Class: class}
		c.ObserveDefault(key)
		if schema == (ids.ObjectAddress{}) {
			break
		}
	}
	return CreationDefaultACL(c.Base, owner, schema, class)
}

// CreationDefaultACL normalizes baseline-equal defaults. Types retain a
// materialized baseline for effective USAGE checks, so remember at creation
// whether that value represents raw catalog absence rather than an explicit ACL.
func (c *Candidate) CreationTypeACL(owner authorization.PrincipalRef, schema ids.ObjectAddress) (authorization.ACLValue, bool) {
	acl := c.CreationACL(owner, schema, authorization.DefaultTypes)
	return acl, authorization.MatchesDefaultBaseline(acl, owner, authorization.DefaultTypes, false)
}

// RemoveSchemaDefaults stages the removal of every default ACL record of
// schema, observing each one first.
func (c *Candidate) RemoveSchemaDefaults(schema ids.ObjectAddress) {
	for key := range c.Base.State().DefaultACLs() {
		if key.Schema != schema {
			continue
		}
		c.ObserveDefault(key)
		c.SetDefaultACL(key, nil)
	}
}
