package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func normalCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "normal_part", "normal_moved", "normal_smoke")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			if b.PG {
				clean := func() {
					for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS tablefunc CASCADE", "DROP SCHEMA IF EXISTS normal_part CASCADE", "DROP SCHEMA IF EXISTS normal_moved CASCADE"} {
						if _, err := c.ExecContext(p.Ctx, q); err != nil { t.Error(q, err) }
					}
				}
				clean()
				defer clean()
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SCHEMA normal_part", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION tablefunc WITH SCHEMA normal_part", 0))
			body(t, p, b, c)
		})
	}
}
func normalNegative(q string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: "number of rows cannot be negative"}, args...)
	s.ErrorStage = "execute"
	return s
}
func TestReferenceTablefuncNormalRowsAndMetadata(t *testing.T) {
	normalCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		normalInstallBoundaries(t,p,b,c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT * FROM normal_part.normal_rand($1,$2,$3)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		for _, n := range []int{0,1,2,3,7} {
			run(fixture.TablefuncNormalSelect(fmt.Sprintf("SELECT * FROM normal_part.normal_rand(%d,-7,0)", n), n, -7))
			s := fixture.TablefuncNormalSelect(q, n, -7, n, -7, 0)
			run(s); run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.TablefuncNormalSelect("SELECT normal_part.normal_rand(3,-7,0)", 3, -7))
		run(fixture.Exec("SET search_path TO normal_part,public", 0))
		run(fixture.TablefuncNormalSelect("SELECT * FROM normal_rand(2,-7,0)", 2, -7))
		catalogStep := fixture.Select("SELECT p.proargtypes::text AS args,p.prorettype::integer AS result,p.proisstrict,p.provolatile,p.proretset,p.proparallel,p.procost,p.prorows,p.prosrc FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='normal_part' AND p.proname='normal_rand'", []string{"args","result","proisstrict","provolatile","proretset","proparallel","procost","prorows","prosrc"}, []string{"TEXT","INT4","BOOL","CHAR","BOOL","CHAR","FLOAT4","FLOAT4","TEXT"}, [][]any{{"23 701 701",int64(701),true,"v",true,"u",float64(1),float64(1000),"normal_rand"}})
		run(catalogStep)
		run(fixture.Prepared(catalogStep))
	})
}
func TestReferenceTablefuncNormalNullAndEvaluation(t *testing.T) {
	normalCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT * FROM normal_part.normal_rand($1,$2,$3)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }; defer st.Close()
		held := fixture.HeldStatement{Executor:c, Statement:st, SQL:q}
		for _, args := range [][]any{{nil,0,1},{3,nil,1},{3,0,nil},{nil,nil,nil},{-1,nil,1},{-1,0,nil},{0,math.Inf(1),math.NaN()},{3,-7,math.Copysign(0,-1)}} {
			n := 0; if args[0] == 3 && args[1] == -7 { n = 3 }
			s := fixture.TablefuncNormalSelect(q,n,-7,args...)
			fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s))
			fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
		}
		for _, literal := range []string{"NULL::integer,0,1","3,NULL::float8,1","3,0,NULL::float8","-1,NULL::float8,1"} {
			fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.TablefuncNormalSelect("SELECT * FROM normal_part.normal_rand("+literal+")",0,0))
		}
		s := fixture.ImplicitError(q,fixture.Diagnostic{Code:"22P02",Message:`invalid input syntax for type double precision: "oops"`,Context:"unnamed portal parameter $2 = '...'"},nil,"oops",1)
		fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
	})
}
func normalRead(t *testing.T, p *fixture.Pair, executor interface{ QueryContext(context.Context,string,...any)(*sql.Rows,error) }, q string, args ...any) []float64 {
	t.Helper()
	rows,err := executor.QueryContext(p.Ctx,q,args...)
	if err != nil { t.Fatal(err) }; defer rows.Close()
	cols,err := rows.ColumnTypes()
	if err != nil || len(cols)!=1 || cols[0].DatabaseTypeName()!="FLOAT8" { t.Fatal("normal FLOAT8 metadata",cols,err) }
	var values []float64
	for rows.Next() { var v float64; if err:=rows.Scan(&v);err!=nil {t.Fatal(err)}; values=append(values,v) }
	if err:=rows.Err();err!=nil {t.Fatal(err)}
	return values
}
func TestReferenceTablefuncNormalIEEE(t *testing.T) {
	normalCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		q:="SELECT * FROM normal_part.normal_rand($1,$2,$3)"
		st,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer st.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:q}
		for _,mean:=range []float64{2,math.NaN(),math.Inf(1),math.Inf(-1)} {
			for _,dev:=range []float64{0,1,-2,math.NaN(),math.Inf(1),math.Inf(-1)} {
				for _,exec:=range []interface{QueryContext(context.Context,string,...any)(*sql.Rows,error)}{c,held} {
					values:=normalRead(t,p,exec,q,31,mean,dev)
					if len(values)!=31 {t.Fatal("count",len(values))}
					for _,v:=range values {
						switch {
						case math.IsNaN(mean)||math.IsNaN(dev): if !math.IsNaN(v){t.Fatal("expected NaN",mean,dev,v)}
						case math.IsInf(dev,0): if !math.IsNaN(v)&&!math.IsInf(v,0)||math.IsInf(mean,0)&&math.IsInf(v,0)&&math.Signbit(v)!=math.Signbit(mean){t.Fatal("raw IEEE",mean,dev,v)}
						case math.IsInf(mean,0): if v!=mean {t.Fatal("infinite mean",v)}
						default: if math.IsNaN(v)||math.IsInf(v,0){t.Fatal("finite transform",v)}
						}
					}
					if len(normalRead(t,p,exec,q,0,mean,dev))!=0 {t.Fatal("zero count")}
				}
			}
		}
	})
}
func normalDistribution(t *testing.T, values []float64, n int) {
	t.Helper()
	if len(values)!=n {t.Fatal("sample count",len(values),n)}
	mean,m2:=0.0,0.0;lower,upper:=0,0
	for i,x:=range values {if math.IsNaN(x)||math.IsInf(x,0){t.Fatal("nonfinite sample")};z:=(x-5)/2;delta:=z-mean;mean+=delta/float64(i+1);m2+=delta*(z-mean);if z < -1 {lower++};if z>1 {upper++}}
	variance:=m2/float64(n)
	if math.Abs(mean)>=0.10||variance<0.80||variance>1.20||lower<100||upper<100 {t.Fatal("Gaussian properties",mean,variance,lower,upper)}
}
func TestReferenceTablefuncNormalDistribution(t *testing.T) {
	normalCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		q:="SELECT * FROM normal_part.normal_rand($1,$2,$3)"
		normalDistribution(t,normalRead(t,p,c,q,20000,5,2),20000)
		st,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer st.Close()
		normalDistribution(t,normalRead(t,p,fixture.HeldStatement{Executor:c,Statement:st,SQL:q},q,20000,5,-2),20000)
	})
}
