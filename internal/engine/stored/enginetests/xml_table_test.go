package enginetests_test

import (
 "context"
 "maps"
 "reflect"
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
 "testing"
)

func TestXMLTableStoredTraversal(t *testing.T) {
 e:=engine.New();s:=enginetest.StartSession(t,e,nil)
 enginetest.SessionSQL(t,s,`CREATE DOMAIN xt_owned_int AS int CHECK(VALUE>0)`)
 enginetest.SessionSQL(t,s,`CREATE VIEW xt_owned AS SELECT * FROM XMLTABLE(XMLNAMESPACES('urn:r' AS p), ('/p:r/' || 'p:v') PASSING XMLPARSE(DOCUMENT '<r xmlns="urn:r"><v>7</v></r>') COLUMNS n FOR ORDINALITY,v xt_owned_int PATH '.' DEFAULT COALESCE(41,42))`)
 read:=e.CatalogRead();relation:=preparedFoldStoredRelation(t,e,"xt_owned");accepted:=stored.ViewOf(relation)
 if err:=accepted.Validate(read,relation);err!=nil {t.Fatal(err)}
 root:=accepted.Query().Root.(*plan.ProjectNode);syntax:=accepted.Parts().Syntax
 raw:=syntax.From.(*parser.XMLTable)
 if len(raw.Expressions())!=5 {t.Fatal("lost written families",len(raw.Expressions()))}
 var provider functions.XMLTable;found:=false
 plan.VisitExecutableQuery(root,func(plan.Node){},func(node expr.Expr){plan.WalkExpression(node,func(node expr.Expr){if table,ok:=node.(functions.XMLTable);ok {provider=table;found=true}})})
 if !found || len(provider.WrittenChildren())!=5 || !provider.Fields[1].Usage.Type.IsDomain() || provider.Fields[1].Default<0 {t.Fatal("lost accepted type/default authority")}
 args,fields:=&provider.Args[0],&provider.Fields[0]
 rebuilt,err:=provider.Rebuild(expr.InfoOf(provider),provider.Children());if err!=nil {t.Fatal(err)}
 if table:=rebuilt.(functions.XMLTable);table.Raw!=provider.Raw || &table.Args[0]!=args || &table.Fields[0]!=fields {t.Fatal("identity rebuild replaced accepted backing")}
 if _,changed,err:=stored.RetagTypes(read,provider,func(d *sqltype.Descriptor)*sqltype.Descriptor{return d});err!=nil || changed {t.Fatal("identity retag changed accepted description",changed,err)}
 if _,err:=stored.RemapExpression(provider,func(ref ids.ObjectRef)ids.ObjectRef{return ref},nil);err!=nil {t.Fatal(err)}
 replace:=func(forged *parser.XMLTable)*stored.View {
  return accepted.With(func(parts *stored.ViewParts){
   next:=*syntax;next.From=forged
   project:=*root;project.Syntax=&next
   query:=*parts.Query;query.Root=&project
   parts.Query,parts.Syntax=&query,&next
   parts.Frames=maps.Clone(parts.Frames);parts.Frames[project.Frame]=&project
   parts.Queries=maps.Clone(parts.Queries);delete(parts.Queries,syntax);parts.Queries[&next]=&project
  })
 }
 for i:=range raw.Expressions() {
  children:=raw.Expressions();children[i]=parser.LiteralExpr{Value:parser.Value{Kind:parser.StringValue,Text:"forged",Pos:children[i].SourcePosition()}}
  if err:=replace(raw.WithExpressions(children)).ValidateLiterals(read);err==nil {t.Fatal("forged written family accepted",i)}
 }
 for _,tc:=range []struct{name string;mutate func(*parser.XMLTable)}{
  {"namespace alias",func(raw *parser.XMLTable){raw.Namespaces[0].Name.Text="forged"}},
  {"namespace default",func(raw *parser.XMLTable){raw.Namespaces[0].Default=true}},
  {"column name",func(raw *parser.XMLTable){raw.Columns[1].Name.Text="forged"}},
  {"ordinality",func(raw *parser.XMLTable){raw.Columns[1].Ordinality=true}},
  {"option kind",func(raw *parser.XMLTable){raw.Columns[1].Options[0].Kind="default"}},
  {"option omission",func(raw *parser.XMLTable){raw.Columns[1].Options[0].Value=nil}},
  {"lateral occurrence",func(raw *parser.XMLTable){raw.Lateral=!raw.Lateral}},
 } {
  t.Run(tc.name,func(t *testing.T){
   forged:=raw.WithExpressions(raw.Expressions());tc.mutate(forged)
   if err:=replace(forged).ValidateLiterals(read);err==nil {t.Fatal("forged source layout accepted")}
  })
 }
 // Output type authority is the checked Usage, never raw declaration spelling.
 spelling:=raw.WithExpressions(raw.Expressions())
 spelling.Columns[1].Type=parser.TypeName{Name:"not_a_type_authority"}
 typePresentation:=replace(spelling)
 if err:=typePresentation.ValidateLiterals(read);err!=nil {t.Fatal("raw type spelling replaced accepted type authority",err)}
 for _,deparse:=range []bool{false,true} {
  text,err:=accepted.SelectSQL(context.Background(),read,stored.Presentation{Deparse:deparse,Columns:relation.Columns});if err!=nil {t.Fatal(err)}
  if _,err:=parser.Parse(text);err!=nil {t.Fatal("renderer lost required c_expr parentheses",text,err)}
  again,err:=typePresentation.SelectSQL(context.Background(),read,stored.Presentation{Deparse:deparse,Columns:relation.Columns})
  if err!=nil || again!=text {t.Fatal("raw declaration redirected accepted type rendering",again,text,err)}
 }
 if accepted.Query().Root!=root || accepted.Parts().Syntax!=syntax || &provider.Args[0]!=args || &provider.Fields[0]!=fields {t.Fatal("rejected stored publication changed accepted state")}
 t.Run("normalized routine and cast families",xmlTableStoredNormalizedFamilies)
 t.Run("DEFAULT namespace rendering",xmlTableStoredDefaultNamespaceRendering)
}

func xmlTableStoredNormalizedFamilies(t *testing.T) {
 e:=engine.New();s:=enginetest.StartSession(t,e,nil)
 for _,sql:=range []string{
  `CREATE DOMAIN xt_normal_int AS integer CHECK(VALUE>0)`,
  `CREATE FUNCTION xt_normal_ns(integer) RETURNS text LANGUAGE SQL VOLATILE AS 'SELECT ''urn:r'''`,
  `CREATE FUNCTION xt_normal_row(integer) RETURNS text LANGUAGE SQL VOLATILE AS 'SELECT ''/p:r/p:v'''`,
  `CREATE FUNCTION xt_normal_path(integer) RETURNS text LANGUAGE SQL VOLATILE AS 'SELECT ''.'''`,
  `CREATE FUNCTION xt_normal_default(integer) RETURNS integer LANGUAGE SQL VOLATILE AS 'SELECT 42'`,
  `CREATE FUNCTION xt_normal_doc(integer) RETURNS xml LANGUAGE SQL VOLATILE AS 'SELECT XMLPARSE(DOCUMENT ''<r xmlns="urn:r"><v>7</v></r>'')'`,
  `CREATE FUNCTION xt_normal_alt_doc(integer) RETURNS xml LANGUAGE SQL VOLATILE AS 'SELECT XMLPARSE(DOCUMENT ''<r xmlns="urn:r"><v>7</v></r>'')'`,
  `CREATE FUNCTION xt_normal_date(integer) RETURNS date LANGUAGE SQL VOLATILE AS 'SELECT DATE ''2024-01-02'''`,
  `CREATE FUNCTION xt_normal_alt_date(integer) RETURNS date LANGUAGE SQL VOLATILE AS 'SELECT DATE ''2024-01-02'''`,
  `CREATE CAST (integer AS xml) WITH FUNCTION xt_normal_doc(integer) AS IMPLICIT`,
  `CREATE CAST (integer AS date) WITH FUNCTION xt_normal_date(integer) AS ASSIGNMENT`,
  `CREATE VIEW xt_normal_users AS SELECT * FROM XMLTABLE(XMLNAMESPACES(xt_normal_ns(1) AS p), xt_normal_row(1) PASSING 1 COLUMNS v xt_normal_int PATH xt_normal_path(1) DEFAULT xt_normal_default(1))`,
  `CREATE VIEW xt_normal_providers AS SELECT * FROM XMLTABLE(XMLNAMESPACES(lower('URN:R') AS p), concat('/p:r/','p:v') PASSING XMLPARSE(DOCUMENT '<r xmlns="urn:r"><v>7</v></r>') COLUMNS v int PATH lower('.') DEFAULT length('owned'))`,
  `CREATE VIEW xt_normal_default_cast AS SELECT * FROM XMLTABLE(XMLNAMESPACES(xt_normal_ns(1) AS p), xt_normal_row(1) PASSING 1 COLUMNS v date PATH 'missing' DEFAULT 1)`,
 } {enginetest.SessionSQL(t,s,sql)}
 for _,name:=range []string{"xt_normal_users","xt_normal_providers","xt_normal_default_cast"} {
  read:=e.CatalogRead();relation:=preparedFoldStoredRelation(t,e,name);accepted:=stored.ViewOf(relation)
  if err:=accepted.Validate(read,relation);err!=nil {t.Fatal("normalized written families lost accepted authority",name,err)}
  root:=accepted.Query().Root.(*plan.ProjectNode)
  var provider functions.XMLTable
  plan.VisitExecutableQuery(root,func(plan.Node){},func(node expr.Expr){
   if table,ok:=node.(functions.XMLTable);ok {provider=table}
  })
  if provider.Raw==nil || len(provider.Raw.Expressions())!=5 {t.Fatal("normalized source not retained",name)}
  if name=="xt_normal_users" {
   written:=provider.Raw.Expressions()
   for _,i:=range []int{0,1,3,4} {
    call,ok:=written[i].(parser.FunctionExpr)
    if !ok || !call.StoredSQLRoutine {t.Fatal("user routine source not normalized",i,written[i])}
   }
   edge,ok:=read.Cast(sqltype.Int4,sqltype.XML);if !ok {t.Fatal("missing actual source cast")}
   calls:=0
   plan.WalkExpression(provider.Args[2],func(node expr.Expr){
    if call,ok:=node.(plan.SQLRoutineExpr);ok && call.Ref.Address==edge.Function {calls++}
   })
   if calls!=1 {t.Fatal("document normalization lost actual selected cast",calls)}
  }
  if name=="xt_normal_providers" {
   call,ok:=provider.Raw.Expressions()[0].(parser.FunctionExpr)
   if !ok || !call.StoredProviderRoutine || call.StoredProviderSymbol!="lower" {t.Fatal("accepted source witness predates provider capture markers")}
  }
  if name!="xt_normal_providers" {
   target,slot,alternateName:=sqltype.XML,2,"xt_normal_alt_doc"
   if name=="xt_normal_default_cast" {target,slot,alternateName=sqltype.Date,provider.Fields[0].Default,"xt_normal_alt_date"}
   edge,ok:=read.Cast(sqltype.Int4,target);if !ok {t.Fatal("missing selected assignment cast")}
   selected:=read.MustProcedure(edge.Function)
   var alternate *catalog.Procedure
   for _,descriptor:=range read.Descriptors() {
    if procedure,ok:=descriptor.(*catalog.Procedure);ok && procedure.Namespace()==relation.Namespace() && procedure.Name()==alternateName {alternate=procedure}
   }
   call,ok:=provider.Args[slot].(plan.SQLRoutineExpr)
   if !ok || alternate==nil || call.Procedure!=selected || call.Ref.Address!=edge.Function || selected.Signature!=alternate.Signature || selected.Result!=alternate.Result {t.Fatal("lost distinguishing selected cast association",name)}
   for _,mutate:=range []func(*plan.SQLRoutineExpr){
    func(call *plan.SQLRoutineExpr){call.Procedure=alternate},
    func(call *plan.SQLRoutineExpr){call.RoutineReference=alternate},
    func(call *plan.SQLRoutineExpr){call.Ref=read.Owner().Domain.Ref(read.Epoch(),alternate.Address())},
   } {
    forged:=call;mutate(&forged)
    if err:=forged.ValidateStoredProvider();err==nil {t.Fatal("substituted selected cast provider accepted",name)}
   }
   original:=maps.Clone(accepted.Parts().RoutineRefs)
   forgeries:=0
   for path,ref:=range original {
    if ref.Address!=edge.Function {continue};forgeries++
    rejected:=accepted.With(func(parts *stored.ViewParts){
     parts.RoutineRefs=maps.Clone(parts.RoutineRefs)
     parts.RoutineRefs[path]=read.Owner().Domain.Ref(read.Epoch(),alternate.Address())
    })
    if err:=rejected.Validate(read,relation);err==nil {t.Fatal("same-valued substituted cast occurrence accepted",name)}
   }
   if forgeries!=1 || !maps.Equal(original,accepted.Parts().RoutineRefs) {t.Fatal("selected cast occurrence missing or accepted backing changed",name,forgeries)}
   if err:=accepted.Validate(read,relation);err!=nil {t.Fatal("rejected cast publication contaminated accepted view",err)}
  }
  args,fields,raw:=&provider.Args[0],&provider.Fields[0],provider.Raw
  for _,deparse:=range []bool{false,true} {
   text,err:=accepted.SelectSQL(context.Background(),read,stored.Presentation{Deparse:deparse,Columns:relation.Columns})
   if err!=nil {t.Fatal("normalized source failed rendering",name,err)}
   if _,err:=parser.Parse(text);err!=nil {t.Fatal(text,err)}
  }
  if provider.Raw!=raw || &provider.Args[0]!=args || &provider.Fields[0]!=fields {t.Fatal("normalization/rendering changed accepted backing")}
 }
 for _,sql:=range []string{
  `DROP CAST (integer AS xml)`,
  `CREATE CAST (integer AS xml) WITH FUNCTION xt_normal_alt_doc(integer) AS ASSIGNMENT`,
  `DROP CAST (integer AS date)`,
  `CREATE CAST (integer AS date) WITH FUNCTION xt_normal_alt_date(integer) AS ASSIGNMENT`,
 } {enginetest.SessionSQL(t,s,sql)}
 for _,name:=range []string{"xt_normal_users","xt_normal_default_cast"} {
  read:=e.CatalogRead();relation:=preparedFoldStoredRelation(t,e,name);retained:=stored.ViewOf(relation)
  if err:=retained.Validate(read,relation);err!=nil {t.Fatal("cast replacement replaced frozen recipe authority",name,err)}
 }
 enginetest.SessionSQL(t,s,`ALTER DOMAIN xt_normal_int RENAME TO xt_normal_renamed_int`)
 read:=e.CatalogRead();relation:=preparedFoldStoredRelation(t,e,"xt_normal_users");renamed:=stored.ViewOf(relation)
 if err:=renamed.Validate(read,relation);err!=nil {t.Fatal("type rename changed written routine/cast authority",err)}
 for _,deparse:=range []bool{false,true} {
  text,err:=renamed.SelectSQL(context.Background(),read,stored.Presentation{Deparse:deparse,Columns:relation.Columns})
  if err!=nil {t.Fatal(err)}
  if _,err:=parser.Parse(text);err!=nil {t.Fatal("rename rendering lost normalized c-expression",text,err)}
 }
}

func xmlTableStoredDefaultNamespaceRendering(t *testing.T) {
 for _,tc:=range []struct{name,document string}{
  {"NULL",`NULL::xml`},{"nonNULL",`'<r/>'::xml`},
 } {t.Run(tc.name,func(t *testing.T){
  e:=engine.New();s:=enginetest.StartSession(t,e,nil)
  enginetest.SessionSQL(t,s,`CREATE VIEW xt_default_owned AS SELECT * FROM XMLTABLE(XMLNAMESPACES('urn:before' AS before_ns, DEFAULT 'urn:r', 'urn:after' AS after_ns), '/r' PASSING (`+tc.document+`) COLUMNS n FOR ORDINALITY,v int PATH '.' DEFAULT 42)`)
  relation:=preparedFoldStoredRelation(t,e,"xt_default_owned");accepted:=stored.ViewOf(relation)
  read:=e.CatalogRead();if err:=accepted.Validate(read,relation);err!=nil {t.Fatal(err)}
  raw:=accepted.Parts().Syntax.From.(*parser.XMLTable)
  snapshot:=raw.WithExpressions(raw.Expressions());backing:=&raw.Namespaces[0]
  parts:=accepted.Parts()
  literals,casts,routines:=maps.Clone(parts.Literals),maps.Clone(parts.CastUsages),maps.Clone(parts.RoutineRefs)
  var provider functions.XMLTable
  plan.VisitExecutableQuery(accepted.Query().Root,func(plan.Node){},func(node expr.Expr){
   plan.WalkExpression(node,func(node expr.Expr){if table,ok:=node.(functions.XMLTable);ok {provider=table}})
  })
  if !reflect.DeepEqual(provider.Raw,raw) || len(provider.Args)!=7 {t.Fatal("lost accepted namespace occurrence frame")}
  args,fields:=&provider.Args[0],&provider.Fields[0]
  providerRaw:=provider.Raw
  for i,deparse:=range []bool{false,true} {
   text,err:=accepted.SelectSQL(context.Background(),read,stored.Presentation{Deparse:deparse,Columns:relation.Columns});if err!=nil {t.Fatal(err)}
   if _,err:=parser.Parse(text);err!=nil {t.Fatal("DEFAULT namespace rendering cannot reparse",text,err)}
   name:=[]string{"xt_default_replay","xt_default_deparse"}[i]
   enginetest.SessionSQL(t,s,`CREATE VIEW `+name+` AS `+text)
   reboundRelation:=preparedFoldStoredRelation(t,e,name);rebound:=stored.ViewOf(reboundRelation)
   if err:=rebound.Validate(e.CatalogRead(),reboundRelation);err!=nil {t.Fatal("rebound DEFAULT namespace lost captures",err)}
   from:=rebound.Parts().Syntax.From
   if alias,ok:=from.(*parser.AliasReference);ok {from=alias.Input}
   rendered,ok:=from.(*parser.XMLTable);if !ok || len(rendered.Namespaces)!=3 || len(rendered.Expressions())!=7 {t.Fatal("rendered source lost operand layout")}
   for j,ns:=range rendered.Namespaces {
    if ns.Default!=(j==1) || ns.Name.Text!=[]string{"before_ns","","after_ns"}[j] {t.Fatal("rendered namespace changed kind/order",j,ns)}
    uri:=ns.URI
    for {cast,ok:=uri.(parser.CastExpr);if !ok {break};uri=cast.Child}
    literal,ok:=uri.(parser.LiteralExpr)
    if !ok || literal.Value.Kind!=parser.StringValue || literal.Value.Text!=[]string{"urn:before","urn:r","urn:after"}[j] {t.Fatal("rendered namespace changed captured URI index",j,uri)}
   }
  }
  after:=accepted.Parts()
  if after.Syntax!=parts.Syntax || after.Query!=parts.Query || !reflect.DeepEqual(raw,snapshot) || &raw.Namespaces[0]!=backing || !reflect.DeepEqual(after.Literals,literals) || !reflect.DeepEqual(after.CastUsages,casts) || !reflect.DeepEqual(after.RoutineRefs,routines) {t.Fatal("rendering changed accepted syntax or capture maps")}
  plan.VisitExecutableQuery(accepted.Query().Root,func(plan.Node){},func(node expr.Expr){
   plan.WalkExpression(node,func(node expr.Expr){if table,ok:=node.(functions.XMLTable);ok {
    if table.Raw!=providerRaw || &table.Args[0]!=args || &table.Fields[0]!=fields {t.Fatal("rendering replaced accepted provider backing")}
   }})
  })
 })}
}
