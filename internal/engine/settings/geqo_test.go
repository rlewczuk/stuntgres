package settings

import (
 "errors"
 "testing"
 "stuntgres/internal/sqlstate"
)

func TestGeqoSettingsDescriptors(t *testing.T) {
 registry := NewRegistry(nil,false)
 cases := []struct { name string; kind Type; boot string; imin,imax int32; fmin,fmax float64; description,extra string }{
	{"geqo", Boolean, "on", 0, 0, 0, 0, "Enables genetic query optimization.", "This algorithm attempts to do planning without exhaustive searching."},
	{"geqo_effort", Integer, "5", 1, 10, 0, 0, "GEQO: effort is used to set the default for other GEQO parameters.", ""},
	{"geqo_generations", Integer, "0", 0, 2147483647, 0, 0, "GEQO: number of iterations of the algorithm.", "0 means use a suitable default value."},
	{"geqo_pool_size", Integer, "0", 0, 2147483647, 0, 0, "GEQO: number of individuals in the population.", "0 means use a suitable default value."},
	{"geqo_seed", Floating, "0", 0, 0, 0, 1, "GEQO: seed for random path selection.", ""},
	{"geqo_selection_bias", Floating, "2", 0, 0, 1.5, 2, "GEQO: selective pressure within the population.", ""},
	{"geqo_threshold", Integer, "12", 2, 2147483647, 0, 0, "Sets the threshold of FROM items beyond which GEQO is used.", ""},
 }
 for _, tc := range cases {
  d,err := registry.Lookup(tc.name); if err != nil { t.Fatal(err) }
  if d.Type != tc.kind || d.Access != Session || d.Unit != UnitNone || d.ReadRestricted || d.BootDefaultNull || d.FixedDefault || d.NativeStartup != nil || d.Normalize != nil || len(d.EnumValues) != 0 || d.BootDefault != tc.boot || d.IntegerMin != tc.imin || d.IntegerMax != tc.imax || d.FloatMin != tc.fmin || d.FloatMax != tc.fmax || d.Category != "Query Tuning / Genetic Query Optimizer" || d.Description != tc.description || d.ExtraDescription != tc.extra { t.Fatalf("%s: %+v",tc.name,d) }
  if d.Format(d.BootValue()) != tc.boot || d.Format(d.DefaultValue()) != tc.boot { t.Fatal("uncaptured default",tc.name) }
  count := 0; for _, name := range registry.Order() { if name == tc.name { count++ } }; if count != 1 { t.Fatal(tc.name,count) }
 }
 valid := map[string][][2]string{
 "geqo": {{"on","on"},{"true","on"},{"yes","on"},{"1","on"},{"TRUE","on"},{"t","on"},{"y","on"},{"off","off"},{"false","off"},{"no","off"},{"0","off"},{"f","off"},{"n","off"}},
 "geqo_threshold": {{"2","2"},{"12","12"},{"2147483647","2147483647"},{"2.5","2"},{"3.5","4"},{"010","8"},{"0xA","10"}},
 "geqo_effort": {{"1","1"},{"5","5"},{"10","10"}},
 "geqo_pool_size": {{"0","0"},{"1","1"},{"2","2"},{"2147483647","2147483647"}},
 "geqo_generations": {{"0","0"},{"1","1"},{"2147483647","2147483647"}},
 "geqo_selection_bias": {{"1.5","1.5"},{"1.75","1.75"},{"2","2"}},
 "geqo_seed": {{"0","0"},{"0.125","0.125"},{"1","1"}},
 }
 invalid := map[string][]string{
 "geqo": {"o","bogus","2"},
 "geqo_threshold": {"1","2147483648","bogus","1kB"},
 "geqo_effort": {"0","11","bogus"},
 "geqo_pool_size": {"-1","2147483648","bogus"},
 "geqo_generations": {"-1","2147483648","bogus"},
 "geqo_selection_bias": {"1.49","2.01","bogus","NaN","1kB"},
 "geqo_seed": {"-0.01","1.01","bogus","NaN","1kB","Infinity"},
 }
 for name, values := range valid {
  d,_ := registry.Lookup(name)
  for _, v := range values { parsed,err := d.Parse(v[0]); if err != nil || d.Format(parsed) != v[1] || d.Raw(parsed) != v[1] { t.Fatal(name,v,parsed,err) } }
  for _, input := range invalid[name] {
   _,err := d.Parse(input); var diagnostic *sqlstate.Error
   if !errors.As(err,&diagnostic) || diagnostic.Code != "22023" { t.Fatal(name,input,err) }
  }
 }
}
