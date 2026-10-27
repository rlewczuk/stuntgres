package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePlannerCostsValidation(t *testing.T) {
	for _,d:=range plannerCostSettings {
		t.Run(d.name,func(t *testing.T) {
			physicalCases(t,nil,func(s *physicalCase) {
				for _,prepared:=range []bool{false,true} {
					valid:=[]struct{input,show,raw string}{{"0","0","0"},{"1.25","1.25","1.25"},{"1e-3","0.001","0.001"},{"1.7976931348623157e308","1.79769e+308","1.79769e+308"}}
					if d.min=="-1" { valid=append(valid,struct{input,show,raw string}{"-1","-1","-1"},struct{input,show,raw string}{"-0.5","-0.5","-0.5"}) }
					if d.kind=="integer" {
						valid=[]struct{input,show,raw string}{{"1MB","1MB","128"},{"1GB","1GB","131072"},{"12kB","16kB","2"},{"20kB","16kB","2"}}
						if d.min=="0" { valid=append(valid,struct{input,show,raw string}{"0","0","0"},struct{input,show,raw string}{"715827882","5726623056kB","715827882"})
						} else { valid=append(valid,struct{input,show,raw string}{"1","8kB","1"},struct{input,show,raw string}{"2147483647","17179869176kB","2147483647"}) }
					}
					for _,v:=range valid {
						s.plannerCostSet(d.name,v.input,prepared); s.plannerCostValue(d,v.show,v.raw,"session",prepared)
						for _,bound:=range []bool{false,true} {
							s.plannerCostSet(d.name,"3",prepared)
							q:="SELECT set_config("+physicalLiteral(d.name)+","+physicalLiteral(v.input)+",false)"; args:=[]any(nil)
							if bound { q="SELECT set_config($1,$2,false)"; args=[]any{d.name,v.input} }
							step:=fixture.Select(q,[]string{"set_config"},[]string{"TEXT"},[][]any{{v.show}},args...); step.Prepared=prepared; s.run(step)
							s.plannerCostValue(d,v.show,v.raw,"session",prepared)
						}
					}
					s.plannerCostSet(d.name,"2",prepared)
					show:="2"; if d.kind=="integer" { show="16kB" }
					invalid:=[]struct{input string; diagnostic fixture.Diagnostic}{{"bogus",resourceInvalid(d.name,"bogus")},{"NaN",resourceInvalid(d.name,"NaN")},{"1e999",resourceInvalid(d.name,"1e999")},{"Infinity",plannerCostBounds(d,"Infinity")}}
					negative:="-1"; if d.min=="-1" { negative="-2" }
					invalid=append(invalid,struct{input string; diagnostic fixture.Diagnostic}{negative,plannerCostBounds(d,negative)})
					if d.kind=="integer" {
						invalid=[]struct{input string; diagnostic fixture.Diagnostic}{{"bogus",resourceInvalid(d.name,"bogus")},{"-1",plannerCostBounds(d,"-1")}}
						unit:=resourceInvalid(d.name,"1mb"); unit.Hint=`Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`
						integer:=resourceInvalid(d.name,"2147483648"); integer.Hint="Value exceeds integer range."
						invalid=append(invalid,struct{input string; diagnostic fixture.Diagnostic}{"1mb",unit},struct{input string; diagnostic fixture.Diagnostic}{"2147483648",integer})
						value:="715827883"; if d.min=="1" { value="0" }
						invalid=append(invalid,struct{input string; diagnostic fixture.Diagnostic}{value,plannerCostBounds(d,value)})
					}
					for _,v:=range invalid {
						s.run(physicalError("SET "+d.name+"="+physicalLiteral(v.input),v.diagnostic,prepared,false))
						s.plannerCostValue(d,show,"2","session",prepared)
						s.run(physicalError("SELECT set_config($1,$2,false)",v.diagnostic,prepared,true,d.name,v.input))
						s.plannerCostValue(d,show,"2","session",prepared)
					}
					s.run(fixture.Exec("RESET "+d.name,0)); s.plannerCostValue(d,d.show,d.boot,"default",prepared)
				}
				if d.name=="random_page_cost" { s.plannerCostSet("seq_page_cost","1",false); s.plannerCostSet(d.name,"0.5",false); s.plannerCostValue(d,"0.5","0.5","session",true) }
			})
		})
	}
}
