package reference_test

import (
 "database/sql"
 "fmt"
 "reflect"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

func citextStringScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
 t.Helper()
 p := fixture.NewScenario(t)
 for _, b := range p.Backends() {
  t.Run(b.Name, func(t *testing.T) {
   c, err := b.DB.Conn(p.Ctx)
   if err != nil { t.Fatal(err) }
   defer c.Close()
   body(t,p,b,c)
  })
 }
}

type citextStringDeclaration struct {
 name string
 args []string
 result string
 values []any
 expected any
}

var citextStringDeclarations = []citextStringDeclaration{
 {"strpos",[]string{"citext","citext"},"INT4",[]any{"AbA","a"},int64(1)},
 {"replace",[]string{"citext","citext","citext"},"TEXT",[]any{"AbA","a","X"},"XbX"},
 {"split_part",[]string{"citext","citext","integer"},"TEXT",[]any{"AbA","a",int64(2)},"b"},
 {"translate",[]string{"citext","citext","text"},"TEXT",[]any{"AbA","a","X"},"XbX"},
}

func (d citextStringDeclaration) signature() string { return "public."+d.name+"("+strings.Join(d.args,",")+")" }
func (d citextStringDeclaration) query() string {
 a:=make([]string,len(d.args))
 for i,typ:=range d.args { a[i]=fmt.Sprintf("$%d::%s",i+1,typ) }
 return "SELECT public."+d.name+"("+strings.Join(a,",")+") AS v"
}
func citextStringValue(q,typ string,v any,args ...any) fixture.Step {
 return fixture.Select(q,[]string{"v"},[]string{typ},[][]any{{v}},args...)
}
func citextStringRoutes(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,s fixture.Step) {
 t.Helper();citextComparisonRoutes(t,p,b,c,s)
}
func citextStringCorpus(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE cs_source(id integer,s citext,d citext,r citext,n integer,t text)",0))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO cs_source VALUES(1,'AbA','a','X',2,'X'),(2,'x','Z','Q',1,'q'),(3,NULL,'a','X',1,'X'),(4,'','','X',1,'X')",4))
}
var citextStringProjectionNames=[]string{"id","r","s","p","t"}
var citextStringProjectionTypes=[]string{"INT4","TEXT","TEXT","INT4","TEXT"}
func citextStringProjectionRows() [][]any {
 return [][]any{{int64(1),"XbX","b",int64(1),"XbX"},{int64(2),"x","x",int64(0),"x"},{int64(3),nil,nil,nil,nil},{int64(4),"X","",int64(1),""}}
}
const citextStringProjection="SELECT id,replace(s,d,r) AS r,split_part(s,d,n) AS s,strpos(s,d) AS p,translate(s,d,t) AS t FROM cs_source"
func citextStringCorpusChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 citextStringRoutes(t,p,b,c,fixture.Select(citextStringProjection+" ORDER BY id",citextStringProjectionNames,citextStringProjectionTypes,citextStringProjectionRows()))
 for _,tc:=range []struct{predicate string;ids [][]any}{
  {"replace(s,d,r)='XbX'",[][]any{{int64(1)}}},
  {"split_part(s,d,n)='b'",[][]any{{int64(1)}}},
  {"strpos(s,d)>0",[][]any{{int64(1)},{int64(4)}}},
  {"translate(s,d,t)='XbX'",[][]any{{int64(1)}}},
 } { citextStringRoutes(t,p,b,c,fixture.Select("SELECT id FROM cs_source WHERE "+tc.predicate+" ORDER BY id",[]string{"id"},[]string{"INT4"},tc.ids)) }
 citextStringRoutes(t,p,b,c,fixture.Select("SELECT id,s::text AS s FROM cs_source ORDER BY id",[]string{"id","s"},[]string{"INT4","TEXT"},[][]any{{int64(1),"AbA"},{int64(2),"x"},{int64(3),nil},{int64(4),""}}))
}
func citextStringMetadata(t *testing.T,b fixture.Backend,rows *sql.Rows) []*sql.ColumnType {
 t.Helper();cols,err:=rows.ColumnTypes();if err!=nil {t.Fatal(err)}
 names:=[]string{"r","s","p","t"};types:=[]string{"TEXT","TEXT","INT4","TEXT"}
 if len(cols)!=4 {t.Fatal("column count",len(cols))}
 for i,col:=range cols {
  scan:=reflect.TypeFor[string]();if i==2 {scan=reflect.TypeFor[int64]();if b.PG {scan=reflect.TypeFor[int32]()}}
  if col.Name()!=names[i]||col.DatabaseTypeName()!=types[i]||col.ScanType()!=scan {t.Fatalf("metadata %d: %s/%s/%v",i,col.Name(),col.DatabaseTypeName(),col.ScanType())}
  if n,ok:=col.Nullable();n||ok {t.Fatalf("computed nullability %v/%v",n,ok)}
  wantLength,knownLength:=int64(0),false
  if i!=2 {wantLength,knownLength=9223372036854775807,true}
  if n,ok:=col.Length();n!=wantLength||ok!=knownLength {t.Fatalf("computed length %d/%v want %d/%v",n,ok,wantLength,knownLength)}
  if p,s,ok:=col.DecimalSize();p!=0||s!=0||ok {t.Fatal("computed decimal",p,s,ok)}
 }
 return cols
}
