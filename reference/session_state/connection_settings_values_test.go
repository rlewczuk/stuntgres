package reference_test

import (
 "fmt"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

type connectionValue struct { input, display string }

func connectionValid(tc connectionSetting) []connectionValue {
 switch tc.name {
 case "client_connection_check_interval": return []connectionValue{{"1500ms","1500ms"},{"1s","1s"},{"1min","1min"},{"0","0"},{"-0","0"}}
 case "md5_password_warnings": return []connectionValue{{"false","off"},{"no","off"},{"0","off"},{"true","on"},{"yes","on"},{"1","on"}}
 case "password_encryption": return []connectionValue{{"MD5","md5"},{"SCRAM-SHA-256","scram-sha-256"}}
 case "scram_iterations": return []connectionValue{{"1","1"},{"8192","8192"},{"2147483647","2147483647"},{"2.5","2"},{"3.5","4"}}
 case "tcp_keepalives_count": return []connectionValue{{"1","1"},{"7","7"}}
 case "tcp_keepalives_idle", "tcp_keepalives_interval": return []connectionValue{{"60s","60"},{"1min","60"},{"120s","120"}}
 case "tcp_user_timeout": return []connectionValue{{"1000ms","1000"},{"1s","1000"},{"1500ms","1500"}}
 }
 panic("not a mutable connection setting: "+tc.name)
}

func connectionAlternate(tc connectionSetting) connectionValue {
 switch tc.name {
 case "client_connection_check_interval": return connectionValue{"1s","1s"}
 case "md5_password_warnings": return connectionValue{"off","off"}
 case "password_encryption": return connectionValue{"md5","md5"}
 case "scram_iterations": return connectionValue{"8192","8192"}
 case "tcp_keepalives_count": return connectionValue{"7","7"}
 case "tcp_keepalives_idle", "tcp_keepalives_interval": return connectionValue{"60s","60"}
 case "tcp_user_timeout": return connectionValue{"1000ms","1000"}
 }
 panic("not a mutable connection setting: "+tc.name)
}

type connectionInvalid struct { input string; diagnostic fixture.Diagnostic }

func connectionInvalidValues(tc connectionSetting) []connectionInvalid {
 invalid := func(value,hint string) connectionInvalid {
  d := physicalDiagnostic("22023",fmt.Sprintf("invalid value for parameter %q: %q",tc.name,value)); d.Hint = hint
  return connectionInvalid{value,d}
 }
 if tc.kind == "bool" {
  d := physicalDiagnostic("22023",fmt.Sprintf("parameter %q requires a Boolean value",tc.name))
  return []connectionInvalid{{"bogus",d},{"o",d}}
 }
 if tc.kind == "enum" { return []connectionInvalid{invalid("bogus","Available values: md5, scram-sha-256."),invalid("","Available values: md5, scram-sha-256.")} }
 suffix := ""; if tc.unit != "" { suffix = " "+tc.unit }
 lower := "-1"; if tc.name == "scram_iterations" { lower = "0" }
 rangeError := physicalDiagnostic("22023",fmt.Sprintf("%s%s is outside the valid range for parameter %q (%s%s .. %s%s)",lower,suffix,tc.name,tc.min,suffix,tc.max,suffix))
 hint := ""; if tc.unit != "" { hint = `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".` }
 out := []connectionInvalid{invalid("bogus",""),{lower,rangeError},invalid("2147483648","Value exceeds integer range."),invalid("-2147483649","Value exceeds integer range."),invalid("1bogus",hint)}
 if tc.unit == "" { out = append(out,invalid("1s","")) }
 return out
}

func TestReferenceConnectionSettingsUserValues(t *testing.T) {
 for _, tc := range connectionSettings {
  if tc.context != "user" { continue }
  t.Run(tc.name,func(t *testing.T) {
   connectionCases(t,[]string{"connection_values_plain"},func(s *physicalCase) {
    s.run(fixture.Exec("CREATE ROLE connection_values_plain LOGIN PASSWORD 'role-core-fixture'",0))
    ordinary := s.login("connection_values_plain")
    initial := connectionInitial(tc,s.pg)
    for _, prepared := range []bool{false,true} {
     for _, value := range connectionValid(tc) {
      query := "SET \""+strings.ToUpper(tc.name)+"\"="+physicalLiteral(value.input)
      stmt,err := ordinary.conn.PrepareContext(s.p.Ctx,query); if err != nil { s.t.Fatal(err) }
      // Merely preparing a mutation cannot change the current value.
      ordinary.connectionRead(tc,initial,false)
      stmt.Close()
      step := fixture.Exec(query,0); step.Prepared = prepared; ordinary.run(step)
      ordinary.connectionRead(tc,value.display,prepared)
      ordinary.connectionMetadata(tc,value.display,"session",prepared)
      s.connectionRead(tc,initial,prepared)
      for _, reset := range []string{"RESET "+tc.name,"SET "+tc.name+"=DEFAULT"} {
       step := fixture.Exec(reset,0); step.Prepared = prepared; ordinary.run(step)
       ordinary.connectionRead(tc,initial,prepared)
      }
      for _, bound := range []bool{false,true} {
       query := "SELECT set_config("+physicalLiteral(tc.name)+","+physicalLiteral(value.input)+",false)"
       var args []any
       if bound { query = "SELECT set_config($1,$2,$3)"; args = []any{strings.ToUpper(tc.name),value.input,false} }
       held,err := ordinary.conn.PrepareContext(s.p.Ctx,query)
       if err != nil { s.t.Fatal("setting function must prepare without assignment",query,err) }
       ordinary.connectionRead(tc,initial,false)
       held.Close()
       step := fixture.Select(query,[]string{"set_config"},[]string{"TEXT"},[][]any{{value.display}},args...); step.Prepared = prepared; ordinary.run(step)
       ordinary.connectionRead(tc,value.display,prepared)
       step = fixture.Exec("SELECT set_config($1,$2,$3)",1,tc.name,value.input,false); step.Prepared = prepared; ordinary.run(step)
       step = fixture.Select("SELECT set_config($1,$2,$3)",[]string{"set_config"},[]string{"TEXT"},[][]any{{initial}},tc.name,nil,false); step.Prepared = prepared; ordinary.run(step)
      }
     }
     alternate := connectionAlternate(tc)
     ordinary.run(fixture.Exec("SET "+tc.name+"="+physicalLiteral(alternate.input),0))
     for _, bad := range connectionInvalidValues(tc) {
      ordinary.run(physicalError("SET "+tc.name+"="+physicalLiteral(bad.input),bad.diagnostic,prepared,false))
      ordinary.connectionRead(tc,alternate.display,prepared)
      ordinary.connectionMetadata(tc,alternate.display,"session",prepared)
      ordinary.run(physicalError("SELECT set_config($1,$2,$3)",bad.diagnostic,prepared,true,tc.name,bad.input,false))
      ordinary.connectionRead(tc,alternate.display,prepared)
     }
     ordinary.run(physicalError("SET "+tc.name+"='a','b'",physicalDiagnostic("22023","SET "+tc.name+" takes only one argument"),prepared,false))
     ordinary.run(physicalError("SELECT set_config(NULL,'x',false)",physicalDiagnostic("22004","SET requires parameter name"),prepared,true))
     ordinary.run(fixture.Exec("RESET ALL",0))
     ordinary.connectionRead(tc,initial,prepared)
     ordinary.connectionMetadata(tc,initial,"default",prepared)
    }
   })
  })
 }
}
