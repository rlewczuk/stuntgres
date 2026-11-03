package core

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type xmlMappingScope struct { namespace *catalog.Namespace; relations []*catalog.Relation }

// Mu protects capture only, never ACL traversal, ordering, locks or SQL.
func (h *statementHost) xmlMappingScopeAuthority(c *exec.Context)(AuthorizationView,error){
	s:=h.scope();e:=s.Engine();e.Mu.Lock();defer e.Mu.Unlock()
	auth,err:=s.namespaceAuthorizationLocked(e);if err!=nil{return auth,err}
	auth.State.CurrentUser=expressionPrincipal(c)
	return auth,nil
}

func xmlMappingExplicitNamespace(auth AuthorizationView,name string)(*catalog.Namespace,error){
	address:=auth.Catalog.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:name})
	if name=="pg_temp"{address=auth.TemporaryNamespace()}
	ns,ok:=auth.Catalog.Namespace(address);if !ok{return nil,SQLError("3F000",fmt.Sprintf(`schema "%s" does not exist`,name))}
	if ns.Temporary!=(ids.SessionToken{})&&!ns.TemporaryToast&&!access.OwnTemporaryNamespace(auth.TemporaryNamespace(),ns){
		actor,exists:=auth.Roles.Find(auth.State.CurrentUser);if !exists||!actor.Superuser{return nil,SQLError("42501","permission denied for schema "+ns.Name())}
	}
	if err:=auth.CanUse(auth.View.Ref(address));err!=nil{return nil,err}
	return ns,nil
}

func xmlMappingScopeRelation(rel *catalog.Relation)bool{
	switch rel.RelationKind{case catalog.TableRelation,catalog.MaterializedViewRelation,catalog.ViewRelation,catalog.CatalogRelation:return true};return false
}

func xmlMappingScopes(ctx context.Context,auth AuthorizationView,schema *string)([]xmlMappingScope,error){
	if err:=ctx.Err();err!=nil{return nil,err}
	var scopes []xmlMappingScope
	if schema!=nil{
		ns,err:=xmlMappingExplicitNamespace(auth,*schema);if err!=nil{return nil,err};scopes=append(scopes,xmlMappingScope{namespace:ns})
	}else{
		for address,d:=range auth.Catalog.Descriptors(){
			if err:=ctx.Err();err!=nil{return nil,err}
			ns,ok:=d.(*catalog.Namespace);if !ok||strings.HasPrefix(ns.Name(),"pg_")||ns.Name()=="information_schema"{continue}
			allowed,_,err:=auth.View.ObjectPrivilege(ctx,auth,auth.State.CurrentUser.ID(),address,authorization.UsagePrivilege,0);if err!=nil{return nil,err};if allowed{scopes=append(scopes,xmlMappingScope{namespace:ns})}
		}
	}
	byNamespace:=make(map[ids.ObjectAddress]int,len(scopes));for i,scope:=range scopes{if err:=ctx.Err();err!=nil{return nil,err};byNamespace[scope.namespace.Address()]=i}
	for _,rel:=range auth.Catalog.Relations(){
		if err:=ctx.Err();err!=nil{return nil,err}
		i,present:=byNamespace[rel.Namespace()];if !present||!xmlMappingScopeRelation(rel){continue}
		allowed,err:=auth.View.RelationPrivilege(ctx,auth.State.CurrentUser.ID(),rel,0,false,authorization.SelectPrivilege,0);if err!=nil{return nil,err};if allowed{scopes[i].relations=append(scopes[i].relations,rel)}
	}
	// NAME ordering uses the existing cancellable bytewise provider. Catalog
	// identities bound names; no OID or iteration order affects scope layout.
	collation,err:=sqltype.RegisteredCollation(950,950);if err!=nil{return nil,err}
	compare:=func(a,b string)int{if err!=nil{return 0};var sign int;sign,err=collation.CompareText(ctx,a,b);return sign}
	slices.SortFunc(scopes,func(a,b xmlMappingScope)int{return compare(a.namespace.Name(),b.namespace.Name())})
	for i:=range scopes{if err!=nil{return nil,err};slices.SortFunc(scopes[i].relations,func(a,b *catalog.Relation)int{return compare(a.Name(),b.Name())})}
	if err!=nil{return nil,err};return scopes,ctx.Err()
}
