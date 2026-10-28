package schemacmds_test

import (
 "strings"
 "testing"
 "stuntgres/internal/engine/extension"
)

func ltxtqueryRenamedOwner(t *testing.T) {
 t.Helper()
 e,op,ext,_:=lqueryProviderInstalled(t,false)
 for _,q:=range []string{"CREATE SCHEMA lx_owner", "ALTER TYPE public.ltxtquery RENAME TO label_query", "ALTER TYPE public.label_query SET SCHEMA lx_owner"} {
  domainUnitPublish(t,e,domainUnitStage(t,e,op,q))
 }
 view:=e.CatalogRead()
 roles,err:=extension.InstalledTypes(view,ext)
 if err!=nil || len(roles)!=3 || roles[2].Name!="ltxtquery" || roles[2].Type.Name()!="label_query" {t.Fatal("renamed canonical role",roles,err)}
 metadata,err:=extension.OwnerMetadata(view,e.RoleStore.PublishedRead(&e.Catalog.Owner,view),ext);if err!=nil {t.Fatal(err)}
 record,found,err:=extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata,"-- ")))
 if err!=nil || !found || len(record.Members)!=extension.LtreeMemberCount {t.Fatal("renamed owner recipe",err)}
 if extension.LtreeHierarchyMemberEnd!=64 || extension.LtreeComparisonProcedureStart!=41 || extension.LtreeHierarchyProcedureStart!=60 || extension.LtreePathProcedureStart!=extension.LtreeHierarchyMemberEnd || extension.LtreePathMemberEnd!=extension.LtreePathOperatorStart+3 || extension.LtreeLqueryProcedureStart!=extension.LtreePathMemberEnd || extension.LtreeLqueryOperatorStart!=extension.LtreeLqueryProcedureStart+2 || extension.LtreeLqueryMemberEnd!=90 || extension.LtreeLtxtqueryProcedureStart!=extension.LtreeLqueryMemberEnd || extension.LtreeLtxtqueryOperatorStart!=92 || extension.LtreeMemberCount!=94 {
  t.Fatal("ltree provider member bounds",extension.LtreeMemberCount,extension.LtreeHierarchyMemberEnd,extension.LtreeComparisonProcedureStart)
 }
 if record.Members[8].Signature!="ltxtquery" {t.Fatal("output signature lost canonical role",record.Members[8])}
 for _,role:=range []struct{slot int;kind,name,signature string}{
  {extension.LtreeBtreeFamilySlot,"operator_family","ltree_ops","btree"},
  {extension.LtreeBtreeClassSlot,"operator_class","ltree_ops","btree"},
  {extension.LtreeHashFamilySlot,"operator_family","hash_ltree_ops","hash"},
  {extension.LtreeHashClassSlot,"operator_class","hash_ltree_ops","hash"},
 }{
  member:=record.Members[role.slot]
  if member.Slot!=role.slot||member.Kind!=role.kind||member.Name!=role.name||member.Signature!=role.signature||member.Owner!="postgres"{t.Fatal("renaming scalar changed comparison class/family role",member)}
 }
}
