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

// DefaultACLRecord owns a stable catalog row identity and immutable ACL.
type DefaultACLRecord struct {
	OID ids.OID
	ACL authorization.ACLValue
}

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
	seen := make(map[ids.OID]struct{}, view.State().DefaultACLCount())
	for key, record := range view.state.defaultACLs {
		if record == nil || record.OID == 0 || key.Class < authorization.DefaultTables || key.Class > authorization.DefaultLargeObjects {
			return fmt.Errorf("stuntgres: invalid default ACL record")
		}
		if _, duplicate := seen[record.OID]; duplicate {
			return fmt.Errorf("stuntgres: duplicate default ACL identity")
		}
		seen[record.OID] = struct{}{}
		if err := authorization.ValidateDefaultACLHeader(roles, key.Creator, key.Class, key.Schema != (ids.ObjectAddress{}), record.ACL); err != nil {
			return err
		}
		if key.Schema != (ids.ObjectAddress{}) {
			if namespace, ok := view.Namespace(key.Schema); !ok || namespace == nil || namespace.Address() != key.Schema {
				return Conflict{}
			}
		}
		if err := authorization.ValidateDefaultACL(roles, key.Creator, key.Class, key.Schema != (ids.ObjectAddress{}), record.ACL); err != nil {
			return err
		}
	}
	return nil
}
