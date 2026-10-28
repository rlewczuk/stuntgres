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
 if extension.LtreeMemberCount!=41 {t.Fatal("ltree provider member count",extension.LtreeMemberCount)}
 if record.Members[8].Signature!="ltxtquery" {t.Fatal("output signature lost canonical role",record.Members[8])}
}
