package engine

import (
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) stageCreateEnumLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateEnum) error {
	ns, err := e.creationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	trustedDestination := op != nil && operationReplayOrZero(op).tableDestination
	if trustedDestination {
		bootstrap, guardErr := auth.Roles.Descriptor(auth.State.Authenticated)
		if guardErr != nil || !bootstrap.Superuser || e.importTargets == nil {
			return fmt.Errorf("stuntgres: invalid trusted restore enum destination authority")
		}
	}
	if err = auth.CheckCreate(ns); err != nil {
		denied, ok := err.(*sqlstate.Error)
		if !trustedDestination || !ok || denied.Code != "42501" {
			return err
		}
	}
	if err = checkPersistentCreationNamespace(auth.Catalog, ns.Address); err != nil {
		return err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	available, err := e.displaceAssociatedArrayLocked(stage, ns.Address, name)
	if err != nil {
		return err
	}
	if !available || typedef.RelationTypeNameOccupied(auth.Catalog, ns.Address, name) {
		return sqlError("42710", fmt.Sprintf(`type "%s" already exists`, name))
	}
	oid, err := e.catalog.allocateOIDLocked()
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
		member, err := e.catalog.allocateEnumMemberLocked()
		if err != nil {
			return err
		}
		members[i] = sqltype.EnumMember{ID: member, Label: label.Text, SortOrder: float32(i + 1)}
	}
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	namespace := auth.Catalog.Get(ns.Address).Name()
	typ, err := sqltype.EnumWithMembers(sqltype.ID(oid), name, catalog.DependencyIdentifier(namespace)+"."+catalog.DependencyIdentifier(name), members)
	if err != nil {
		return err
	}
	acl, synthetic := stage.creationTypeACL(auth.State.CurrentUser, ns.Address)
	arrayOID, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return err
	}
	array, err := sqltype.Array(sqltype.ID(arrayOID), associatedArrayName(stage, ns.Address, name), typ)
	if err != nil {
		return err
	}
	if err := stage.Create(&catalog.Type{SQLType: typ, ObjectNamespace: ns.Address, ObjectRevision: revision, Owner: auth.State.CurrentUser, ACL: acl, ACLSyntheticDefault: synthetic, Array: array.ID()}); err != nil {
		return err
	}
	return stage.Create(&catalog.Type{SQLType: array, ObjectNamespace: ns.Address, ObjectRevision: revision, Owner: auth.State.CurrentUser, ACL: authorization.DefaultBaseline(auth.State.CurrentUser, authorization.DefaultTypes, false), ACLSyntheticDefault: true})
}
