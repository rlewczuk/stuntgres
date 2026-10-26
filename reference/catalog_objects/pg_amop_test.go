package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var pgAmopColumns = []string{"oid", "amopfamily", "amoplefttype", "amoprighttype", "amopstrategy", "amoppurpose", "amopopr", "amopmethod", "amopsortfamily"}
var pgAmopTypes = []string{"OID", "OID", "OID", "OID", "INT2", "CHAR", "OID", "OID", "OID"}
const pgAmopWitnessQuery = "SELECT * FROM pg_catalog.pg_amop WHERE oid IN (10010,10015,10017,10264,10319) ORDER BY oid"
const pgAmopProbe = "SELECT * FROM pg_catalog.pg_amop WHERE oid=$1::oid"

// Released witnesses, not rows harvested from the native implementation.
func pgAmopFacts() [][]any {
	return [][]any{
		{int64(10010),int64(1976),int64(21),int64(20),int64(1),"s",int64(1864),int64(403),int64(0)},
		{int64(10015),int64(1976),int64(23),int64(23),int64(1),"s",int64(97),int64(403),int64(0)},
		{int64(10017),int64(1976),int64(23),int64(23),int64(3),"s",int64(96),int64(403),int64(0)},
		{int64(10264),int64(1977),int64(23),int64(23),int64(1),"s",int64(96),int64(405),int64(0)},
		{int64(10319),int64(1029),int64(600),int64(600),int64(15),"o",int64(517),int64(783),int64(1970)},
	}
}

func pgAmopRows(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,q string,want [][]any,prepared bool,args ...any) {
	t.Helper()
	s := fixture.Select(q,pgAmopColumns,pgAmopTypes,want,args...)
	s.Prepared = prepared
	s.CheckMetadata = func(t *testing.T,r *sql.Rows) { pgAmopMetadata(t,r,b.PG) }
	fixture.PgDescriptionExecutorRows(t,p,b,e,s)
}

func pgAmopMetadata(t *testing.T,r *sql.Rows,pg bool) {
	t.Helper()
	pgAmopResultMetadata(t,r,pg,false)
}

func pgAmopResultMetadata(t *testing.T,r *sql.Rows,pg,nullable bool) {
	t.Helper()
	cols, err := r.Columns(); if err != nil || !reflect.DeepEqual(cols,pgAmopColumns) { t.Fatal(cols,err) }
	types, err := r.ColumnTypes(); if err != nil || len(types) != 9 { t.Fatal(types,err) }
	for i,c := range types {
		scan := reflect.TypeFor[int64]()
		if i == 5 { scan = reflect.TypeFor[string]() }
		if pg {
			scan = reflect.TypeFor[any]()
			if i == 4 { scan = reflect.TypeFor[int16]() }
		}
		if c.Name() != cols[i] || c.DatabaseTypeName() != pgAmopTypes[i] || c.ScanType() != scan { t.Fatalf("column %d: %q %q %v; want %q %q %v",i,c.Name(),c.DatabaseTypeName(),c.ScanType(),cols[i],pgAmopTypes[i],scan) }
		if n,k := c.Nullable(); n != (!pg && nullable) || k == pg { t.Fatal("nullability",i,n,k) }
		if n,k := c.Length(); n != 0 || k { t.Fatal("length",i,n,k) }
		if a,z,k := c.DecimalSize(); a != 0 || z != 0 || k { t.Fatal("decimal",i,a,z,k) }
	}
}

func pgAmopConsume(t *testing.T,r *sql.Rows) [][]any {
	t.Helper()
	var got [][]any
	for r.Next() {
		var oid,family,left,right,strategy,operator,method,sort int64
		var purpose string
		if err := r.Scan(&oid,&family,&left,&right,&strategy,&purpose,&operator,&method,&sort); err != nil { t.Fatal(err) }
		got = append(got,[]any{oid,family,left,right,strategy,purpose,operator,method,sort})
	}
	if err := r.Err(); err != nil { t.Fatal(err) }
	return got
}

func pgAmopInventory(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	t.Helper()
	for _, prepared := range []bool{false,true} { pgAmopRows(t,p,b,b.DB,pgAmopWitnessQuery,pgAmopFacts(),prepared) }
	for _, s := range []fixture.Step{
		fixture.Select("SELECT amopstrategy,amopopr FROM pg_amop WHERE amopfamily=1976 AND amoplefttype=23 AND amoprighttype=23 ORDER BY amopstrategy",[]string{"amopstrategy","amopopr"},[]string{"INT2","OID"},[][]any{{int64(1),int64(97)},{int64(2),int64(523)},{int64(3),int64(96)},{int64(4),int64(525)},{int64(5),int64(521)}}),
		fixture.Select("SELECT a.oid,o.oprleft,o.oprright,f.opfmethod,m.amname FROM pg_amop a JOIN pg_operator o ON o.oid=a.amopopr JOIN pg_opfamily f ON f.oid=a.amopfamily JOIN pg_am m ON m.oid=a.amopmethod WHERE a.oid IN (10010,10015,10017,10264,10319) ORDER BY a.oid",[]string{"oid","oprleft","oprright","opfmethod","amname"},[]string{"OID","OID","OID","OID","NAME"},[][]any{{int64(10010),int64(21),int64(20),int64(403),"btree"},{int64(10015),int64(23),int64(23),int64(403),"btree"},{int64(10017),int64(23),int64(23),int64(403),"btree"},{int64(10264),int64(23),int64(23),int64(405),"hash"},{int64(10319),int64(600),int64(600),int64(783),"gist"}}),
		fixture.Select("SELECT a.oid,f.oid,f.opfmethod,m.amname FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopsortfamily JOIN pg_am m ON m.oid=f.opfmethod WHERE a.oid=10319",[]string{"oid","oid","opfmethod","amname"},[]string{"OID","OID","OID","NAME"},[][]any{{int64(10319),int64(1970),int64(403),"btree"}}),
	} { fixture.PgDescriptionRows(t,p,b,s); fixture.PgDescriptionRows(t,p,b,fixture.Prepared(s)) }
	stmt, err := b.DB.PrepareContext(p.Ctx,pgAmopProbe); if err != nil { t.Fatal(err) }; defer stmt.Close()
	held := fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:pgAmopProbe}
	for _, route := range []struct{ e fixture.Executor; prepared bool }{{b.DB,false},{b.DB,true},{held,false}} {
		for _, index := range []int{0,3,4} { row := pgAmopFacts()[index]; pgAmopRows(t,p,b,route.e,pgAmopProbe,[][]any{row},route.prepared,row[0]) }
		for _, value := range []any{nil,int64(0)} { pgAmopRows(t,p,b,route.e,pgAmopProbe,nil,route.prepared,value) }
	}
	const membership = "SELECT * FROM pg_amop WHERE oid=$1::oid AND amopfamily=$2::oid AND amopmethod=$3::oid"
	memberStmt, err := b.DB.PrepareContext(p.Ctx,membership); if err != nil { t.Fatal(err) }; defer memberStmt.Close()
	memberHeld := fixture.HeldStatement{Executor:b.DB,Statement:memberStmt,SQL:membership}
	for _, route := range []struct{ e fixture.Executor; prepared bool }{{b.DB,false},{b.DB,true},{memberHeld,false}} {
		for _, row := range pgAmopFacts() {
			pgAmopRows(t,p,b,route.e,membership,[][]any{row},route.prepared,row[0],row[1],row[7])
		}
		for _, args := range [][]any{{int64(10015),int64(1977),int64(403)},{int64(10015),int64(1976),int64(405)},{nil,int64(1976),int64(403)},{int64(10015),nil,int64(403)},{int64(10015),int64(1976),nil},{int64(0),int64(0),int64(0)}} {
			pgAmopRows(t,p,b,route.e,membership,nil,route.prepared,args...)
		}
	}
	if !b.PG {
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT count(*),count(DISTINCT oid) FROM pg_amop WHERE oid<>0",[]string{"count","count"},[]string{"INT8","INT8"},[][]any{{int64(638),int64(638)}}))
		pgAmopRows(t,p,b,b.DB,"SELECT * FROM pg_amop WHERE amopmethod=2742",nil,false)
	}
}

func TestReferencePGAmopInventory(t *testing.T) { pgOpclassScenario(t,pgAmopInventory) }
