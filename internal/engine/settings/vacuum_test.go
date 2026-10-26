package settings

import (
 "fmt"
 "reflect"
 "sort"
 "strconv"
 "testing"
 "stuntgres/internal/sqlstate"
)

// These literal expectations follow the released descriptor contract, not vacuumBuiltins.
var vacuumExpected = []struct { name string; kind Type; access Access; unit Unit; raw, show, category, description, extra string; min,max float64 }{
 {"autovacuum", Boolean, Sighup, UnitNone, "on", "on", "Vacuuming / Automatic Vacuuming", "Starts the autovacuum subprocess.", "", 0, 0},
 {"autovacuum_analyze_scale_factor", Floating, Sighup, UnitNone, "0.1", "0.1", "Vacuuming / Automatic Vacuuming", "Number of tuple inserts, updates, or deletes prior to analyze as a fraction of reltuples.", "", 0, 100},
 {"autovacuum_analyze_threshold", Integer, Sighup, UnitNone, "50", "50", "Vacuuming / Automatic Vacuuming", "Minimum number of tuple inserts, updates, or deletes prior to analyze.", "", 0, 2147483647},
 {"autovacuum_freeze_max_age", Integer, StartupOnly, UnitNone, "200000000", "200000000", "Vacuuming / Automatic Vacuuming", "Age at which to autovacuum a table to prevent transaction ID wraparound.", "", 100000, 2000000000},
 {"autovacuum_max_workers", Integer, Sighup, UnitNone, "3", "3", "Vacuuming / Automatic Vacuuming", "Sets the maximum number of simultaneously running autovacuum worker processes.", "", 1, 262143},
 {"autovacuum_multixact_freeze_max_age", Integer, StartupOnly, UnitNone, "400000000", "400000000", "Vacuuming / Automatic Vacuuming", "Multixact age at which to autovacuum a table to prevent multixact wraparound.", "", 10000, 2000000000},
 {"autovacuum_naptime", Integer, Sighup, Seconds, "60", "1min", "Vacuuming / Automatic Vacuuming", "Time to sleep between autovacuum runs.", "", 1, 2147483},
 {"autovacuum_vacuum_cost_delay", Floating, Sighup, Milliseconds, "2", "2ms", "Vacuuming / Automatic Vacuuming", "Vacuum cost delay in milliseconds, for autovacuum.", "-1 means use \"vacuum_cost_delay\".", -1, 100},
 {"autovacuum_vacuum_cost_limit", Integer, Sighup, UnitNone, "-1", "-1", "Vacuuming / Automatic Vacuuming", "Vacuum cost amount available before napping, for autovacuum.", "-1 means use \"vacuum_cost_limit\".", -1, 10000},
 {"autovacuum_vacuum_insert_scale_factor", Floating, Sighup, UnitNone, "0.2", "0.2", "Vacuuming / Automatic Vacuuming", "Number of tuple inserts prior to vacuum as a fraction of reltuples.", "", 0, 100},
 {"autovacuum_vacuum_insert_threshold", Integer, Sighup, UnitNone, "1000", "1000", "Vacuuming / Automatic Vacuuming", "Minimum number of tuple inserts prior to vacuum.", "-1 disables insert vacuums.", -1, 2147483647},
 {"autovacuum_vacuum_max_threshold", Integer, Sighup, UnitNone, "100000000", "100000000", "Vacuuming / Automatic Vacuuming", "Maximum number of tuple updates or deletes prior to vacuum.", "-1 disables the maximum threshold.", -1, 2147483647},
 {"autovacuum_vacuum_scale_factor", Floating, Sighup, UnitNone, "0.2", "0.2", "Vacuuming / Automatic Vacuuming", "Number of tuple updates or deletes prior to vacuum as a fraction of reltuples.", "", 0, 100},
 {"autovacuum_vacuum_threshold", Integer, Sighup, UnitNone, "50", "50", "Vacuuming / Automatic Vacuuming", "Minimum number of tuple updates or deletes prior to vacuum.", "", 0, 2147483647},
 {"autovacuum_worker_slots", Integer, StartupOnly, UnitNone, "16", "16", "Vacuuming / Automatic Vacuuming", "Sets the number of backend slots to allocate for autovacuum workers.", "", 1, 262143},
 {"vacuum_cost_delay", Floating, Session, Milliseconds, "0", "0", "Vacuuming / Cost-Based Vacuum Delay", "Vacuum cost delay in milliseconds.", "", 0, 100},
 {"vacuum_cost_limit", Integer, Session, UnitNone, "200", "200", "Vacuuming / Cost-Based Vacuum Delay", "Vacuum cost amount available before napping.", "", 1, 10000},
 {"vacuum_cost_page_dirty", Integer, Session, UnitNone, "20", "20", "Vacuuming / Cost-Based Vacuum Delay", "Vacuum cost for a page dirtied by vacuum.", "", 0, 10000},
 {"vacuum_cost_page_hit", Integer, Session, UnitNone, "1", "1", "Vacuuming / Cost-Based Vacuum Delay", "Vacuum cost for a page found in the buffer cache.", "", 0, 10000},
 {"vacuum_cost_page_miss", Integer, Session, UnitNone, "2", "2", "Vacuuming / Cost-Based Vacuum Delay", "Vacuum cost for a page not found in the buffer cache.", "", 0, 10000},
 {"vacuum_failsafe_age", Integer, Session, UnitNone, "1600000000", "1600000000", "Vacuuming / Freezing", "Age at which VACUUM should trigger failsafe to avoid a wraparound outage.", "", 0, 2100000000},
 {"vacuum_freeze_min_age", Integer, Session, UnitNone, "50000000", "50000000", "Vacuuming / Freezing", "Minimum age at which VACUUM should freeze a table row.", "", 0, 1000000000},
 {"vacuum_freeze_table_age", Integer, Session, UnitNone, "150000000", "150000000", "Vacuuming / Freezing", "Age at which VACUUM should scan whole table to freeze tuples.", "", 0, 2000000000},
 {"vacuum_max_eager_freeze_failure_rate", Floating, Session, UnitNone, "0.03", "0.03", "Vacuuming / Freezing", "Fraction of pages in a relation vacuum can scan and fail to freeze before disabling eager scanning.", "A value of 0.0 disables eager scanning and a value of 1.0 will eagerly scan up to 100 percent of the all-visible pages in the relation. If vacuum successfully freezes these pages, the cap is lower than 100 percent, because the goal is to amortize page freezing across multiple vacuums.", 0, 1},
 {"vacuum_multixact_failsafe_age", Integer, Session, UnitNone, "1600000000", "1600000000", "Vacuuming / Freezing", "Multixact age at which VACUUM should trigger failsafe to avoid a wraparound outage.", "", 0, 2100000000},
 {"vacuum_multixact_freeze_min_age", Integer, Session, UnitNone, "5000000", "5000000", "Vacuuming / Freezing", "Minimum age at which VACUUM should freeze a MultiXactId in a table row.", "", 0, 1000000000},
 {"vacuum_multixact_freeze_table_age", Integer, Session, UnitNone, "150000000", "150000000", "Vacuuming / Freezing", "Multixact age at which VACUUM should scan whole table to freeze tuples.", "", 0, 2000000000},
 {"vacuum_truncate", Boolean, Session, UnitNone, "on", "on", "Vacuuming / Default Behavior", "Enables vacuum to truncate empty pages at the end of the table.", "", 0, 0},
}

func TestVacuumSettingsDefinitions(t *testing.T) {
 r:=NewRegistry(nil,false)
 if !sort.StringsAreSorted(r.Order()) { t.Fatal("registry is not sorted") }
 if len(vacuumExpected)!=28 { t.Fatal("independent inventory incomplete") }
 for _, want:=range vacuumExpected {
  t.Run(want.name,func(t *testing.T) {
   d,err:=r.Lookup(want.name); if err!=nil { t.Fatal(err) }
   if d.Name!=want.name || d.Type!=want.kind || d.Access!=want.access || d.Unit!=want.unit || d.Category!=want.category || d.Description!=want.description || d.ExtraDescription!=want.extra { t.Fatalf("metadata: %+v",d) }
   if d.ReadRestricted || d.BootDefaultNull || d.FixedDefault || d.NativeStartup!=nil || d.Normalize!=nil || d.EnumValues!=nil { t.Fatalf("unexpected flags/hooks: %+v",d) }
   if d.Type==Integer { if float64(d.IntegerMin)!=want.min || float64(d.IntegerMax)!=want.max || d.FloatMin!=0 || d.FloatMax!=0 { t.Fatal("integer bounds",d) } } else if d.Type==Floating { if d.FloatMin!=want.min || d.FloatMax!=want.max || d.IntegerMin!=0 || d.IntegerMax!=0 { t.Fatal("real bounds",d) } } else if d.IntegerMin!=0 || d.IntegerMax!=0 || d.FloatMin!=0 || d.FloatMax!=0 { t.Fatal("Boolean bounds",d) }
   if d.BootDefault!=want.show || d.Raw(d.BootValue())!=want.raw || d.CurrentRaw(d.DefaultValue())!=want.raw || d.Format(d.DefaultValue())!=want.show || !reflect.DeepEqual(d.DefaultValue(),d.BootValue()) { t.Fatal("boot/default",d) }
   count:=0; for _,name:=range r.Order() { if name==want.name { count++ } }; if count!=1 { t.Fatal("registry occurrence",count) }
  })
 }
}

func TestVacuumSettingsParsing(t *testing.T) {
 r:=NewRegistry(nil,false)
 for _,want:=range vacuumExpected {
  t.Run(want.name,func(t *testing.T) {
   d,_:=r.Lookup(want.name)
   valid:=func(input string) Value { t.Helper(); v,err:=d.Parse(input); if err!=nil { t.Fatal(input,err) }; return v }
   invalid:=func(input string) *sqlstate.Error { t.Helper(); _,err:=d.Parse(input); e,ok:=err.(*sqlstate.Error); if !ok || e.Code!="22023" { t.Fatal(input,err) }; return e }
   if d.Type==Boolean {
    for input,on:=range map[string]bool{"on":true,"OFF":false,"true":true,"false":false,"yes":true,"no":false,"1":true,"0":false,"Tr":true,"f":false,"y":true,"n":false} { if valid(input).Boolean!=on { t.Fatal(input) } }
    e:=invalid("o"); if e.Message!=fmt.Sprintf("parameter %q requires a Boolean value",d.Name) || e.Hint!="" { t.Fatal(e) }; return
   }
   number:=func(n float64) string { return strconv.FormatFloat(n,'f',-1,64) }
   for _,n:=range []float64{want.min,want.max} { if valid(number(n)).Number!=n { t.Fatal("bound",n) } }
   for _,n:=range []float64{want.min-1,want.max+1} { invalid(number(n)) }
   invalid("bogus")
   if d.Type==Integer { e:=invalid("2147483648"); if e.Hint!="Value exceeds integer range." { t.Fatal(e) } } else { for _,input:=range []string{"NaN","Infinity","1e999","1e-999"} { invalid(input) } }
  })
 }
 for _,tc:=range []struct{name,input,raw,show string}{
  {"autovacuum_analyze_scale_factor","2","2","2"},{"autovacuum_vacuum_scale_factor","100","100","100"},{"autovacuum_vacuum_insert_scale_factor","2","2","2"},
  {"autovacuum_vacuum_cost_delay","-1","-1","-1"},{"autovacuum_vacuum_cost_limit","-1","-1","-1"},{"autovacuum_vacuum_insert_threshold","-1","-1","-1"},{"autovacuum_vacuum_max_threshold","-1","-1","-1"},
  {"autovacuum_vacuum_cost_limit","0","0","0"},{"autovacuum_max_workers","17","17","17"},
  {"vacuum_cost_limit","2.5","2","2"},{"vacuum_cost_limit","3.5","4","4"},{"vacuum_cost_limit","010","8","8"},{"vacuum_cost_limit","0xA","10","10"},
  {"vacuum_cost_delay","1500us","1.5","1500us"},{"vacuum_cost_delay","0.0001","0.0001","0.1us"},{"vacuum_cost_delay","0.0001ms","0","0"},
 } { d,_:=r.Lookup(tc.name); v,err:=d.Parse(tc.input); if err!=nil || d.Raw(v)!=tc.raw || d.Format(v)!=tc.show { t.Fatal(tc,v,err) } }
}
