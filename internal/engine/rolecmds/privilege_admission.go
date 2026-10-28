package rolecmds

import (
	"cmp"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type privilegeClaim struct {
	address  ids.ObjectAddress
	original catalog.Descriptor
	name ids.NameKey
	binding ids.ObjectAddress
	incarnation uint64
}

// Named relation binding is an admission operation. Re-resolution after an AS
// wait precedes ACL planning, so a replacement name can legitimately identify a
// new object. No existing candidate is ever replayed or retargeted.
func admitGrantRelation(s *core.StatementScope, name parser.ObjectName, initial catalog.Descriptor) (catalog.Descriptor, error) {
	e := s.Engine()
	ref := e.Catalog.Domain.Ref(s.TxRef.Epoch(), initial.Address())
	for {
		if err := s.AcquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
			return nil, err
		}
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(s.Op)
		var next ids.ObjectRef
		if err == nil {
			next, err = e.ResolveRelationReference(auth, name, true)
		}
		var descriptor catalog.Descriptor
		if err == nil {
			descriptor = auth.Catalog.Get(next.Address)
		}
		e.Mu.Unlock()
		if err != nil {
			return nil, err
		}
		if next == ref {
			return descriptor, nil
		}
		ref = next
	}
}

func admitObjectGrant(s *core.StatementScope, req role.GrantRequest, statement *parser.ObjectGrant, parameters []privilegeClaim) error {
	e := s.Engine()
	targets := req.Targets()
	// Canonical name claims also serialize first grants with independently
	// reserved OIDs. Their predecessors were captured with initial resolution.
	slices.SortFunc(parameters, func(a, b privilegeClaim) int { return cmp.Compare(a.name.Name, b.name.Name) })
	for i, claim := range parameters {
		if i != 0 && parameters[i-1].name == claim.name {
			continue
		}
		resource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(s.TxRef.Epoch(), ids.ObjectAddress{}), Name: claim.name}
		if err := s.AcquireCatalogResource(resource, lock.AccessExclusive); err != nil {
			return err
		}
		e.Mu.Lock()
		current := e.Catalog.State
		replaced := current.ParameterIncarnation() != claim.incarnation
		changed := current.NameAddress(claim.name) != claim.binding || current.Get(claim.binding) != claim.original
		e.Mu.Unlock()
		if replaced {
			return ids.ErrCatalogReplaced
		}
		if changed {
			if claim.original != nil {
				return core.SQLError("XX000", "tuple concurrently updated")
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505",
				Message: `duplicate key value violates unique constraint "pg_parameter_acl_parname_index"`,
				Detail: fmt.Sprintf("Key (parname)=(%s) already exists.", claim.name.Name),
				Schema: "pg_catalog", Table: "pg_parameter_acl", Constraint: "pg_parameter_acl_parname_index"}
		}
	}
	if statement.Kind == "" || statement.Kind == "sequence" {
		for i, name := range statement.Relations {
			descriptor, err := admitGrantRelation(s, name, targets[i])
			if err != nil {
				return err
			}
			targets[i] = descriptor
		}
		// ALL IN SCHEMA has an already-expanded, ordered member inventory.
		// Do not add later objects while admitting that inventory.
		for _, descriptor := range targets[len(statement.Relations):] {
			if descriptor.Address().ClassID != ids.RelationClassID {
				continue
			}
			ref := e.Catalog.Domain.Ref(s.TxRef.Epoch(), descriptor.Address())
			if err := s.AcquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
				return err
			}
		}
	}
	var scratch [8]privilegeClaim
	claims := scratch[:0]
	e.Mu.Lock()
	current := e.PrivateCatalogLocked(s.PendingCatalog())
	for i, descriptor := range targets {
		address := descriptor.Address()
		if address.ClassID == ids.ParameterClassID {
			continue
		}
		fresh := current.Get(address)
		if fresh == nil {
			e.Mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		targets[i] = fresh
		claims = append(claims, privilegeClaim{address: address, original: e.Catalog.State.Get(address)})
	}
	e.Mu.Unlock()
	slices.SortFunc(claims, func(a, b privilegeClaim) int {
		if n := cmp.Compare(a.address.ClassID, b.address.ClassID); n != 0 {
			return n
		}
		return cmp.Compare(a.address.ObjectID, b.address.ObjectID)
	})
	for i, claim := range claims {
		if i != 0 && claims[i-1].address == claim.address {
			continue
		}
		resource := lock.Resource{Kind: lock.CatalogACLResource, ObjectRef: e.Catalog.Domain.Ref(s.TxRef.Epoch(), claim.address)}
		if err := s.AcquireCatalogResource(resource, lock.AccessExclusive); err != nil {
			return err
		}
		e.Mu.Lock()
		changed := e.Catalog.State.Get(claim.address) != claim.original
		e.Mu.Unlock()
		if changed {
			if claim.address.ClassID == ids.RelationClassID {
				return core.SQLError("XX000", "tuple concurrently updated")
			}
			return catalog.Conflict{}
		}
	}
	return nil
}

type defaultPrivilegeClaim struct {
	key      catalog.DefaultACLKey
	original *catalog.DefaultACLRecord
}

// Default planning preserves its existing ordered error precedence. Its private
// immutable outputs are retained only if every claimed predecessor and captured
// authority survives the unlocked writer wait; no ACL is recomputed after wait.
func admitDefaultPrivileges(s *core.StatementScope, claims []defaultPrivilegeClaim) error {
	e := s.Engine()
	slices.SortFunc(claims, func(a, b defaultPrivilegeClaim) int {
		if n := cmp.Compare(a.key.Creator.ID(), b.key.Creator.ID()); n != 0 {
			return n
		}
		if n := cmp.Compare(a.key.Schema.ObjectID, b.key.Schema.ObjectID); n != 0 {
			return n
		}
		return cmp.Compare(a.key.Class, b.key.Class)
	})
	for _, claim := range claims {
		key := claim.key
		resource := lock.DefaultACLWriter(e.Catalog.Domain.Ref(s.TxRef.Epoch(), key.Schema), key.Creator.ID(), uint8(key.Class))
		if err := s.AcquireCatalogResource(resource, lock.AccessExclusive); err != nil {
			return err
		}
		e.Mu.Lock()
		current := e.Catalog.State.DefaultACL(key)
		e.Mu.Unlock()
		if current == claim.original {
			continue
		}
		if claim.original != nil {
			return core.SQLError("XX000", "tuple concurrently updated")
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505",
			Message: `duplicate key value violates unique constraint "pg_default_acl_role_nsp_obj_index"`,
			Detail:  fmt.Sprintf("Key (defaclrole, defaclnamespace, defaclobjtype)=(%d, %d, %s) already exists.", key.Creator.ID(), key.Schema.ObjectID, defaultPrivilegeClassTag(key)),
			Schema:  "pg_catalog", Table: "pg_default_acl", Constraint: "pg_default_acl_role_nsp_obj_index"}
	}
	return nil
}

func defaultPrivilegeClassTag(key catalog.DefaultACLKey) string {
	// These are the existing pg_default_acl class tags, not lock identities.
	return string("rSfTnL"[int(key.Class)-1])
}
