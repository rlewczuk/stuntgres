package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceGeqoValidation(t *testing.T) {
 for _, d := range geqoSettings { t.Run(d.name,func(t *testing.T) {
  physicalCases(t,nil,func(s *physicalCase) {
   for _, prepared := range []bool{false,true} {
    for _, v := range geqoValidValues(d) {
     // Preparing a setter must not assign a value.
     s.plannerCostSet(d.name,d.a,false)
     setter,err := s.conn.PrepareContext(s.p.Ctx,"SET "+d.name+"="+physicalLiteral(v.input)); if err != nil { s.t.Fatal(err) }
     s.geqoValue(d,d.a,"session",prepared); setter.Close()
     s.plannerCostSet(d.name,v.input,prepared); s.geqoValue(d,v.output,"session",prepared)
     for _, bound := range []bool{false,true} {
      s.plannerCostSet(d.name,d.a,prepared)
      query := "SELECT set_config("+physicalLiteral(d.name)+","+physicalLiteral(v.input)+",false)"; args := []any(nil)
      if bound { query = "SELECT set_config($1,$2,false)"; args = []any{d.name,v.input} }
      step := fixture.Select(query,[]string{"set_config"},[]string{"TEXT"},[][]any{{v.output}},args...); step.Prepared = prepared; s.run(step)
      s.geqoValue(d,v.output,"session",prepared)
     }
    }
    // Equal-to-boot assignments still have session provenance.
    s.plannerCostSet(d.name,d.boot,prepared); s.geqoValue(d,d.boot,"session",prepared)
    for _, v := range geqoInvalidValues(d) {
     s.run(physicalError("SET "+d.name+"="+physicalLiteral(v.input),v.diagnostic,prepared,false))
     s.geqoValue(d,d.boot,"session",prepared)
     for _, bound := range []bool{false,true} {
      query := "SELECT set_config("+physicalLiteral(d.name)+","+physicalLiteral(v.input)+",false)"; args := []any(nil)
      if bound { query = "SELECT set_config($1,$2,false)"; args = []any{d.name,v.input} }
      s.run(physicalError(query,v.diagnostic,prepared,true,args...)); s.geqoValue(d,d.boot,"session",prepared)
     }
    }
    s.run(fixture.Exec("RESET "+d.name,0)); s.geqoValue(d,d.boot,"default",prepared)
   }
  })
 }) }
}
