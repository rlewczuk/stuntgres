package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func bitwiseAggregateBoundaries(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE bw_signed(s SMALLINT,i INTEGER,b BIGINT)",0))
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO bw_signed VALUES(-32768,-2147483648,-9223372036854775808),(32767,2147483647,9223372036854775807)",2))
	for _, tc := range []struct{ col, typ string; minimum int64 }{{"s","INT2",-32768},{"i","INT4",-2147483648},{"b","INT8",-9223372036854775808}} {
		q := "SELECT bit_and("+tc.col+"),bit_or("+tc.col+"),bit_xor("+tc.col+") FROM bw_signed"
		bitwiseAggregateRoutes(t,p,b,bitwiseAggregateTriple(b.PG,q,tc.typ,[]any{int64(0),int64(-1),int64(-1)}))
		bitwiseAggregateRoutes(t,p,b,bitwiseAggregateTriple(b.PG,q+" WHERE s<0",tc.typ,[]any{tc.minimum,tc.minimum,tc.minimum}))
	}
	for _, tc := range []struct{ tag, a, z, zeros, ones string }{
		{"empty","","","",""},
		{"nine","101010101","010101010","000000000","111111111"},
		{"wide",strings.Repeat("0",64)+"1",strings.Repeat("1",64)+"0",strings.Repeat("0",65),strings.Repeat("1",65)},
	} {
		table := "bw_bits_"+tc.tag
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE "+table+"(v VARBIT)",0))
		fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO "+table+" VALUES(B'"+tc.a+"'),(B'"+tc.z+"'),(NULL)",3))
		bitwiseAggregateRoutes(t,p,b,bitwiseAggregateTriple(b.PG,"SELECT bit_and(v),bit_or(v),bit_xor(v) FROM "+table,"BIT",[]any{tc.zeros,tc.ones,tc.ones}))
	}
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE bw_width(k INT,v \"bit\")",0))
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO bw_width VALUES(1,B'0'),(2,B'1'),(3,B'01'),(4,NULL)",4))
	for _, name := range []string{"bit_and","bit_or","bit_xor"} {
		q := "SELECT "+name+"(v ORDER BY k) FROM bw_width"
		for _, prepared := range []bool{false,true} {
			s := fixture.ErrorDiagnostic(q,bitwiseAggregateWidthDiagnostic(name))
			s.Prepared, s.SelectRows, s.ErrorStage = prepared,true,"execute"
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
		}
	}
	// Absorbing zero/one and the third unequal width still evaluate every row.
	// Discarded FILTER rows and NULL never enter the transition.
	bitwiseAggregateRoutes(t,p,b,bitwiseAggregateTriple(b.PG,"SELECT bit_and(v) FILTER(WHERE k<3),bit_or(v) FILTER(WHERE k<3),bit_xor(v) FILTER(WHERE k<3) FROM bw_width","BIT",[]any{"0","1","1"}))
	bitwiseAggregateRoutes(t,p,b,bitwiseAggregateTriple(b.PG,"SELECT bit_and(v),bit_or(v),bit_xor(v) FROM bw_width WHERE k>=3","BIT",[]any{"01","01","01"}))
	for _, name := range []string{"bit_and","bit_or","bit_xor"} {
		for _, modifier := range []struct{ sql, keyType string }{
			{"DISTINCT f",""},{"DISTINCT v",""},
			{"f ORDER BY f","bit"},{"v ORDER BY v","bit varying"},
			{"DISTINCT f ORDER BY f","bit"},{"DISTINCT v ORDER BY v","bit varying"},
		} {
			q := "SELECT "+name+"("+modifier.sql+") FROM bw WHERE k='fold'"
			if b.PG {
				want := map[string]string{"bit_and":"00000","bit_or":"11111","bit_xor":"01010"}[name]
				bitwiseAggregateRoutes(t,p,b,fixture.Select(q,[]string{name},[]string{"BIT"},[][]any{{want}}))
			} else {
				d := fixture.ControlsReferenceDiagnostic("42883","could not identify an equality operator for type bit")
				position := strings.Index(q,"DISTINCT ")+len("DISTINCT ")+1
				if modifier.keyType!="" {
					d = fixture.ControlsReferenceDiagnostic("0A000","aggregate ordering is unsupported for type "+modifier.keyType)
					position = strings.Index(q,"ORDER BY ")+len("ORDER BY ")+1
				}
				d.Position = strconv.Itoa(position)
				for _, prepared := range []bool{false,true} {
					for _, query := range []bool{false,true} {
						s := fixture.ErrorDiagnostic(q,d)
						s.SelectRows,s.Prepared,s.ErrorStage = query,prepared,"execute"
						if prepared { s.ErrorStage="prepare" }
						fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
					}
				}
			}
		}
	}
	q := "SELECT bit_and(i) OVER (),bit_or(i) OVER (),bit_xor(i) OVER () FROM bw WHERE k='fold'"
	if b.PG {
		bitwiseAggregateRoutes(t,p,b,fixture.Select(q,[]string{"bit_and","bit_or","bit_xor"},[]string{"INT4","INT4","INT4"},[][]any{{int64(0),int64(15),int64(5)},{int64(0),int64(15),int64(5)},{int64(0),int64(15),int64(5)},{int64(0),int64(15),int64(5)}}))
	} else {
		d := fixture.ControlsReferenceDiagnostic("0A000","window function provider is unsupported")
		d.Position="8"
		for _, prepared := range []bool{false,true} {
			for _, query := range []bool{false,true} {
				s := fixture.ErrorDiagnostic(q,d)
				s.SelectRows,s.Prepared,s.ErrorStage = query,prepared,"execute"
				if prepared { s.ErrorStage="prepare" }
				fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
			}
		}
	}
}

func TestReferenceBitwiseAggregateBoundaries(t *testing.T) {
	bitwiseAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		bitwiseAggregateSetup(t,p,b)
		bitwiseAggregateBoundaries(t,p,b)
	})
}
