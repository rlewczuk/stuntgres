package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

type defaultACLKey struct {
	Creator authorization.PrincipalRef
	Schema  ids.ObjectAddress
	Class   authorization.DefaultACLClass
}
type defaultACLRecord struct{ ACL authorization.ACLValue }

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

func creationDefaultACL(view CatalogRead, owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	var global, scoped *authorization.ACLValue
	if record := view.state.defaultACLs[defaultACLKey{Creator: owner, Class: class}]; record != nil {
		global = &record.ACL
	}
	if schema != (ids.ObjectAddress{}) {
		if record := view.state.defaultACLs[defaultACLKey{Creator: owner, Schema: schema, Class: class}]; record != nil {
			scoped = &record.ACL
		}
	}
	return authorization.CreationDefaultACL(owner, class, global, scoped)
}
func validateDefaultACLs(roles authorization.Read, view CatalogRead) error {
	for key, record := range view.state.defaultACLs {
		if record == nil {
			return fmt.Errorf("stuntgres: invalid default ACL record")
		}
		if err := authorization.ValidateDefaultACLHeader(roles, key.Creator, key.Class, key.Schema != (ids.ObjectAddress{}), record.ACL); err != nil {
			return err
		}
		if key.Schema != (ids.ObjectAddress{}) {
			if _, ok := view.Namespace(key.Schema); !ok {
				return CatalogConflict{}
			}
		}
		if err := authorization.ValidateDefaultACL(roles, key.Creator, key.Class, key.Schema != (ids.ObjectAddress{}), record.ACL); err != nil {
			return err
		}
	}
	return nil
}
