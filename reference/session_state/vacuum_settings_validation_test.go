package reference_test

import (
 "strconv"
 "testing"
 "stuntgres/reference/fixture"
)

type vacuumMutation struct { name,input,show,raw string }
var vacuumMutations = []vacuumMutation{
 {"vacuum_cost_delay","1500us","1500us","1.5"},
 {"vacuum_cost_limit","201","201","201"},
 {"vacuum_cost_page_dirty","21","21","21"},
 {"vacuum_cost_page_hit","3","3","3"},
 {"vacuum_cost_page_miss","4","4","4"},
 {"vacuum_failsafe_age","0","0","0"},
 {"vacuum_freeze_min_age","1000000000","1000000000","1000000000"},
 {"vacuum_freeze_table_age","2000000000","2000000000","2000000000"},
 {"vacuum_max_eager_freeze_failure_rate","0.5","0.5","0.5"},
 {"vacuum_multixact_failsafe_age","0","0","0"},
 {"vacuum_multixact_freeze_min_age","1000000000","1000000000","1000000000"},
 {"vacuum_multixact_freeze_table_age","2000000000","2000000000","2000000000"},
 {"vacuum_truncate","off","off","off"},
}

func (s *physicalCase) vacuumAssign(m vacuumMutation,prepared,bound bool) {
 step:=fixture.Exec("SET "+m.name+"="+physicalLiteral(m.input),0)
 if bound { step=fixture.Select("SELECT set_config($1,$2,false)",[]string{"set_config"},[]string{"TEXT"},[][]any{{m.show}},m.name,m.input) }
 step.Prepared=prepared; s.run(step)
 s.vacuumValue(m.name,m.show,m.raw,"session",prepared)
}

func TestReferenceVacuumSettingsValidation(t *testing.T) {
 for _,m:=range vacuumMutations {
  t.Run(m.name,func(t *testing.T) {
   physicalCases(t,nil,func(s *physicalCase) {
    peer:=s.lockPeer(); d:=vacuumDefinition(m.name)
    for _,prepared:=range []bool{false,true} {
     valid:=[]vacuumMutation{m}
     invalid:=[]struct{input string; diagnostic fixture.Diagnostic}{}
     if d.kind=="bool" {
      for _,input:=range []string{"on","off","TRUE","FALSE","yes","no","1","0","Tr","f","y","n"} {
       show:="off"; if input=="on" || input=="TRUE" || input=="yes" || input=="1" || input=="Tr" || input=="y" { show="on" }; valid=append(valid,vacuumMutation{d.name,input,show,show})
      }
      invalid=append(invalid,struct{input string; diagnostic fixture.Diagnostic}{"o",physicalDiagnostic("22023",`parameter "vacuum_truncate" requires a Boolean value`)})
     } else {
      for _,input:=range []string{d.min,d.max} { show:=input; if d.unit=="ms" && input!="0" { show+="ms" }; valid=append(valid,vacuumMutation{d.name,input,show,input}) }
      lo,_:=strconv.ParseFloat(d.min,64); hi,_:=strconv.ParseFloat(d.max,64)
      for _,n:=range []float64{lo-1,hi+1} { input:=strconv.FormatFloat(n,'f',-1,64); invalid=append(invalid,struct{input string; diagnostic fixture.Diagnostic}{input,vacuumBounds(d,input)}) }
      invalid=append(invalid,struct{input string; diagnostic fixture.Diagnostic}{"bogus",vacuumInvalid(d.name,"bogus")})
      if d.kind=="integer" { e:=vacuumInvalid(d.name,"2147483648"); e.Hint="Value exceeds integer range."; invalid=append(invalid,struct{input string; diagnostic fixture.Diagnostic}{"2147483648",e}) }
     }
     for _,v:=range valid { for _,bound:=range []bool{false,true} { s.vacuumAssign(v,prepared,bound) } }
     s.vacuumAssign(m,prepared,false)
     for _,bad:=range invalid {
      s.run(physicalError("SET "+m.name+"="+physicalLiteral(bad.input),bad.diagnostic,prepared,false))
      s.vacuumValue(m.name,m.show,m.raw,"session",prepared)
      s.run(physicalError("SELECT set_config($1,$2,false)",bad.diagnostic,prepared,true,m.name,bad.input))
      s.vacuumValue(m.name,m.show,m.raw,"session",prepared)
     }
     peer.vacuumValue(d.name,d.show,d.raw,"default",prepared)
     s.run(fixture.Exec("RESET "+d.name,0)); s.vacuumValue(d.name,d.show,d.raw,"default",prepared)
    }
   })
  })
 }
 t.Run("numeric-syntax-and-migrated-units",func(t *testing.T) {
  physicalCases(t,nil,func(s *physicalCase) {
   for _,prepared:=range []bool{false,true} {
    for _,m:=range []vacuumMutation{
     {"vacuum_cost_delay","1500us","1500us","1.5"},
     {"vacuum_cost_delay","0.0001","0.1us","0.0001"},
     {"vacuum_cost_delay","0.0001ms","0","0"},
     {"vacuum_cost_delay","1.5","1500us","1.5"},
     {"vacuum_cost_delay","100ms","100ms","100"},
     {"vacuum_cost_limit","2.5","2","2"},{"vacuum_cost_limit","3.5","4","4"},{"vacuum_cost_limit","010","8","8"},{"vacuum_cost_limit","0xA","10","10"},
     {"vacuum_max_eager_freeze_failure_rate","0","0","0"},{"vacuum_max_eager_freeze_failure_rate","0.5","0.5","0.5"},{"vacuum_max_eager_freeze_failure_rate","1","1","1"},
    } { for _,bound:=range []bool{false,true} { s.vacuumAssign(m,prepared,bound) } }
    for _,tc:=range []struct{name,input,parsed,hint string}{
     {"vacuum_cost_delay","101ms","101",""},{"vacuum_cost_delay","-1ms","-1",""},
     {"vacuum_cost_delay","NaN","",""},{"vacuum_cost_delay","Infinity","Infinity",""},
     {"vacuum_cost_delay","1e999","",""},{"vacuum_cost_delay","1e-999","",""},
     {"vacuum_cost_delay","1MS","",`Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`},
     {"vacuum_cost_limit","1ms","",""},
     {"vacuum_max_eager_freeze_failure_rate","1.1","1.1",""},{"vacuum_max_eager_freeze_failure_rate","-0.1","-0.1",""},
     {"vacuum_max_eager_freeze_failure_rate","NaN","",""},{"vacuum_max_eager_freeze_failure_rate","Infinity","Infinity",""},
    } {
     d:=vacuumDefinition(tc.name)
     s.run(fixture.Exec("RESET "+d.name,0))
     e:=vacuumInvalid(d.name,tc.input); if tc.parsed!="" { e=vacuumBounds(d,tc.parsed) }; e.Hint=tc.hint
     s.run(physicalError("SET "+tc.name+"="+physicalLiteral(tc.input),e,prepared,false)); s.vacuumValue(d.name,d.show,d.raw,"default",prepared)
     s.run(physicalError("SELECT set_config($1,$2,false)",e,prepared,true,tc.name,tc.input)); s.vacuumValue(d.name,d.show,d.raw,"default",prepared)
    }
    s.run(fixture.Exec("RESET ALL",0))
   }
  })
 })
}
