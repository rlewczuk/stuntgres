package engine

import (
	"cmp"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type privilegeClaim struct {
	address  ids.ObjectAddress
	original catalog.Descriptor
}

// Named relation binding is an admission operation. Re-resolution after an AS
// wait precedes ACL planning, so a replacement name can legitimately identify a
// new object. No existing candidate is ever replayed or retargeted.
func (s *StatementScope) admitGrantRelation(name parser.ObjectName, initial catalog.Descriptor) (catalog.Descriptor, error) {
	e := s.engine()
	ref := e.catalog.Domain.Ref(s.txRef.Epoch(), initial.Address())
	for {
		if err := s.acquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
			return nil, err
		}
		e.mu.Lock()
		auth, err := e.authorizationLocked(s.op)
		var next ids.ObjectRef
		if err == nil {
			next, err = e.resolveRelationReference(auth, name, true)
		}
		var descriptor catalog.Descriptor
		if err == nil {
			descriptor = auth.Catalog.Get(next.Address)
		}
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if next == ref {
			return descriptor, nil
		}
		ref = next
	}
}

func (s *StatementScope) admitObjectGrant(req role.GrantRequest, statement *parser.ObjectGrant) error {
	e := s.engine()
	targets := req.Targets()
	if statement.Kind == "" || statement.Kind == "sequence" {
		for i, name := range statement.Relations {
			descriptor, err := s.admitGrantRelation(name, targets[i])
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
			ref := e.catalog.Domain.Ref(s.txRef.Epoch(), descriptor.Address())
			if err := s.acquireCatalogResource(lock.Resource{ObjectRef: ref}, lock.AccessShare); err != nil {
				return err
			}
		}
	}
	var scratch [8]privilegeClaim
	claims := scratch[:0]
	e.mu.Lock()
	current := e.privateCatalogLocked(s.owner.Catalog)
	for i, descriptor := range targets {
		address := descriptor.Address()
		fresh := current.Get(address)
		// Sparse parameter baselines have a reserved real identity but no
		// installed descriptor until their first nonbaseline ACL is staged.
		if fresh == nil && address.ClassID != ids.ParameterClassID {
			e.mu.Unlock()
			return ids.ErrCatalogReplaced
		}
		if fresh != nil {
			targets[i] = fresh
		}
		claims = append(claims, privilegeClaim{address: address, original: e.catalog.state.Get(address)})
	}
	e.mu.Unlock()
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
		resource := lock.Resource{Kind: lock.CatalogACLResource, ObjectRef: e.catalog.Domain.Ref(s.txRef.Epoch(), claim.address)}
		if err := s.acquireCatalogResource(resource, lock.AccessExclusive); err != nil {
			return err
		}
		e.mu.Lock()
		changed := e.catalog.state.Get(claim.address) != claim.original
		e.mu.Unlock()
		if changed {
			if claim.address.ClassID == ids.RelationClassID {
				return sqlError("XX000", "tuple concurrently updated")
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
func (s *StatementScope) admitDefaultPrivileges(claims []defaultPrivilegeClaim) error {
	e := s.engine()
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
		resource := lock.DefaultACLWriter(e.catalog.Domain.Ref(s.txRef.Epoch(), key.Schema), key.Creator.ID(), uint8(key.Class))
		if err := s.acquireCatalogResource(resource, lock.AccessExclusive); err != nil {
			return err
		}
		e.mu.Lock()
		current := e.catalog.state.DefaultACL(key)
		e.mu.Unlock()
		if current == claim.original {
			continue
		}
		if claim.original != nil {
			return sqlError("XX000", "tuple concurrently updated")
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
