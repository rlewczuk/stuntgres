package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferenceXMLMapScopeSignatures(t *testing.T){
	fixture.RoleReferenceCleanup(t,[]string{"xs_signature_user"});fixture.SchemaReferenceCleanup(t,"xs_signature")
	xmlScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		xmlMapExec(t,p,c,`CREATE SCHEMA xs_signature`,`CREATE ROLE xs_signature_user`,`GRANT USAGE ON SCHEMA xs_signature TO xs_signature_user`,`SET ROLE xs_signature_user`)
		defer c.ExecContext(p.Ctx,`RESET ROLE`)
		db:=xmlMapDatabase(t,p,c)
		for index,function:=range xmlScopeFunctions{
			database:=index>=3;oid:=int64(2933+index);names,types:="schema,nulls,tableforest,targetns","19 16 16 25";args:=[]string{"'xs_signature'","false","false","''"};casts:=[]string{"name","boolean","boolean","text"};if database{names="nulls,tableforest,targetns";types="16 16 25";args=args[1:];casts=casts[1:]}
			q:=fmt.Sprintf(`SELECT oid::bigint AS oid,prorettype::integer AS result,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,procost::integer AS cost,pronargdefaults::integer AS defaults,array_to_string(proargnames,',') AS names,proargtypes::text AS types FROM pg_proc WHERE oid=%d`,oid)
			xmlBoth(t,p,b,c,fixture.Select(q,[]string{"oid","result","proisstrict","volatility","parallel","cost","defaults","names","types"},[]string{"INT8","INT4","BOOL","TEXT","TEXT","INT4","INT4","TEXT","TEXT"},[][]any{{oid,int64(142),true,"s","r",int64(100),int64(0),names,types}}))
			root,schema,body:="xs_signature","xs_signature","";if database{root=db;schema="";body=xmlScopeNested("xs_signature","")}
			xsd:=xmlScopeXSD(db,schema,"","",false);if database{xsd=xmlScopeXSD(db,"","","",false,"xs_signature")}
			want:=xmlScopeDocument(root,"","",body);if index%3==1{want=xsd};if index%3==2{want=xmlScopeDocument(root,"",xsd,body)}
			labels:=strings.Split(names,",")
			for _,route:=range []string{"positional","named","typed"}{
				written:=append([]string(nil),args...);if route=="typed"{for i:=range written{written[i]+="::"+casts[i]}};if route=="named"{for i:=range written{written[i]=labels[i]+"=>"+written[i]};for i,j:=0,len(written)-1;i<j;i,j=i+1,j-1{written[i],written[j]=written[j],written[i]}}
				xmlScopeBoth(t,p,b,c,"SELECT pg_catalog."+function+"("+strings.Join(written,",")+") AS v",want)
			}
			for i:=range args{
				written:=append([]string(nil),args...);if !database{written[0]="'xs_absent'"};written[i]="NULL::"+casts[i]
				xmlBoth(t,p,b,c,xmlSelect("SELECT "+function+"("+strings.Join(written,",")+") AS v","XML",nil))
				written[i]="$1::"+casts[i];step:=xmlSelect("SELECT "+function+"("+strings.Join(written,",")+") AS v","XML",nil);step.Args=[]any{nil};xmlBoth(t,p,b,c,step)
			}
		}
	})
}
