package engine

import (
	"fmt"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) stageCreateEnumLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateEnum) error {
	ns, err := e.creationNamespace(auth, s.Name)
	if err != nil {
		return err
	}
	if err = auth.CheckCreate(ns); err != nil {
		return err
	}
	if err = checkPersistentCreationNamespace(auth.catalog, ns.Address); err != nil {
		return err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	available, err := e.displaceAssociatedArrayLocked(stage, ns.Address, name)
	if err != nil {
		return err
	}
	if !available || relationTypeNameOccupied(auth.catalog, ns.Address, name) {
		return sqlError("42710", fmt.Sprintf(`type "%s" already exists`, name))
	}
	oid, err := e.allocateOIDLocked()
	if err != nil {
		return err
	}
	labels := make([]string, len(s.Labels))
	seen := make(map[string]bool, len(labels))
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
		labels[i] = label.Text
	}
	revision, err := e.revisionLocked()
	if err != nil {
		return err
	}
	namespace := auth.catalog.state.descriptors[ns.Address].Name()
	typ, err := sqltype.Enum(sqltype.ID(oid), name, dependencyIdentifier(namespace)+"."+dependencyIdentifier(name), labels)
	if err != nil {
		return err
	}
	acl := ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: auth.state.CurrentUser, Grantee: ACLGrantee{Public: true}, Privileges: UsagePrivilege}, {Grantor: auth.state.CurrentUser, Grantee: ACLGrantee{Role: auth.state.CurrentUser}, Privileges: UsagePrivilege, GrantOptions: UsagePrivilege}}}
	arrayOID, err := e.allocateOIDLocked()
	if err != nil {
		return err
	}
	array, err := sqltype.Array(sqltype.ID(arrayOID), associatedArrayName(stage, ns.Address, name), typ)
	if err != nil {
		return err
	}
	if err := stage.Create(&typeDescriptor{typ: typ, namespace: ns.Address, revision: revision, owner: auth.state.CurrentUser, acl: acl, array: array.ID()}); err != nil {
		return err
	}
	return stage.Create(&typeDescriptor{typ: array, namespace: ns.Address, revision: revision, owner: auth.state.CurrentUser, acl: acl})
}
