package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func bitwiseAggregateCallers(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	for _, q := range []string{
		"SELECT bit_and(i),bit_or(i),bit_xor(i) FROM (SELECT i FROM bw WHERE k='fold') AS z",
		"WITH bw_cte AS (SELECT i FROM bw WHERE k='fold') SELECT bit_and(i),bit_or(i),bit_xor(i) FROM bw_cte",
		"SELECT bit_and(i ORDER BY b,i),bit_or(i),bit_xor(i) FROM bw WHERE k='fold'",
	} {
		bitwiseAggregateRoutes(t,p,b,bitwiseAggregateTriple(b.PG,q,"INT4",[]any{int64(0),int64(15),int64(5)}))
	}
	for _, prepared := range []bool{false,true} {
		s := bitwiseAggregateTriple(b.PG,"SELECT bit_and(i),bit_or(i),bit_xor(i) FROM bw WHERE k='fold'","INT4",[]any{int64(0),int64(15),int64(5)})
		s.Prepared,s.NonContext = prepared,true
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
	}
	const q = "SELECT bit_xor($1::integer) FROM bw WHERE k='fold' AND i IS NOT NULL"
	stmt, err := b.DB.PrepareContext(p.Ctx,q)
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	held := fixture.HeldStatement{b.DB,stmt,q}
	for _, arg := range []any{7,nil,9} {
		var value any
		if arg != nil { value=int64(arg.(int)) }
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,[]string{"bit_xor"},[]string{"INT4"},[][]any{{value}},arg))
	}
	d := fixture.ControlsReferenceDiagnostic("22P02",`invalid input syntax for type integer: "bad"`)
	d.Context = "unnamed portal parameter $1 = '...'"
	for _, query := range []bool{false,true} {
		s := fixture.ErrorDiagnostic(q,d,"bad")
		s.SelectRows,s.ErrorStage = query,"execute"
		fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
	}
	fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,[]string{"bit_xor"},[]string{"INT4"},[][]any{{int64(7)}},7))
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE bw_dest(a INT,o INT,x INT)",0))
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE bw_bit_dest(a \"bit\",o \"bit\",x \"bit\")",0))
	for _, prepared := range []bool{false,true} {
		for _, tc := range []struct{ q string; affected int64 }{
			{"INSERT INTO bw_dest SELECT bit_and(i),bit_or(i),bit_xor(i) FROM bw WHERE k='fold'",1},
			{"INSERT INTO bw_dest SELECT bit_and(i),bit_or(i),bit_xor(i) FROM bw WHERE FALSE",1},
			{"INSERT INTO bw_dest SELECT bit_and(i),bit_or(i),bit_xor(i) FROM bw WHERE FALSE GROUP BY k",0},
			{"INSERT INTO bw_bit_dest SELECT bit_and(f),bit_or(f),bit_xor(f) FROM bw WHERE k='fold'",1},
			{"INSERT INTO bw_bit_dest SELECT bit_and(v),bit_or(v),bit_xor(v) FROM bw WHERE FALSE",1},
		} {
			s := fixture.Exec(tc.q,tc.affected)
			s.Prepared=prepared
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
		}
	}
	integerBag := fixture.Select("SELECT a,o,x FROM bw_dest",[]string{"a","o","x"},[]string{"INT4","INT4","INT4"},[][]any{{int64(0),int64(15),int64(5)},{nil,nil,nil},{int64(0),int64(15),int64(5)},{nil,nil,nil}})
	integerBag.Bag=true
	bitBag := fixture.Select("SELECT a,o,x FROM bw_bit_dest",[]string{"a","o","x"},[]string{"BIT","BIT","BIT"},[][]any{{"00000","11111","01010"},{nil,nil,nil},{"00000","11111","01010"},{nil,nil,nil}})
	bitBag.Bag=true
	bitwiseAggregateRoutes(t,p,b,integerBag)
	bitwiseAggregateRoutes(t,p,b,bitBag)
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE bw_atomic(k INT,v \"bit\")",0))
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO bw_atomic VALUES(1,B'0'),(1,B'1'),(2,B'0'),(2,B'01')",4))
	for _, name := range []string{"bit_and","bit_or","bit_xor"} {
		for _, prepared := range []bool{false,true} {
			// One valid group precedes a failing group. INSERT must publish none.
			s := fixture.ErrorDiagnostic("INSERT INTO bw_bit_dest(a) SELECT "+name+"(v ORDER BY k) FROM bw_atomic GROUP BY k ORDER BY k",bitwiseAggregateWidthDiagnostic(name))
			s.Prepared,s.ErrorStage=prepared,"execute"
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
			bitwiseAggregateRoutes(t,p,b,bitBag)
		}
	}
	// Bind BIT/VARBIT through destination input; no excluded explicit BIT cast.
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE bw_binds(f BIT(5),v VARBIT(9))",0))
	const insert = "INSERT INTO bw_binds VALUES($1,$2)"
	inputStmt, err := b.DB.PrepareContext(p.Ctx,insert)
	if err != nil { t.Fatal(err) }
	defer inputStmt.Close()
	for _, input := range []any{"00101",[]byte("00101"),nil} {
		fixture.RunStep(t,p.Ctx,fixture.HeldStatement{b.DB,inputStmt,insert},b.Recorder,fixture.Exec(insert,1,input,input))
	}
	bitwiseAggregateRoutes(t,p,b,bitwiseAggregateTriple(b.PG,"SELECT bit_and(f),bit_or(f),bit_xor(f) FROM bw_binds","BIT",[]any{"00101","00101","00000"}))
	bitwiseAggregateRoutes(t,p,b,bitwiseAggregateTriple(b.PG,"SELECT bit_and(v),bit_or(v),bit_xor(v) FROM bw_binds","BIT",[]any{"00101","00101","00000"}))
	bitwiseAggregateAuthority(t,p,b)
}

func TestReferenceBitwiseAggregateCallers(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"bw_reader"})
	bitwiseAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		bitwiseAggregateSetup(t,p,b)
		bitwiseAggregateCallers(t,p,b)
	})
}

func bitwiseAggregateAuthority(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE ROLE bw_reader",0))
	if b.PG {
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("GRANT USAGE ON SCHEMA public TO bw_reader",0))
	}
	conn,err := b.DB.Conn(p.Ctx)
	if err!=nil { t.Fatal(err) }
	defer conn.Close()
	defer conn.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
	fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("SET SESSION AUTHORIZATION bw_reader",0))
	for _, column := range []string{"i","f","v"} {
		for _, name := range []string{"bit_and","bit_or","bit_xor"} {
			for _, suffix := range []string{""," WHERE FALSE"} {
				q := "SELECT "+name+"("+column+") FROM bw"+suffix
				d := fixture.ControlsReferenceDiagnostic("42501","permission denied for table bw")
				for _, prepared := range []bool{false,true} {
					s := fixture.ErrorDiagnostic(q,d)
					s.SelectRows,s.Prepared,s.ErrorStage = true,prepared,"execute"
					fixture.RunStep(t,p.Ctx,conn,b.Recorder,s)
				}
			}
		}
	}
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("GRANT SELECT(i,f,v) ON bw TO bw_reader",0))
	for _, tc := range []struct{ col,typ string }{{"i","INT4"},{"f","BIT"},{"v","BIT"}} {
		q := "SELECT bit_and("+tc.col+"),bit_or("+tc.col+"),bit_xor("+tc.col+") FROM bw WHERE FALSE"
		stmt,err := conn.PrepareContext(p.Ctx,q)
		if err!=nil { t.Fatal(err) }
		func() {
			defer stmt.Close()
			held := fixture.HeldStatement{conn,stmt,q}
			s := bitwiseAggregateTriple(b.PG,q,tc.typ,[]any{nil,nil,nil})
			fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("REVOKE SELECT("+tc.col+") ON bw FROM bw_reader",0))
			failure := fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic("42501","permission denied for table bw"))
			failure.SelectRows,failure.ErrorStage = true,"execute"
			fixture.RunStep(t,p.Ctx,held,b.Recorder,failure)
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("GRANT SELECT("+tc.col+") ON bw TO bw_reader",0))
			fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
		}()
	}
}
