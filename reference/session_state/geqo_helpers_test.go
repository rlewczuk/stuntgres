package reference_test

import (
 "fmt"
 "strings"
 "stuntgres/reference/fixture"
)

// Literal PostgreSQL expectations, independent of native descriptors.
type geqoSetting struct { name, kind, boot, min, max, description, extra, a, b string }
var geqoSettings = []geqoSetting{
	{"geqo", "bool", "on", "", "", "Enables genetic query optimization.", "This algorithm attempts to do planning without exhaustive searching.", "off", "on"},
	{"geqo_effort", "integer", "5", "1", "10", "GEQO: effort is used to set the default for other GEQO parameters.", "", "1", "10"},
	{"geqo_generations", "integer", "0", "0", "2147483647", "GEQO: number of iterations of the algorithm.", "0 means use a suitable default value.", "1", "2"},
	{"geqo_pool_size", "integer", "0", "0", "2147483647", "GEQO: number of individuals in the population.", "0 means use a suitable default value.", "1", "2"},
	{"geqo_seed", "real", "0", "0", "1", "GEQO: seed for random path selection.", "", "0.125", "1"},
	{"geqo_selection_bias", "real", "2", "1.5", "2", "GEQO: selective pressure within the population.", "", "1.5", "1.75"},
	{"geqo_threshold", "integer", "12", "2", "2147483647", "Sets the threshold of FROM items beyond which GEQO is used.", "", "2", "3"},
}

func geqoNames() string {
 names := make([]string, len(geqoSettings))
 for i, d := range geqoSettings { names[i] = physicalLiteral(d.name) }
 return strings.Join(names, ",")
}

func (s *physicalCase) geqoValue(d geqoSetting, value, source string, prepared bool) {
 s.resourceValue(d.name, value, value, source, prepared)
 step := fixture.Select("SELECT boot_val,reset_val FROM pg_settings WHERE name=$1", []string{"boot_val","reset_val"}, []string{"TEXT","TEXT"}, [][]any{{d.boot,d.boot}}, d.name)
 step.Prepared = prepared
 s.run(step)
}

func geqoBounds(d geqoSetting, value string) fixture.Diagnostic {
 return physicalDiagnostic("22023", fmt.Sprintf("%s is outside the valid range for parameter %q (%s .. %s)", value,d.name,d.min,d.max))
}

type geqoValid struct { input, output string }
func geqoValidValues(d geqoSetting) []geqoValid {
 switch d.name {
 case "geqo": return []geqoValid{{"on","on"},{"true","on"},{"yes","on"},{"1","on"},{"TRUE","on"},{"t","on"},{"y","on"},{"off","off"},{"false","off"},{"no","off"},{"0","off"},{"f","off"},{"n","off"}}
 case "geqo_threshold": return []geqoValid{{"2","2"},{"12","12"},{"2147483647","2147483647"},{"2.5","2"},{"3.5","4"},{"010","8"},{"0xA","10"}}
 case "geqo_effort": return []geqoValid{{"1","1"},{"5","5"},{"10","10"}}
 case "geqo_pool_size": return []geqoValid{{"0","0"},{"1","1"},{"2","2"},{"2147483647","2147483647"}}
 case "geqo_generations": return []geqoValid{{"0","0"},{"1","1"},{"2147483647","2147483647"}}
 case "geqo_selection_bias": return []geqoValid{{"1.5","1.5"},{"1.75","1.75"},{"2","2"}}
 case "geqo_seed": return []geqoValid{{"0","0"},{"0.125","0.125"},{"1","1"}}
 }
 panic(d.name)
}

type geqoInvalid struct { input string; diagnostic fixture.Diagnostic }
func geqoInvalidValues(d geqoSetting) []geqoInvalid {
 if d.name == "geqo" {
  diagnostic := physicalDiagnostic("22023", `parameter "geqo" requires a Boolean value`)
  return []geqoInvalid{{"o",diagnostic},{"bogus",diagnostic},{"2",diagnostic}}
 }
 values := []geqoInvalid{{"bogus",resourceInvalid(d.name,"bogus")}}
 bounds := []string{}
 switch d.name {
 case "geqo_threshold": bounds = []string{"1"}
 case "geqo_effort": bounds = []string{"0","11"}
 case "geqo_pool_size", "geqo_generations": bounds = []string{"-1"}
 case "geqo_selection_bias": bounds = []string{"1.49","2.01"}
 case "geqo_seed": bounds = []string{"-0.01","1.01","Infinity"}
 }
 for _, v := range bounds { values = append(values, geqoInvalid{v,geqoBounds(d,v)}) }
 if d.kind == "integer" && d.name != "geqo_effort" {
  diagnostic := resourceInvalid(d.name,"2147483648"); diagnostic.Hint = "Value exceeds integer range."
  values = append(values,geqoInvalid{"2147483648",diagnostic})
 }
 if d.kind == "real" { values = append(values,geqoInvalid{"NaN",resourceInvalid(d.name,"NaN")}) }
 if d.kind == "real" || d.name == "geqo_threshold" { values = append(values,geqoInvalid{"1kB",resourceInvalid(d.name,"1kB")}) }
 return values
}
