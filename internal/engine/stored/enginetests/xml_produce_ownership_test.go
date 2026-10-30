package enginetests_test

import (
 "context"
 "fmt"
 "maps"
 "reflect"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/stored"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

func TestXMLProduceFrozenOwnership(t *testing.T){
 e:=engine.New();s:=enginetest.StartSession(t,e,nil)
 enginetest.SessionSQL(t,s,`CREATE TABLE xml_produce_owner(t text,x xml)`)
 enginetest.SessionSQL(t,s,`CREATE VIEW xml_produce_owned AS SELECT xmlelement(NAME "r$",XMLATTRIBUTES(t AS "a$b"),x,t) AS el,xmlforest(t AS f,x AS x) AS fo,xmlconcat(x,'<end/>') AS co,xmlpi(NAME p,t) AS pi,xmlroot(x,VERSION '1.1',STANDALONE YES) AS ro,xmltext(t) AS tx,xmlcomment(t) AS cm FROM xml_produce_owner`)
 read:=e.CatalogRead();relation:=preparedFoldStoredRelation(t,e,"xml_produce_owned");accepted:=stored.ViewOf(relation);parts:=accepted.Parts();syntax:=parts.Syntax;root:=accepted.Query().Root.(*plan.ProjectNode);backing:=&root.Expressions[0]
 original:=append([]expr.Expr(nil),root.Expressions...);raw:=syntax.Targets[0].(parser.XMLConstructExpr);named:=append([]parser.XMLNamedOperand(nil),raw.Named...)
 if err:=accepted.Validate(read,relation);err!=nil{t.Fatal(err)}
 for i,node:=range original{
  if i<5{recipe,ok:=node.(functions.XMLConstruct);if !ok||!recipe.MatchesSyntax(syntax.Targets[i].(parser.XMLConstructExpr)){t.Fatal("actual accepted constructor",node)}}else{call,ok:=node.(functions.XMLTextCall);if !ok||call.CallRoutineReference()==nil{t.Fatal("actual ordinary routine",node)};want:=uint32(3813);if i==6{want=2895};if call.CallRoutineReference().RoutineOID()!=want{t.Fatal(call.CallRoutineReference())}}
  mapped,changed,err:=stored.RetagTypes(read,node,func(d *sqltype.Descriptor)*sqltype.Descriptor{return d});if err!=nil||changed||!reflect.DeepEqual(mapped,node){t.Fatal("identity retag",err)}
  remapped,err:=stored.RemapExpression(node,func(ref ids.ObjectRef)ids.ObjectRef{return ref},nil);if err!=nil||!reflect.DeepEqual(remapped,node){t.Fatal("identity remap",err)}
 }
 replace:=func(target int,mutate func(*parser.XMLConstructExpr))*stored.View{return accepted.With(func(next *stored.ViewParts){copySyntax:=*syntax;copySyntax.Targets=append([]parser.Expr(nil),syntax.Targets...);copyRaw:=copySyntax.Targets[target].(parser.XMLConstructExpr);copyRaw.Args=append([]parser.Expr(nil),copyRaw.Args...);copyRaw.Named=append([]parser.XMLNamedOperand(nil),copyRaw.Named...);for i:=range copyRaw.Named{if copyRaw.Named[i].Name!=nil{copyRaw.Named[i].Name=new(*copyRaw.Named[i].Name)}};mutate(&copyRaw);copySyntax.Targets[target]=copyRaw;project:=*root;project.Syntax=&copySyntax;query:=*next.Query;query.Root=&project;next.Query,next.Syntax=&query,&copySyntax;next.Frames=maps.Clone(next.Frames);next.Frames[project.Frame]=&project;next.Queries=maps.Clone(next.Queries);delete(next.Queries,syntax);next.Queries[&copySyntax]=&project})}
 for _,tc:=range []struct{target int;mutate func(*parser.XMLConstructExpr)}{
  {0,func(r *parser.XMLConstructExpr){r.Name.Text="forged"}},
  {0,func(r *parser.XMLConstructExpr){r.Named[0].Name.Text="forged"}},
  {0,func(r *parser.XMLConstructExpr){r.Args=r.Args[:1]}},
  {1,func(r *parser.XMLConstructExpr){r.Named[0],r.Named[1]=r.Named[1],r.Named[0]}},
  {2,func(r *parser.XMLConstructExpr){r.Kind=parser.XMLForest}},
  {3,func(r *parser.XMLConstructExpr){r.Args=nil}},
  {4,func(r *parser.XMLConstructExpr){r.Standalone=1}},
  {4,func(r *parser.XMLConstructExpr){r.Version=nil}},
 }{rejected:=replace(tc.target,tc.mutate);if err:=rejected.ValidateLiterals(read);err==nil{t.Fatal("forged constructor occurrence admitted",tc.target)}}
 if accepted.Query().Root!=root||&root.Expressions[0]!=backing||accepted.Parts().Syntax!=syntax||!reflect.DeepEqual(root.Expressions,original)||!reflect.DeepEqual(raw.Named,named){t.Fatal("rejected publication changed retained owner/backing")}
 for _,deparse:=range []bool{false,true}{if _,err:=stored.ViewDefinitionSQL(context.Background(),read,relation,stored.Presentation{Deparse:deparse});err!=nil{t.Fatal(err)}}
}

func TestXMLProduceSelectedCastOwnership(t *testing.T){
 e:=engine.New();s:=enginetest.StartSession(t,e,nil)
 for _,name:=range []string{"xml_produce_cast_actual","xml_produce_cast_other"}{enginetest.SessionSQL(t,s,`CREATE FUNCTION `+name+`(integer) RETURNS xml LANGUAGE sql IMMUTABLE AS 'SELECT XMLPARSE(CONTENT ''<same/>'')'`)}
 enginetest.SessionSQL(t,s,`CREATE CAST (integer AS xml) WITH FUNCTION xml_produce_cast_actual(integer) AS ASSIGNMENT`)
 enginetest.SessionSQL(t,s,`CREATE TABLE xml_produce_cast_data(n integer)`)
 enginetest.SessionSQL(t,s,`CREATE VIEW xml_produce_cast_owned AS SELECT xmlconcat(n,'<end/>') AS v FROM xml_produce_cast_data`)
 read:=e.CatalogRead();relation:=preparedFoldStoredRelation(t,e,"xml_produce_cast_owned");accepted:=stored.ViewOf(relation);root:=accepted.Query().Root.(*plan.ProjectNode);recipe:=root.Expressions[0].(functions.XMLConstruct)
 var actual plan.SQLRoutineExpr;plan.WalkExpression(recipe,func(node expr.Expr){if call,ok:=node.(plan.SQLRoutineExpr);ok{actual=call}})
 edge,ok:=read.Cast(sqltype.Int4,sqltype.XML);if !ok||actual.Procedure==nil||actual.Procedure!=read.MustProcedure(edge.Function)||actual.Ref.Address!=edge.Function||actual.Procedure.Strict{t.Fatal("selected nonSTRICT assignment authority",actual)}
 var other *catalog.Procedure;for _,descriptor:=range read.Descriptors(){if candidate,ok:=descriptor.(*catalog.Procedure);ok&&candidate.Name()=="xml_produce_cast_other"{other=candidate}}
 if other==nil{t.Fatal("missing alternate routine")}
 forged:=actual;forged.Procedure=other
 if forged.ValidateStoredProvider()==nil{t.Fatal("same-valued foreign cast acquired actual procedure authority")}
 if accepted.Query().Root!=root||root.Expressions[0].(functions.XMLConstruct).Children()[0].(plan.SQLRoutineExpr).Procedure!=actual.Procedure{t.Fatal("foreign cast changed accepted recipe")}
 if err:=accepted.Validate(read,relation);err!=nil{t.Fatal(err)}
}

func TestXMLProduceErasedChildCastOwnership(t *testing.T){
 e:=engine.New();s:=enginetest.StartSession(t,e,nil)
 enginetest.SessionSQL(t,s,`CREATE TABLE xml_produce_erased_data(t text,x xml)`)
 enginetest.SessionSQL(t,s,`CREATE VIEW xml_produce_erased_owned AS SELECT /*é*/ xmlelement(NAME r,XMLATTRIBUTES(xmltext(t) AS first,CAST(CAST(t AS text) AS text) AS a),CAST(x AS xml),CAST(t AS text)) AS el,xmlforest(xmltext(t) AS a,CAST(t AS text) AS b) AS fo,xmlroot(xmlconcat(x,xmltext(t)),VERSION CAST(CAST('1.1' AS text) AS text),STANDALONE YES) AS ro,xmlpi(NAME p,CAST(t AS text)) AS pi,xmlconcat(CAST(x AS xml),xmltext(t)) AS co FROM xml_produce_erased_data`)
 read:=e.CatalogRead();relation:=preparedFoldStoredRelation(t,e,"xml_produce_erased_owned");accepted:=stored.ViewOf(relation);parts:=accepted.Parts();root:=accepted.Query().Root.(*plan.ProjectNode);syntax:=parts.Syntax
 if err:=accepted.Validate(read,relation);err!=nil{t.Fatal("later-child exact ordinals rejected",err)}
 usages:=maps.Clone(parts.CastUsages);backing:=&root.Expressions[0];if len(usages)<5{t.Fatal("missing actual erased CAST occurrence authorities",usages)}
 for path:=range usages{for _,remove:=range []bool{false,true}{
  rejected:=accepted.With(func(next *stored.ViewParts){next.CastUsages=maps.Clone(next.CastUsages);if remove{delete(next.CastUsages,path)}else{next.CastUsages[path]=enginetest.Usage(e,sqltype.Bool)}})
  if err:=rejected.ValidateLiterals(read);err==nil{t.Fatal("erased child CAST lost exact captured authority",path,remove)}
 }}
 if accepted.Query().Root!=root||&root.Expressions[0]!=backing||accepted.Parts().Syntax!=syntax||!reflect.DeepEqual(accepted.Parts().CastUsages,usages){t.Fatal("rejected child CAST publication changed accepted backing")}
 for _,deparse:=range []bool{false,true}{if _,err:=stored.ViewDefinitionSQL(context.Background(),read,relation,stored.Presentation{Deparse:deparse});err!=nil{t.Fatal("erased child CAST deparse",deparse,err)}}
}

func TestXMLProduceNameRenderOwnership(t *testing.T){
 e:=engine.New();s:=enginetest.StartSession(t,e,nil)
 for i,tc:=range []struct{label,accepted,presented string}{
  {"t","t","t"},{"_x0041_","_x005F_x0041_","_x0041_"},
  {":lead","_x003A_lead",":lead"},{"xmlThing","_x0078_mlThing","xmlThing"},
  {"a:b","a_x003A_b","a:b"},{"😀","_x1F600_","_x1F600_"},
  {"a\u200cb","a_x200C_b","a\u200cb"},{"xml","_x0078_ml","xml"},{"XML","_x0058_ML","XML"},
 }{
  table,view:=fmt.Sprintf("xml_name_owner_%d",i),fmt.Sprintf("xml_name_owned_%d",i)
  column:=catalog.QuoteIdentifier(tc.label)
  enginetest.SessionSQL(t,s,`CREATE TABLE `+table+`(`+column+` text)`)
  enginetest.SessionSQL(t,s,`CREATE VIEW `+view+` AS SELECT xmlforest(`+column+`) AS f,xmlelement(NAME r,XMLATTRIBUTES(`+column+`)) AS e,xmlforest(`+column+` AS `+column+`) AS explicit FROM `+table)
  relation:=preparedFoldStoredRelation(t,e,view);accepted:=stored.ViewOf(relation);parts:=accepted.Parts()
  root:=accepted.Query().Root.(*plan.ProjectNode);backing:=&root.Expressions[0]
  saved:=append([]expr.Expr(nil),root.Expressions...);syntax:=parts.Syntax
  recipes:=make([]functions.XMLConstruct,len(saved));names:=make([][]string,len(saved));children:=make([][]expr.Expr,len(saved))
  for j,node:=range saved{recipes[j]=node.(functions.XMLConstruct);names[j]=recipes[j].Names();children[j]=recipes[j].Children()}
  if names[0][0]!=tc.accepted||names[1][0]!=tc.accepted{t.Fatal("accepted inferred mapping",tc,names)}
  for _,target:=range []int{0,2}{
   rejected:=accepted.With(func(next *stored.ViewParts){
    copySyntax:=*syntax;copySyntax.Targets=append([]parser.Expr(nil),syntax.Targets...)
    raw:=copySyntax.Targets[target].(parser.XMLConstructExpr);raw.Named=append([]parser.XMLNamedOperand(nil),raw.Named...)
    if raw.Named[0].Name==nil{raw.Named[0].Name=&parser.Identifier{Text:tc.label,Pos:raw.Named[0].Pos}}else{raw.Named[0].Name=nil}
    copySyntax.Targets[target]=raw
    project:=*root;project.Syntax=&copySyntax;query:=*next.Query;query.Root=&project
    next.Query,next.Syntax=&query,&copySyntax;next.Frames=maps.Clone(next.Frames);next.Frames[project.Frame]=&project
    next.Queries=maps.Clone(next.Queries);delete(next.Queries,syntax);next.Queries[&copySyntax]=&project
   })
   if err:=rejected.ValidateLiterals(e.CatalogRead());err==nil{t.Fatal("forged explicit/inferred fact admitted",target)}
   if _,err:=rejected.SelectSQL(context.Background(),e.CatalogRead(),stored.Presentation{});err==nil{t.Fatal("raw presentation trusted forged explicit/inferred fact",target)}
  }
  check:=func(){
   t.Helper();read:=e.CatalogRead();current:=preparedFoldStoredRelation(t,e,view)
   for j,recipe:=range recipes{
    rendered,err:=recipe.Render();if err!=nil{t.Fatal(err)}
    label:=tc.presented
    // Explicit xml-prefix and interior-colon labels never acquired the
    // inferred mapping, but have the same released presentation identifier.
    found:=false;for _,piece:=range rendered.Pieces{if piece.Text==" AS "+catalog.QuoteIdentifier(label){found=true}}
    if !found{t.Fatal("typed presentation omitted accepted label",tc,j,rendered.Pieces)}
    if !reflect.DeepEqual(recipe.Names(),names[j])||!reflect.DeepEqual(recipe.Children(),children[j])||!recipe.MatchesSyntax(syntax.Targets[j].(parser.XMLConstructExpr)){t.Fatal("render changed accepted provider")}
   }
   for _,deparse:=range []bool{false,true}{for _,pretty:=range []bool{false,true}{
    sql,err:=stored.ViewDefinitionSQL(context.Background(),read,current,stored.Presentation{Deparse:deparse,Pretty:pretty});if err!=nil{t.Fatal(err)}
    statements,err:=parser.Parse(sql);if err!=nil{t.Fatal(sql,err)}
    count:=0
    if err:=parser.VisitQueryExpressions(statements[0],func(raw parser.Expr)error{
     if constructor,ok:=raw.(parser.XMLConstructExpr);ok{for _,named:=range constructor.Named{if named.Name==nil||named.Name.Text!=tc.presented{t.Fatalf("raw/typed presentation label: %s %#v",sql,named.Name)};count++}}
     return nil
    });err!=nil||count!=3{t.Fatal("presented constructors",count,err,sql)}
   }}
   if accepted.Query().Root!=root||&root.Expressions[0]!=backing||accepted.Parts().Syntax!=syntax||!reflect.DeepEqual(root.Expressions,saved)||!reflect.DeepEqual(accepted.Parts().CastUsages,parts.CastUsages){t.Fatal("presentation replaced accepted graph/backing/generation authority")}
  }
  check();enginetest.SessionSQL(t,s,`ALTER TABLE `+table+` RENAME COLUMN `+column+` TO renamed`);check()
 }
}
