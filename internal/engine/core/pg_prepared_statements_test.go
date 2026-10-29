package core_test

import (
 "context"
 "errors"
 "reflect"
 "testing"
 "time"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/pgcatalog"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/sqltype"
 "stuntgres/internal/parser"
)

func ppsUnitCapture(e *core.Engine,r *core.PreparedRegistry,ctx context.Context) (*exec.Source,error) {
 d:=e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.PreparedStatementsOID})
 produce,_:=pgcatalog.ProducerFor(pgcatalog.PreparedStatementsOID)
 return core.CaptureCatalogSourceRows(pgcatalog.Input{Context:ctx,Source:d,Runtime:core.NewCatalogRuntime(core.CatalogRuntimeFields{PreparedStatements:r})},produce)
}
func ppsUnitRows(t *testing.T,e *core.Engine,r *core.PreparedRegistry) map[string][]sqltype.Value {
 t.Helper();source,err:=ppsUnitCapture(e,r,context.Background());if err!=nil {t.Fatal(err)}
 rows:=make(map[string][]sqltype.Value);for _,row:=range source.Anonymous {name,_:=row[0].Text();rows[name]=row};return rows
}
func ppsUnitExecute(t *testing.T,s *session.Session,clock time.Time,q string) error {
 t.Helper();op,err:=s.Acquire(context.Background());if err!=nil {t.Fatal(err)};defer op.Done();op.CommandStart=clock
 _,err=core.EngineOf(s).Execute(op,enginetest.Statement(t,q),0,nil);return err
}

func TestPGPreparedStatementsIdentity(t *testing.T) {
 for range 2 {
  e:=engine.New();view:=e.CatalogRead();d:=view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:16250})
  if err:=core.RelationProvider.Validate(view,d);err!=nil {t.Fatal(err)}
  if !d.VirtualView||d.Name()!="pg_prepared_statements"||d.RowType!=12097||len(d.Columns)!=8 {t.Fatal("relation identity",d)}
  row:=view.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:12097});array:=view.MustType(ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:12096})
  if row.OwningRelation!=d.Address()||array.OwningRelation!=d.Address()||row.Array!=12096||array.SQLType.Element()!=row.SQLType {t.Fatal("reciprocal type identity")}
  mappings:=view.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:12095});if mappings.Name()!="pg_user_mappings" {t.Fatal("mapping relation overwritten")}
  names:=[]string{"name","statement","prepare_time","parameter_types","result_types","from_sql","generic_plans","custom_plans"};types:=[]sqltype.ID{25,25,1184,2211,2211,16,20,20}
  for i,column:=range d.Columns {
   typ,_:=view.LookupType(types[i]);if column.Name!=names[i]||column.Usage.Type!=typ||!column.Nullable||!column.NullabilityKnown||column.OriginTable!=16250||column.OriginAttribute!=int16(i+1)||d.Attributes[i]!=int32(i+1) {t.Fatal("attribute identity",i,column)}
  }
  if len(d.Security.ACL.Entries)!=1||!d.Security.ACL.Entries[0].Grantee.Public||d.Security.ACL.Entries[0].Privileges!=authorization.SelectPrivilege {t.Fatal("PUBLIC SELECT",d.Security)}
  if _,ok:=pgcatalog.ProducerFor(16250);!ok {t.Fatal("missing producer")}
  registrations:=0;for _,registration:=range pgcatalog.Registrations() {if registration.OID==16250 {registrations++}}
  if registrations!=1 {t.Fatal("producer inventory membership",registrations)}
 }
}

func TestPGPreparedStatementsProjection(t *testing.T) {
 e:=engine.New();s:=enginetest.StartSession(t,e,nil);r:=core.PayloadOf(s).PreparedSQL
 clock:=time.Date(2024,2,3,4,5,6,123456000,time.UTC)
 source:="/* exact */ PREPARE unit_pps(integer,text) AS SELECT $1 AS i,$2 AS s"
 if err:=ppsUnitExecute(t,s,clock,source);err!=nil {t.Fatal(err)}
 definition:=r.Lookup("unit_pps");if definition.Created!=clock||!definition.FromSQL||definition.Source!=source {t.Fatal("admitted provenance",definition)}
 parameters:=definition.Description.Parameters.Types;results:=definition.Description.Results;folds:=definition.Description.Folds
 enginetest.SessionSQL(t,s,"PREPARE unit_zero AS SELECT FROM pg_prepared_statements")
 enginetest.SessionSQL(t,s,"CREATE TABLE unit_pps_write(i integer)")
 enginetest.SessionSQL(t,s,"PREPARE unit_write AS INSERT INTO unit_pps_write VALUES(1)")
 cycle:=s.NewProtocolCycle()
 preparedFoldProtocolCommand(t,e,s,cycle,func(scope *core.StatementScope)error {
  scope.Op.CommandStart=clock
  _,err:=scope.ParseProtocol("unit_wire","SELECT $1::integer AS i",[]uint32{23});return err
 })
 preparedFoldProtocolCommand(t,e,s,cycle,func(scope *core.StatementScope)error {_,err:=scope.ParseProtocol("","SELECT 8",nil);return err})
 preparedFoldProtocolCommand(t,e,s,cycle,func(scope *core.StatementScope)error {_,err:=scope.ParseProtocol("unit_empty","",[]uint32{23,25});return err})
 rows:=ppsUnitRows(t,e,r);if len(rows)!=5||rows[""]!=nil {t.Fatal("named membership",rows)}
 row:=rows["unit_pps"];instant,err:=row[2].TimestampTime();if err!=nil||!instant.Equal(clock) {t.Fatal("creation clock",instant,err)}
 for i,oid:=range []uint32{23,25} {p,_:=row[3].ArrayElement(i).TypeOID();v,_:=row[4].ArrayElement(i).TypeOID();if p!=oid||v!=oid {t.Fatal("signature identity",i,p,v)}}
 if row[3].ArrayDimension(0)!=(sqltype.ArrayDimension{Lower:1,Length:2})||!row[6].IsNull()||row[6].Type().ID()!=20||!row[7].IsNull()||row[7].Type().ID()!=20 {t.Fatal("typed arrays/statistics")}
 zero:=rows["unit_zero"];if zero[3].IsNull()||zero[3].ArrayLen()!=0||zero[4].IsNull()||zero[4].ArrayLen()!=0||zero[4].ArrayRank()!=0 {t.Fatal("zero-column result")}
 if !rows["unit_write"][4].IsNull() {t.Fatal("non-result has result descriptor")}
 empty:=rows["unit_empty"];if empty[3].ArrayLen()!=2||!empty[4].IsNull() {t.Fatal("protocol hint fallback")}
 for i,oid:=range []uint32{23,25} {got,_:=empty[3].ArrayElement(i).TypeOID();if got!=oid {t.Fatal("unused hint identity",got)}}
 wire:=r.Lookup("unit_wire");if wire.FromSQL||wire.Created!=clock {t.Fatal("protocol provenance")}
 preparedFoldProtocolCommand(t,e,s,cycle,func(scope *core.StatementScope)error {_,err:=scope.DescribeProtocolStatement("unit_wire");return err})
 if wire.Created!=clock||r.Lookup("unit_pps")!=definition||&parameters[0]!=&definition.Description.Parameters.Types[0]||&results[0]!=&definition.Description.Results[0]||definition.Description.Folds!=folds {t.Fatal("capture/rebind replaced retained definition backing")}
 current:=engine.New();retagged:=ppsUnitRows(t,current,r)["unit_pps"]
 for i,value:=range retagged {d:=current.CatalogRead().MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:16250});if value.Type()!=d.Columns[i].Usage.Type||value.Type()==row[i].Type() {t.Fatal("current catalog ownership",i)}}
 if retagged[3].ArrayElement(0).Type()!=retagged[3].Type().Element()||retagged[3].ArrayElement(0).Type()==row[3].ArrayElement(0).Type() {t.Fatal("array elements retained foreign descriptors")}
 retained:=row[3];again:=ppsUnitRows(t,e,r)["unit_pps"][3]
 if retained==again {t.Fatal("captures alias array backing")}
 if definition.Description.Parameters.Types[0]!=parameters[0] {t.Fatal("projection changed signature")}
 for _,at:=range []int32{1,2,3,4,5,6,7,8} {
  ctx,cancel:=context.WithCancel(context.Background());probe:=&cancelAtErrCheck{Context:ctx,cancel:cancel,at:at}
  got,err:=ppsUnitCapture(e,r,probe);cancel();if got!=nil||!errors.Is(err,context.Canceled) {t.Fatal("partial canceled capture",at,got,err)}
 }
}

func TestPGPreparedStatementsOwnership(t *testing.T) {
 e:=engine.New();a,b:=enginetest.StartSession(t,e,nil),enginetest.StartSession(t,e,nil)
 clock:=time.Date(2023,1,2,3,4,5,1000,time.UTC)
 if err:=ppsUnitExecute(t,a,clock,"PREPARE unit_owner(integer) AS SELECT $1");err!=nil {t.Fatal(err)}
 if err:=ppsUnitExecute(t,b,clock,"PREPARE unit_owner AS SELECT 22");err!=nil {t.Fatal(err)}
 r,peer:=core.PayloadOf(a).PreparedSQL,core.PayloadOf(b).PreparedSQL;original:=r.Lookup("unit_owner")
 source,err:=ppsUnitCapture(e,r,context.Background());if err!=nil {t.Fatal(err)}
 captured:=source.Anonymous[0];snapshot:=append([]sqltype.Value(nil),captured...);array:=captured[3];element:=array.ArrayElement(0)
 if err:=ppsUnitExecute(t,a,clock.Add(time.Hour),"PREPARE unit_owner AS SELECT 99");err==nil||r.Lookup("unit_owner")!=original||original.Created!=clock {t.Fatal("failed publication replaced provenance",err)}
 ctx,cancel:=context.WithCancel(context.Background());op,err:=a.Acquire(ctx);if err!=nil {t.Fatal(err)}
 // Reach the common session publication preflight after normal command gating.
 scope,err:=e.StartStatement(op);if err!=nil {op.Done();t.Fatal(err)}
 raw:=&parser.DeallocateSQL{All:true};if err:=scope.Gate(raw);err!=nil {op.Done();t.Fatal(err)}
 cancel();_,err=core.ScopeExecuteDeallocateSQL(scope,raw,0)
 if !errors.Is(err,context.Canceled)||r.Lookup("unit_owner")!=original||original.Created!=clock||scope.Completed {t.Fatal("preflight canceled publication changed provenance",err)}
 _=scope.Finish(err);op.Done()
 if err:=r.EndSession(b.SessionToken,session.CleanClose);err==nil||r.Lookup("unit_owner")!=original {t.Fatal("foreign token changed owner")}
 a.RequestEnd(session.CleanClose);if err:=a.WaitEnd(context.Background());err!=nil {t.Fatal(err)}
 if r.Lookup("unit_owner")!=nil||peer.Lookup("unit_owner")==nil {t.Fatal("physical end crossed owners")}
 if !reflect.DeepEqual(captured,snapshot)||captured[3]!=array||captured[3].ArrayElement(0)!=element {t.Fatal("EndSession mutated captured backing")}
 if rows:=ppsUnitRows(t,engine.New(),r);len(rows)!=0 {t.Fatal("ended registry resurrected",rows)}
 if !reflect.DeepEqual(captured,snapshot) {t.Fatal("catalog replacement changed detached values")}
}
