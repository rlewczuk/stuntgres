package catalog

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

type DefaultACLKey struct {
	Creator authorization.PrincipalRef
	Schema  ids.ObjectAddress
	Class   authorization.DefaultACLClass
}

type DefaultACLRecord struct{ ACL authorization.ACLValue }

func CloneDefaultACLs(source map[DefaultACLKey]*DefaultACLRecord) map[DefaultACLKey]*DefaultACLRecord {
	next := make(map[DefaultACLKey]*DefaultACLRecord, len(source))
	for key, record := range source {
		next[key] = record
	}
	return next
}

func overlayDefaultACLs(source map[DefaultACLKey]*DefaultACLRecord, changes map[DefaultACLKey]*DefaultACLRecord) map[DefaultACLKey]*DefaultACLRecord {
	if len(changes) == 0 {
		return source
	}
	next := CloneDefaultACLs(source)
	for key, record := range changes {
		if record == nil {
			delete(next, key)
		} else {
			next[key] = record
		}
	}
	return next
}

func CreationDefaultACL(view Read, owner authorization.PrincipalRef, schema ids.ObjectAddress, class authorization.DefaultACLClass) authorization.ACLValue {
	var global, scoped *authorization.ACLValue
	if record := view.state.defaultACLs[DefaultACLKey{Creator: owner, Class: class}]; record != nil {
		global = &record.ACL
	}
	if schema != (ids.ObjectAddress{}) {
		if record := view.state.defaultACLs[DefaultACLKey{Creator: owner, Schema: schema, Class: class}]; record != nil {
			scoped = &record.ACL
		}
	}
	return authorization.CreationDefaultACL(owner, class, global, scoped)
}

func ValidateDefaultACLs(roles authorization.Read, view Read) error {
	for key, record := range view.state.defaultACLs {
		if record == nil {
			return fmt.Errorf("stuntgres: invalid default ACL record")
		}
		if err := authorization.ValidateDefaultACLHeader(roles, key.Creator, key.Class, key.Schema != (ids.ObjectAddress{}), record.ACL); err != nil {
			return err
		}
		if key.Schema != (ids.ObjectAddress{}) {
			if _, ok := view.Namespace(key.Schema); !ok {
				return Conflict{}
			}
		}
		if err := authorization.ValidateDefaultACL(roles, key.Creator, key.Class, key.Schema != (ids.ObjectAddress{}), record.ACL); err != nil {
			return err
		}
	}
	return nil
}
