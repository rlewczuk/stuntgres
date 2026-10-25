package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestAccessExecutionACLRawPersistence(t *testing.T) {
	e:=New();s:=sessionStart(t,e,nil)
	for _,q:=range []string{"CREATE ROLE ae_owner","CREATE ROLE ae_grantor","CREATE ROLE ae_reader","ALTER SCHEMA public OWNER TO ae_owner","CREATE FUNCTION ae_raw() RETURNS integer RETURN 1","CREATE FUNCTION ae_absent() RETURNS integer RETURN 2","CREATE FUNCTION ae_empty() RETURNS integer RETURN 3","CREATE FUNCTION ae_equivalent() RETURNS integer RETURN 4","ALTER FUNCTION ae_raw() OWNER TO ae_owner","REVOKE EXECUTE ON FUNCTION ae_raw() FROM PUBLIC","GRANT EXECUTE ON FUNCTION ae_raw() TO ae_grantor WITH GRANT OPTION","SET ROLE ae_grantor","GRANT EXECUTE ON FUNCTION ae_raw() TO ae_reader","RESET ROLE","REVOKE EXECUTE ON FUNCTION ae_empty() FROM PUBLIC,postgres","GRANT EXECUTE ON FUNCTION ae_equivalent() TO PUBLIC"} {sessionSQL(t,s,q)}
	var out bytes.Buffer;if err:=e.Dump(context.Background(),&out);err!=nil {t.Fatal(err)}
	if !strings.Contains(out.String(),"ae_reader=X/ae_grantor") {t.Fatal("named delegated grantor not rendered")}
	next:=New();destination:=sessionStart(t,next,nil)
	for _,q:=range []string{"CREATE ROLE ae_reader","CREATE ROLE ae_grantor","CREATE ROLE ae_owner"} {sessionSQL(t,destination,q)}
	if err:=next.Restore(context.Background(),strings.NewReader(out.String()));err!=nil {t.Fatal(err)}
	read:=next.CatalogRead();roles:=next.roles.PublishedRead(&next.catalog.Owner,read)
	get:=func(name string)*catalog.Procedure {
		a:=read.NameAddress(ids.NameKey{ClassID:ids.ProcedureClassID,Namespace:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PublicNamespaceOID},Name:name})
		p,ok:=read.Procedure(a);if !ok {t.Fatal("missing routine",name)};return p
	}
	if get("ae_absent").ACL.Explicit {t.Fatal("NULL ACL changed")}
	if a:=get("ae_empty").ACL;!a.Explicit || len(a.Entries)!=0 {t.Fatal("explicit empty changed",a)}
	if a:=get("ae_equivalent").ACL;!a.Explicit || len(a.Entries)!=2 {t.Fatal("explicit default-equivalent changed",a)}
	p:=get("ae_raw");if p.Owner!=roles.LookupRef("ae_owner") {t.Fatal("owner not remapped")}
	found:=false
	for _,entry:=range p.ACL.Entries {
		if entry.Grantee.Role==roles.LookupRef("ae_reader") {found=true;if entry.Grantor!=roles.LookupRef("ae_grantor") || entry.Privileges!=authorization.ExecutePrivilege || entry.GrantOptions!=0 {t.Fatal("delegated entry changed",entry)}}
	}
	if !found {t.Fatal("delegated reader missing")}
	if err:=access.ValidateACL(roles,p);err!=nil {t.Fatal(err)}
	invalid:=*p;invalid.ACL=authorization.ACLValue{Explicit:true,Entries:[]authorization.ACLEntry{{Grantor:p.Owner,Grantee:authorization.ACLGrantee{Public:true},Privileges:authorization.UsagePrivilege}}}
	if err:=access.ValidateACL(roles,&invalid);err==nil {t.Fatal("wrong class mask admitted")}
}
