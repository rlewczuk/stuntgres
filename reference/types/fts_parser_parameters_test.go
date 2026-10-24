package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSParserParameters(t *testing.T) {
	for i, call := range []string{
		"ts_parse('default','Word')",
		"pg_catalog.ts_parse('default','Word')",
		"ts_parse(parser_name => 'default',txt => 'Word')",
		"ts_parse(txt => 'Word',parser_name => 'default')",
		"ts_parse(parser_oid => 3722::oid,txt => 'Word')",
		"ts_parse(txt => 'Word',parser_oid => 3722::oid)",
	} {
		t.Run(fmt.Sprintf("named_%d",i),func(t *testing.T) {
			ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				ftsParserSelect(t,p,b,"SELECT * FROM "+call,[]string{"tokid","token"},[]string{"INT4","TEXT"},[][]any{{int64(1),"Word"}})
			})
		})
	}
	for i, call := range []string{"ts_token_type(parser_name => 'default')","ts_token_type(parser_oid => 3722::oid)"} {
		t.Run(fmt.Sprintf("descriptor_named_%d",i),func(t *testing.T) {
			ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				ftsParserSelect(t,p,b,"SELECT * FROM "+call+" ORDER BY tokid",[]string{"tokid","alias","description"},[]string{"INT4","TEXT","TEXT"},ftsParserDescriptors())
			})
		})
	}
	for _, tc := range []struct { name, query string; args []any; rows [][]any }{
		{"unknown", "SELECT * FROM ts_parse($1,$2)", []any{"default","Word"}, [][]any{{int64(1),"Word"}}},
		{"bytes", "SELECT * FROM ts_parse($1::text,$2::text)", []any{[]byte("default"),[]byte("Word")}, [][]any{{int64(1),"Word"}}},
		{"empty_bytes", "SELECT * FROM ts_parse($1::text,$2::text)", []any{[]byte("default"),[]byte{}}, nil},
		{"nil_bytes", "SELECT * FROM ts_parse($1::text,$2::text)", []any{[]byte("default"),[]byte(nil)}, nil},
		{"null", "SELECT * FROM ts_parse($1,$2)", []any{nil,"Word"}, nil},
	} {
		t.Run(tc.name,func(t *testing.T) {
			ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				ftsParserSelect(t,p,b,tc.query,[]string{"tokid","token"},[]string{"INT4","TEXT"},tc.rows,tc.args...)
			})
		})
	}
	for _, oid := range []bool{false,true} {
		t.Run(fmt.Sprintf("held_oid_%t",oid),func(t *testing.T) {
			ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				conn,err := b.DB.Conn(p.Ctx)
				if err != nil { t.Fatal(err) }
				defer conn.Close()
				query := "SELECT * FROM ts_parse($1::text,$2::text)"
				var valid,invalid any = "default","missing"
				code,message := "42704",`text search parser "missing" does not exist`
				if oid {
					query = "SELECT * FROM ts_parse($1::oid,$2::text)"
					valid,invalid = int64(3722),int64(0)
					code,message = "XX000","cache lookup failed for text search parser 0"
				}
				stmt,err := conn.PrepareContext(p.Ctx,query)
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				held := fixture.HeldStatement{Executor:conn,Statement:stmt,SQL:query}
				for range 2 {
					for _, tc := range []struct { parser,document any; rows [][]any; fails bool }{
						{valid,"Word",[][]any{{int64(1),"Word"}},false},
						{valid,"",nil,false}, {nil,"Word",nil,false}, {invalid,nil,nil,false},
						{invalid,"Word",nil,true},
						{valid,"Again",[][]any{{int64(1),"Again"}},false},
					} {
						s := fixture.Select(query,[]string{"tokid","token"},[]string{"INT4","TEXT"},tc.rows,tc.parser,tc.document)
						if tc.fails {
							s = fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic(code,message),tc.parser,tc.document)
							s.SelectRows,s.ErrorStage = true,"execute"
						}
						fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
					}
				}
			})
		})
	}
	for _, cast := range []string{"", "::text", "::oid"} {
		t.Run("held_descriptors"+cast,func(t *testing.T) {
			ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
				query := "SELECT * FROM ts_token_type($1"+cast+") ORDER BY tokid"
				stmt,err := b.DB.PrepareContext(p.Ctx,query)
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				held := fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:query}
				var valid,invalid any = "default","missing"
				code,message := "42704",`text search parser "missing" does not exist`
				if cast == "::oid" { valid,invalid,code,message = int64(3722),int64(0),"XX000","cache lookup failed for text search parser 0" }
				for _, input := range []any{valid,nil,invalid,valid} {
					s := fixture.Select(query,[]string{"tokid","alias","description"},[]string{"INT4","TEXT","TEXT"},ftsParserDescriptors(),input)
					if input == nil { s = fixture.Select(query,[]string{"tokid","alias","description"},[]string{"INT4","TEXT","TEXT"},nil,input) }
					if input == invalid {
						s = fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic(code,message),input)
						s.SelectRows,s.ErrorStage = true,"execute"
					}
					fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
				}
			})
		})
	}
}
