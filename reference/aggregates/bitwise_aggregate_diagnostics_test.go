package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func bitwiseAggregateDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	const missingHint = "No function matches the given name and argument types. You might need to add explicit type casts."
	bitwiseAggregateInsertValuesDiagnostics(t,p,b)
	for _, name := range []string{"bit_and","bit_or","bit_xor"} {
		for _, prepared := range []bool{false,true} {
			t.Run("generated/"+name+"/"+strconv.FormatBool(prepared),func(t *testing.T) {
				q := "CREATE TABLE bw_gen(i INTEGER,g INTEGER GENERATED ALWAYS AS ("+name+"(i)) STORED)"
				d := fixture.ControlsReferenceDiagnostic("42803","aggregate functions are not allowed in column generation expressions")
				d.Position="62"
				s := fixture.ErrorDiagnostic(q,d)
				s.Prepared,s.ErrorStage=prepared,"execute"
				fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
				absent := fixture.ControlsReferenceDiagnostic("42P01",`relation "bw_gen" does not exist`)
				absent.Position="15"
				check := fixture.ErrorDiagnostic("SELECT i FROM bw_gen",absent)
				check.SelectRows,check.ErrorStage=true,"execute"
				fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,check)
			})
		}
	}
	const ambiguousHint = "Could not choose a best candidate function. You might need to add explicit type casts."
	const orderHint = "No aggregate function matches the given name and argument types. Perhaps you misplaced ORDER BY; ORDER BY must appear after all regular arguments of the aggregate."
	for _, name := range []string{"bit_and","bit_or","bit_xor"} {
		type candidate struct { q, code, message, pos, hint string; args []any }
		cases := []candidate{}
		for _, arg := range []struct{ sql, typ string }{{"TRUE","boolean"},{"'x'::text","text"},{"1.5","numeric"},{"1::double precision","double precision"},{"",""},{"*",""},{"1,2","integer, integer"}} {
			cases = append(cases, candidate{"SELECT "+name+"("+arg.sql+") FROM bw","42883","function "+name+"("+arg.typ+") does not exist","8",missingHint,nil})
		}
		for _, arg := range []string{"NULL","'101'"} {
			cases = append(cases, candidate{"SELECT "+name+"("+arg+") FROM bw","42725","function "+name+"(unknown) is not unique","8",ambiguousHint,nil})
		}
		for _, arg := range []any{nil,"101",7} {
			cases = append(cases, candidate{"SELECT "+name+"($1) FROM bw","42725","function "+name+"(unknown) is not unique","8",ambiguousHint,[]any{arg}})
		}
		cases = append(cases,
			candidate{"SELECT /*é*/ "+name+"(TRUE) FROM bw WHERE FALSE","42883","function "+name+"(boolean) does not exist","14",missingHint,nil},
			candidate{"SELECT pg_catalog."+name+"(TRUE) FROM bw","42883","function pg_catalog."+name+"(boolean) does not exist","8",missingHint,nil},
			candidate{"SELECT public."+name+"(i) FROM bw","42883","function public."+name+"(integer) does not exist","8",missingHint,nil},
			candidate{"SELECT bw_missing."+name+"(i) FROM bw","3F000",`schema "bw_missing" does not exist`,"8","",nil},
			candidate{"SELECT "+name+"(TRUE ORDER BY i,b) FROM bw","42883","function "+name+"(boolean) does not exist","8",orderHint,nil},
			candidate{"SELECT "+name+"(TRUE ORDER BY i) FROM bw","42883","function "+name+"(boolean) does not exist","8",missingHint,nil},
			candidate{"SELECT "+name+"(TRUE) OVER () FROM bw","42883","function "+name+"(boolean) does not exist","8",missingHint,nil},
			candidate{"SELECT "+name+"(NULL) OVER () FROM bw","42725","function "+name+"(unknown) is not unique","8",ambiguousHint,nil},
			candidate{"SELECT "+name+"("+strings.Repeat("1,",99)+"1) FROM bw","42883","function "+name+"("+strings.Repeat("integer, ",99)+"integer) does not exist","8",missingHint,nil},
			candidate{"SELECT bw_missing."+name+"("+strings.Repeat("1,",100)+"1) FROM bw","54023","cannot pass more than 100 arguments to a function","8","",nil},
		)
		for _, q := range []string{"SELECT "+name+"(missing) FROM bw", "SELECT bw_missing."+name+"(missing) FROM bw", "SELECT "+name+"(missing) OVER () FROM bw", "SELECT /*é*/ "+name+"(missing) FROM bw WHERE FALSE"} {
			// The schema name contains 'missing'; the child is the last occurrence.
			pos := strconv.Itoa(utf8.RuneCountInString(q[:strings.LastIndex(q,"missing")])+1)
			cases = append(cases, candidate{q,"42703",`column "missing" does not exist`,pos,"",nil})
		}
		for _, clause := range []struct{ q, context string }{
			{"SELECT i FROM bw WHERE "+name+"(i)=0","WHERE"},
			{"SELECT i FROM bw GROUP BY "+name+"(i)","GROUP BY"},
			{"SELECT bw.i FROM bw JOIN bw AS z ON "+name+"(bw.i)=0","JOIN conditions"},
			{"SELECT i FROM bw LIMIT "+name+"(i)","LIMIT"},
			{"SELECT i FROM bw OFFSET "+name+"(i)","OFFSET"},
		} {
			cases = append(cases, candidate{clause.q,"42803","aggregate functions are not allowed in "+clause.context,strconv.Itoa(strings.Index(clause.q,name)+1),"",nil})
		}
		if name == "bit_and" {
			cases = append(cases,
				candidate{"SELECT bit_and(TRUE) FILTER (WHERE 1) FROM bw","42804","argument of FILTER must be type boolean, not type integer","36","",nil},
				candidate{"SELECT bit_and(bit_or(i)) FROM bw","42803","aggregate function calls cannot be nested","16","",nil},
				candidate{"SELECT bit_and(count(*)) FROM bw","42803","aggregate function calls cannot be nested","16","",nil},
			)
		}
		for index, tc := range cases {
			t.Run(name+"/"+strconv.Itoa(index)+"/"+tc.q, func(t *testing.T) {
				for _, prepared := range []bool{false,true} {
					for _, query := range []bool{false,true} {
						t.Run(strconv.FormatBool(prepared)+"/query="+strconv.FormatBool(query), func(t *testing.T) {
							d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
							d.Position, d.Hint = tc.pos, tc.hint
							s := fixture.ErrorDiagnostic(tc.q,d,tc.args...)
							s.Prepared, s.SelectRows, s.ErrorStage = prepared, query, "execute"
							if prepared { s.ErrorStage = "prepare" }
							fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
						})
					}
				}
			})
		}
	}
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE bw_errors(v \"bit\")",0))
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO bw_errors VALUES(B'1'),(B'01'),(NULL)",3))
	for _, name := range []string{"bit_and","bit_or","bit_xor"} {
		t.Run(name+"/runtime-width",func(t *testing.T) {
			q := "SELECT "+name+"(v) FROM bw_errors"
			for _, prepared := range []bool{false,true} {
				for _, query := range []bool{false,true} {
					s := fixture.ErrorDiagnostic(q,bitwiseAggregateWidthDiagnostic(name))
					s.Prepared, s.SelectRows, s.ErrorStage = prepared, query, "execute"
					fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
				}
			}
			stmt, err := b.DB.PrepareContext(p.Ctx,q)
			if err != nil { t.Fatal("width must not fail Prepare:",err) }
			defer stmt.Close()
			held := fixture.HeldStatement{b.DB,stmt,q}
			for _, query := range []bool{false,true} {
				s := fixture.ErrorDiagnostic(q,bitwiseAggregateWidthDiagnostic(name))
				s.SelectRows, s.ErrorStage = query, "execute"
				fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
			}
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("DELETE FROM bw_errors",3))
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO bw_errors VALUES(B'01'),(NULL)",2))
			s := fixture.Select(q,[]string{name},[]string{"BIT"},[][]any{{"01"}})
			fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("DELETE FROM bw_errors",2))
			fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO bw_errors VALUES(B'1'),(B'01'),(NULL)",3))
		})
	}
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer conn.Close()
	defer conn.ExecContext(p.Ctx,"RESET search_path")
	fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("CREATE DOMAIN bw_boolean AS BOOLEAN",0))
	fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("CREATE TABLE bw_diag_domain(v bw_boolean)",0))
	fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("INSERT INTO bw_diag_domain VALUES(TRUE),(NULL)",2))
	for _, path := range []string{"public","pg_catalog"} {
		fixture.RunStep(t,p.Ctx,conn,b.Recorder,fixture.Exec("SET search_path TO "+path,0))
		domain := "bw_boolean"
		if path=="pg_catalog" { domain="public.bw_boolean" }
		for _, name := range []string{"bit_and","bit_or","bit_xor"} {
			t.Run(path+"/"+name+"/domain",func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42883","function "+name+"("+domain+") does not exist")
				d.Position,d.Hint = "8",missingHint
				for _, prepared := range []bool{false,true} {
					for _, query := range []bool{false,true} {
						s := fixture.ErrorDiagnostic("SELECT "+name+"(v) FROM public.bw_diag_domain WHERE FALSE",d)
						s.Prepared,s.SelectRows,s.ErrorStage = prepared,query,"execute"
						if prepared { s.ErrorStage="prepare" }
						fixture.RunStep(t,p.Ctx,conn,b.Recorder,s)
					}
				}
			})
		}
	}
}

func TestReferenceBitwiseAggregateDiagnostics(t *testing.T) {
	bitwiseAggregateScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
		bitwiseAggregateSetup(t,p,b)
		bitwiseAggregateDiagnostics(t,p,b)
	})
}

func bitwiseAggregateInsertValuesDiagnostics(t *testing.T,p *fixture.Pair,b fixture.Backend) {
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("CREATE TABLE bw_values(i INTEGER)",0))
	fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Exec("INSERT INTO bw_values VALUES(99)",1))
	const signatureHint="No function matches the given name and argument types. You might need to add explicit type casts."
	for _, name:=range []string{"bit_and","bit_or","bit_xor"} {
		for _, tc:=range []struct{ tag,q,code,message,marker,hint string }{
			{"valid","INSERT INTO bw_values VALUES("+name+"(1))","42803","aggregate functions are not allowed in VALUES",name,""},
			{"late_valid","INSERT INTO bw_values VALUES(2),("+name+"(1))","42803","aggregate functions are not allowed in VALUES",name,""},
			{"unicode_valid","/*é*/ INSERT INTO bw_values VALUES("+name+"(1))","42803","aggregate functions are not allowed in VALUES",name,""},
			{"child","INSERT INTO bw_values VALUES("+name+"(missing))","42703",`column "missing" does not exist`,"missing",""},
			{"signature","INSERT INTO bw_values VALUES("+name+"(TRUE))","42883","function "+name+"(boolean) does not exist",name,signatureHint},
			{"filter","INSERT INTO bw_values VALUES("+name+"(TRUE) FILTER (WHERE 1))","42804","argument of FILTER must be type boolean, not type integer","1",""},
		} {
			for _, prepared:=range []bool{false,true} {
				for _, query:=range []bool{false,true} {
					t.Run("insert_values/"+name+"/"+tc.tag+"/prepared="+strconv.FormatBool(prepared)+"/query="+strconv.FormatBool(query),func(t *testing.T) {
						// Assert destination state even if a diagnostic or route assertion
						// terminates this subtest. A valid first VALUES row publishes none.
						defer fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,fixture.Select("SELECT i FROM bw_values",[]string{"i"},[]string{"INT4"},[][]any{{int64(99)}}))
						d:=fixture.ControlsReferenceDiagnostic(tc.code,tc.message)
						d.Position=strconv.Itoa(utf8.RuneCountInString(tc.q[:strings.Index(tc.q,tc.marker)])+1)
						d.Hint=tc.hint
						s:=fixture.ErrorDiagnostic(tc.q,d)
						s.Prepared,s.SelectRows,s.ErrorStage=prepared,query,"execute"
						if prepared { s.ErrorStage="prepare" }
						fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
					})
				}
			}
		}
	}
}
