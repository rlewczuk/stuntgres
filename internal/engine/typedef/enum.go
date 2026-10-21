package typedef

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func StageCreateEnumLocked(h StageHost, auth ddl.Authority, s *parser.CreateEnum) error {
	ns, err := h.ExistingCreationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	trustedDestination, err := h.TrustedRestoreDestination(auth, "enum destination")
	if err != nil {
		return err
	}
	view := h.AccessView(auth)
	if err = view.CheckCreate(auth, ns); err != nil {
		denied, ok := err.(*sqlstate.Error)
		if !trustedDestination || !ok || denied.Code != "42501" {
			return err
		}
	}
	if err = access.CheckPersistentCreationNamespace(view.Catalog, ns.Address); err != nil {
		return err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	available, err := h.DisplaceAssociatedArray(ns.Address, name)
	if err != nil {
		return err
	}
	if !available || RelationTypeNameOccupied(view.Catalog, ns.Address, name) {
		return sqlError("42710", fmt.Sprintf(`type "%s" already exists`, name))
	}
	oid, err := h.AllocateOID()
	if err != nil {
		return err
	}
	members := make([]sqltype.EnumMember, len(s.Labels))
	seen := make(map[string]bool, len(members))
	for i, label := range s.Labels {
		if len(label.Text) > 63 {
			diagnostic := sqlError("42602", fmt.Sprintf(`invalid enum label "%s"`, label.Text)).(*sqlstate.Error)
			diagnostic.Detail = "Labels must be 63 bytes or less."
			return diagnostic
		}
		if seen[label.Text] {
			diagnostic := sqlError("23505", `duplicate key value violates unique constraint "pg_enum_typid_label_index"`).(*sqlstate.Error)
			diagnostic.Detail = fmt.Sprintf("Key (enumtypid, enumlabel)=(%d, %s) already exists.", oid, label.Text)
			diagnostic.Schema = "pg_catalog"
			diagnostic.Table = "pg_enum"
			diagnostic.Constraint = "pg_enum_typid_label_index"
			return diagnostic
		}
		seen[label.Text] = true
		member, err := h.AllocateEnumMember()
		if err != nil {
			return err
		}
		members[i] = sqltype.EnumMember{ID: member, Label: label.Text, SortOrder: float32(i + 1)}
	}
	revision, err := h.Revision()
	if err != nil {
		return err
	}
	namespace := view.Catalog.Get(ns.Address).Name()
	typ, err := sqltype.EnumWithMembers(sqltype.ID(oid), name, catalog.DependencyIdentifier(namespace)+"."+catalog.DependencyIdentifier(name), members)
	if err != nil {
		return err
	}
	acl, synthetic := h.CreationTypeACL(view.State.CurrentUser, ns.Address)
	arrayOID, err := h.AllocateOID()
	if err != nil {
		return err
	}
	array, err := sqltype.Array(sqltype.ID(arrayOID), h.AssociatedArrayName(ns.Address, name), typ)
	if err != nil {
		return err
	}
	if err := h.Stage().Create(&catalog.Type{SQLType: typ, ObjectNamespace: ns.Address, ObjectRevision: revision, Owner: view.State.CurrentUser, ACL: acl, ACLSyntheticDefault: synthetic, Array: array.ID()}); err != nil {
		return err
	}
	return h.Stage().Create(&catalog.Type{SQLType: array, ObjectNamespace: ns.Address, ObjectRevision: revision, Owner: view.State.CurrentUser, ACL: authorization.DefaultBaseline(view.State.CurrentUser, authorization.DefaultTypes, false), ACLSyntheticDefault: true})
}
