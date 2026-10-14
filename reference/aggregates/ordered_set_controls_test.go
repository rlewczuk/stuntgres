package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceOrderedSetAggregateControls(t *testing.T) {
	var emitted string
	orderedSetScenario(t,func(t *testing.T,p *fixture.Pair,c *sql.Conn,b fixture.Backend) {
		q:="SELECT c,d,a::text AS a,iv::text AS iv,m FROM os_control_view"
		want:=fixture.Select(q,[]string{"c","d","a","iv","m"},[]string{"FLOAT8","INT4","TEXT","TEXT","INT4"},[][]any{{float64(25),int64(20),"[-1:0]={40,10}","1 day",int64(10)}})
		stored:=fixture.Select("SELECT a::text AS a,iv::text AS iv FROM os_control_result",[]string{"a","iv"},[]string{"TEXT","TEXT"},[][]any{{"{10,40}","{00:00:00,\"2 days\"}"}})
		grouped:=fixture.Select("SELECT p,v FROM os_control_grouped ORDER BY p NULLS LAST",[]string{"p","v"},[]string{"FLOAT8","FLOAT8"},[][]any{{float64(.25),float64(12.5)},{float64(.75),float64(37.5)},{nil,nil}})
		collated:=fixture.Select("SELECT m,d FROM os_control_collated",[]string{"m","d"},[]string{"TEXT","TEXT"},[][]any{{"A","é"}})
		casts:=fixture.Select("SELECT nested::text AS nested,nulls::text AS nulls,computed::text AS computed,coerced::text AS coerced FROM os_control_array_casts",[]string{"nested","nulls","computed","coerced"},[]string{"TEXT","TEXT","TEXT","TEXT"},[][]any{{"[-1:0]={40,10}",nil,"[-1:0]={40,10}","[-1:0]={40,10}"}})
		carriers:=fixture.ArrayReferenceSelect("SELECT n,na,ca,ta,ra,pa,fa,e::text AS e,ea,d,da,co,coa,ns,nsa FROM os_control_carrier_view",
			[]string{"n","na","ca","ta","ra","pa","fa","e","ea","d","da","co","coa","ns","nsa"},
			[]string{"NAME","_NAME","_REGCLASS","_REGTYPE","_REGROLE","_REGPROC","_REGPROCEDURE","TEXT","_OS_CONTROL_ENUM","INT4","_OS_CONTROL_DOMAIN","REGCOLLATION","_REGCOLLATION","REGNAMESPACE","_REGNAMESPACE"},
			[]string{"NAME","_NAME","_REGCLASS","_REGTYPE","_REGROLE","_REGPROC","_REGPROCEDURE","TEXT","_TEXT","INT4","_INT4","REGCOLLATION","_REGCOLLATION","REGNAMESPACE","_REGNAMESPACE"},
			[][]any{{"a","{a,NULL,a}","{pg_class,NULL,pg_class}","{integer,NULL,integer}","{pg_monitor,NULL,pg_monitor}","{boolin,NULL,boolin}","{abs(integer),NULL,abs(integer)}","z","{z,NULL,z}",int64(7),"{7,NULL,7}","os_control_alias","{os_control_alias,NULL,os_control_alias}","pg_catalog","{pg_catalog,NULL,pg_catalog}"}},b.PG)
		if b.PG {carriers.Types[8]="";carriers.Types[10]="";carriers.Types[11]="";carriers.Types[12]=""}
		carriers.CheckRaw=func(t *testing.T,column,row int,value any) bool {
			if column!=3&&column!=5&&column!=6&&column<11 {return false}
			want:=carriers.Rows[row][column].(string)
			if b.PG {
				bytes,ok:=value.([]byte);if !ok||bytes==nil||string(bytes)!=want {t.Fatalf("registry control raw %d got %T %#v want []byte(%q)",column,value,value,want)}
			} else {
				text,ok:=value.(string);if !ok||text!=want {t.Fatalf("registry control raw %d got %T %#v want string %q",column,value,value,want)}
			}
			return true
		}
		if b.PG {
			if emitted=="" {t.Fatal("actual native dump missing before PG replay")}
			defer c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
			if _,err:=c.ExecContext(p.Ctx,emitted);err!=nil {t.Fatal(err)}
			orderedSetBoth(t,p,c,b,want);orderedSetBoth(t,p,c,b,stored)
			orderedSetBoth(t,p,c,b,grouped);orderedSetBoth(t,p,c,b,collated)
			orderedSetBoth(t,p,c,b,casts)
			orderedSetBoth(t,p,c,b,carriers)
			return
		}
		orderedSetSetup(t,p,c,b)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE os_interval(v INTERVAL)",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO os_interval VALUES('0'),('2 days')",2))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE VIEW os_control_view AS SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY x) AS c,percentile_disc(.5) WITHIN GROUP(ORDER BY x) AS d,percentile_disc('[-1:0]={1,0}'::float8[]) WITHIN GROUP(ORDER BY x) AS a,(SELECT percentile_cont(.5) WITHIN GROUP(ORDER BY v) FROM os_interval) AS iv,mode() WITHIN GROUP(ORDER BY x) AS m FROM os_input",0))
		// Explicit nested casts and typed NULLs must retain their meaning
		// without acquiring another literal annotation on each dump replay.
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE VIEW os_control_array_casts AS SELECT percentile_disc(CAST(CAST('[-1:0]={1,0}' AS FLOAT8[]) AS FLOAT8[])) WITHIN GROUP(ORDER BY x) AS nested,percentile_cont(NULL::float8[]) WITHIN GROUP(ORDER BY x) AS nulls,percentile_disc(COALESCE(NULL::float8[],'[-1:0]={1,0}'::float8[])) WITHIN GROUP(ORDER BY x) AS computed,percentile_disc('[-1:0]={1,0}'::int4[]) WITHIN GROUP(ORDER BY x) AS coerced FROM os_input",0))
		orderedSetBoth(t,p,c,b,casts)
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE os_control_result AS SELECT percentile_disc('{0,1}'::float8[]) WITHIN GROUP(ORDER BY x) AS a,(SELECT percentile_cont('{0,1}'::float8[]) WITHIN GROUP(ORDER BY v) FROM os_interval) AS iv FROM os_input",1))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE VIEW os_control_grouped AS SELECT p,percentile_cont(p) WITHIN GROUP(ORDER BY x::float8) FILTER(WHERE x IS NOT NULL) AS v FROM os_input GROUP BY p",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE COLLATION os_control_alias FROM pg_catalog.pg_unicode_fast",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE os_control_text(v TEXT)",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO os_control_text VALUES('A'),('Z'),('a'),('é')",4))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE VIEW os_control_collated AS SELECT mode() WITHIN GROUP(ORDER BY v COLLATE os_control_alias) FILTER(WHERE v IS NOT NULL) AS m,percentile_disc(0) WITHIN GROUP(ORDER BY v COLLATE os_control_alias DESC) AS d FROM os_control_text",0))
		orderedSetBoth(t,p,c,b,grouped);orderedSetBoth(t,p,c,b,collated)
		// Registry identity comes from stored symbolic catalog inputs, not
		// allocated IDs or a forbidden scalar REGROLE view constant.
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TYPE os_control_enum AS ENUM('z','a')",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE DOMAIN os_control_domain AS INT",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE os_control_carriers(n NAME,c REGCLASS,t REGTYPE,r REGROLE,p REGPROC,f REGPROCEDURE,e os_control_enum,d os_control_domain,co REGCOLLATION,ns REGNAMESPACE)",0))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO os_control_carriers VALUES('a','pg_class','integer','pg_monitor','boolin','abs(integer)','z',7,'public.os_control_alias','pg_catalog'),(NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL)",2))
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE VIEW os_control_carrier_view AS SELECT mode() WITHIN GROUP(ORDER BY n) AS n,percentile_disc('{1,NULL,0}'::float8[]) WITHIN GROUP(ORDER BY n) AS na,percentile_disc('{1,NULL,0}'::float8[]) WITHIN GROUP(ORDER BY c) AS ca,percentile_disc('{1,NULL,0}'::float8[]) WITHIN GROUP(ORDER BY t) AS ta,percentile_disc('{1,NULL,0}'::float8[]) WITHIN GROUP(ORDER BY r) AS ra,percentile_disc('{1,NULL,0}'::float8[]) WITHIN GROUP(ORDER BY p) AS pa,percentile_disc('{1,NULL,0}'::float8[]) WITHIN GROUP(ORDER BY f) AS fa,mode() WITHIN GROUP(ORDER BY e) AS e,percentile_disc('{1,NULL,0}'::float8[]) WITHIN GROUP(ORDER BY e) AS ea,mode() WITHIN GROUP(ORDER BY d) AS d,percentile_disc('{1,NULL,0}'::float8[]) WITHIN GROUP(ORDER BY d) AS da,mode() WITHIN GROUP(ORDER BY co) AS co,percentile_disc('{1,NULL,0}'::float8[]) WITHIN GROUP(ORDER BY co) AS coa,mode() WITHIN GROUP(ORDER BY ns) AS ns,percentile_disc('{1,NULL,0}'::float8[]) WITHIN GROUP(ORDER BY ns) AS nsa FROM os_control_carriers",0))
		orderedSetBoth(t,p,c,b,carriers)
		orderedSetBoth(t,p,c,b,want);orderedSetBoth(t,p,c,b,stored)
		stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
		old,err:=stmt.QueryContext(p.Ctx);if err!=nil {t.Fatal(err)};defer old.Close()
		var dump bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&dump);err!=nil {t.Fatal(err)}
		emitted=dump.String()
		if !strings.Contains(emitted,"WITHIN GROUP") {t.Fatal("dump lost ordered-set syntax")}
		fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO os_input VALUES(50,.5)",1))
		changed:=fixture.Select(q,want.Columns,want.Types,[][]any{{float64(30),int64(30),"[-1:0]={50,10}","1 day",int64(10)}})
		fixture.RunStep(t,p.Ctx,held,b.Recorder,changed)
		if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil {t.Fatal(err)}
		if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(emitted));err!=nil {t.Fatal(err)}
		fixture.RunStep(t,p.Ctx,held,b.Recorder,want)
		var cvalue float64;var d,m int64;var a,iv string
		if !old.Next() {t.Fatal("old control cursor lost",old.Err())}
		if err:=old.Scan(&cvalue,&d,&a,&iv,&m);err!=nil {t.Fatal(err)}
		if !reflect.DeepEqual([]any{cvalue,d,a,iv,m},want.Rows[0])||old.Next()||old.Err()!=nil {t.Fatal("old control cursor changed")}
		if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(emitted+"\nCREATE TABLE os_invalid(v absent_os_type);"));err==nil {t.Fatal("invalid replacement succeeded")}
		ctx,cancel:=context.WithCancel(p.Ctx);cancel()
		if err:=stuntgres.Restore(ctx,p.Native,strings.NewReader(emitted));!errors.Is(err,context.Canceled) {t.Fatal("canceled replacement",err)}
		fixture.RunStep(t,p.Ctx,held,b.Recorder,want);orderedSetBoth(t,p,c,b,stored)
		orderedSetBoth(t,p,c,b,grouped);orderedSetBoth(t,p,c,b,collated)
		orderedSetBoth(t,p,c,b,casts)
		orderedSetBoth(t,p,c,b,carriers)
		var redump bytes.Buffer
		if err:=stuntgres.Dump(p.Ctx,p.Native,&redump);err!=nil {t.Fatal(err)}
		if emitted!=redump.String() {t.Fatalf("replacement changed redump\nWANT emitted dump:\n%s\nGOT restored dump:\n%s",emitted,redump.String())}
		connector,err:=stuntgres.NewDriver().OpenConnector("ordered-set-restored/"+t.Name());if err!=nil {t.Fatal(err)}
		fresh:=sql.OpenDB(connector);defer fresh.Close()
		if err:=stuntgres.Restore(p.Ctx,fresh,strings.NewReader(emitted));err!=nil {t.Fatal(err)}
		// Public independent Driver restore, without borrowing fixture internals.
		rows,err:=fresh.QueryContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer rows.Close()
		if !rows.Next() {t.Fatal("independent restore empty",rows.Err())}
		if err:=rows.Scan(&cvalue,&d,&a,&iv,&m);err!=nil {t.Fatal(err)}
		if !reflect.DeepEqual([]any{cvalue,d,a,iv,m},want.Rows[0])||rows.Next()||rows.Err()!=nil {t.Fatal("independent restored values")}
		independent,err:=fresh.QueryContext(p.Ctx,carriers.Query);if err!=nil {t.Fatal(err)};defer independent.Close()
		metadata,err:=independent.ColumnTypes();if err!=nil||len(metadata)!=len(carriers.Columns) {t.Fatal("independent carrier schema",metadata,err)}
		for i,column:=range metadata {
			if column.Name()!=carriers.Columns[i]||column.DatabaseTypeName()!=carriers.Types[i] {t.Fatal("independent carrier authority",i,column)}
		}
		values:=make([]any,len(carriers.Columns));destinations:=make([]any,len(values))
		for i:=range values {destinations[i]=&values[i]}
		if !independent.Next() {t.Fatal("independent carrier restore empty",independent.Err())}
		if err:=independent.Scan(destinations...);err!=nil {t.Fatal(err)}
		if !reflect.DeepEqual(values,carriers.Rows[0])||independent.Next()||independent.Err()!=nil {t.Fatal("independent carrier remap/output",values,carriers.Rows[0],independent.Err())}
	})
}

func TestReferenceOrderedSetAggregateFeatureSmoke(t *testing.T) {
	for _,scenario:=range []struct{name string;body func(*testing.T)} {
		{"values",TestReferenceOrderedSetAggregateValues},
		{"arrays",TestReferenceOrderedSetAggregateArrays},
		{"types",TestReferenceOrderedSetAggregateTypes},
		{"carrier_arrays",TestReferenceOrderedSetAggregateCarrierArrays},
		{"carrier_boundaries",TestReferenceOrderedSetAggregateCarrierBoundaries},
		{"intervals",TestReferenceOrderedSetAggregateIntervals},
		{"mode",TestReferenceOrderedSetAggregateMode},
		{"direct",TestReferenceOrderedSetAggregateDirectArguments},
		{"diagnostics",TestReferenceOrderedSetAggregateDiagnostics},
		{"precedence",TestReferenceOrderedSetAggregatePrecedence},
		{"parameters",TestReferenceOrderedSetAggregateParameters},
		{"plain_routes",TestReferenceOrderedSetAggregatePlainRoutes},
		{"grammar_contexts",TestReferenceOrderedSetAggregateGrammarContexts},
		{"metadata",TestReferenceOrderedSetAggregateMetadata},
		{"custom_metadata",TestReferenceOrderedSetAggregateCustomMetadata},
		{"controls",TestReferenceOrderedSetAggregateControls},
	} {t.Run(scenario.name,scenario.body)}
}
