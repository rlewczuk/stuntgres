package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
)

type defaultACLKey struct {
	creator authorization.PrincipalRef
	schema  ObjectAddress
	class   authorization.DefaultACLClass
}
type defaultACLRecord struct{ acl authorization.ACLValue }

func cloneDefaultACLs(source map[defaultACLKey]*defaultACLRecord) map[defaultACLKey]*defaultACLRecord {
	next := make(map[defaultACLKey]*defaultACLRecord, len(source))
	for key, record := range source {
		next[key] = record
	}
	return next
}
func overlayDefaultACLs(source map[defaultACLKey]*defaultACLRecord, changes map[defaultACLKey]*defaultACLRecord) map[defaultACLKey]*defaultACLRecord {
	if len(changes) == 0 {
		return source
	}
	next := cloneDefaultACLs(source)
	for key, record := range changes {
		if record == nil {
			delete(next, key)
		} else {
			next[key] = record
		}
	}
	return next
}

func creationDefaultACL(view CatalogRead, owner authorization.PrincipalRef, schema ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	var global, scoped *authorization.ACLValue
	if record := view.state.defaultACLs[defaultACLKey{creator: owner, class: class}]; record != nil {
		global = &record.acl
	}
	if schema != (ObjectAddress{}) {
		if record := view.state.defaultACLs[defaultACLKey{creator: owner, schema: schema, class: class}]; record != nil {
			scoped = &record.acl
		}
	}
	return authorization.CreationDefaultACL(owner, class, global, scoped)
}
func validateDefaultACLs(roles authorization.Read, view CatalogRead) error {
	for key, record := range view.state.defaultACLs {
		if record == nil {
			return fmt.Errorf("stuntgres: invalid default ACL record")
		}
		if err := authorization.ValidateDefaultACLHeader(roles, key.creator, key.class, key.schema != (ObjectAddress{}), record.acl); err != nil {
			return err
		}
		if key.schema != (ObjectAddress{}) {
			if _, ok := view.state.descriptors[key.schema].(*namespaceDescriptor); !ok {
				return CatalogConflict{}
			}
		}
		if err := authorization.ValidateDefaultACL(roles, key.creator, key.class, key.schema != (ObjectAddress{}), record.acl); err != nil {
			return err
		}
	}
	return nil
}
