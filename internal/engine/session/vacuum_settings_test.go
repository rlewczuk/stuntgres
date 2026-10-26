package session

import (
 "fmt"
 "reflect"
 "testing"
 "stuntgres/internal/engine/settings"
 "stuntgres/internal/sqlstate"
)

var vacuumImmutableDefaults = []struct{name,equivalent,changed string}{
 {"autovacuum", "TRUE", "off"},
 {"autovacuum_analyze_scale_factor", "0.1", "1.1"},
 {"autovacuum_analyze_threshold", "50", "51"},
 {"autovacuum_freeze_max_age", "200000000", "200000001"},
 {"autovacuum_max_workers", "3", "4"},
 {"autovacuum_multixact_freeze_max_age", "400000000", "400000001"},
 {"autovacuum_naptime", "1min", "61"},
 {"autovacuum_vacuum_cost_delay", "2000us", "3.0"},
 {"autovacuum_vacuum_cost_limit", "-1", "0"},
 {"autovacuum_vacuum_insert_scale_factor", "0.2", "1.2"},
 {"autovacuum_vacuum_insert_threshold", "1000", "1001"},
 {"autovacuum_vacuum_max_threshold", "100000000", "100000001"},
 {"autovacuum_vacuum_scale_factor", "0.2", "1.2"},
 {"autovacuum_vacuum_threshold", "50", "51"},
 {"autovacuum_worker_slots", "16", "17"},
}
func TestVacuumSettingsDefaultCapture(t *testing.T) {
 r:=settings.NewRegistry(nil,false)
 capture:=func(p settings.SessionDefaultProvider)(*SettingsStore,error) { s:=&SettingsStore{Defaults:map[string]string{},Values:map[string]string{}}; err:=s.Initialize(t.Context(),r.Definitions(),r.Order(),p); return s,err }
 for _,tc:=range vacuumImmutableDefaults {
  t.Run(tc.name,func(t *testing.T) {
   s,err:=capture(physicalDefaultProvider{tc.name:tc.equivalent}); if err!=nil { t.Fatal(err) }
   d:=r.Definitions()[tc.name]
   if !reflect.DeepEqual(s.DefaultTyped[tc.name],d.DefaultValue()) { t.Fatal("equivalent default",s.DefaultTyped[tc.name]) }
   _,err=capture(physicalDefaultProvider{tc.name:tc.changed}); if err==nil || err.Error()!="stuntgres: default provider cannot change "+tc.name { t.Fatal(err) }
   _,err=capture(physicalDefaultProvider{tc.name:"bogus"}); e,ok:=err.(*sqlstate.Error); if !ok || e.Code!="22023" { t.Fatal("invalid provider",err) }
  })
 }
 p:=&clientDefaultProvider{physicalDefaultProvider:physicalDefaultProvider{"vacuum_cost_delay":"1.234567ms"}}
 s,err:=capture(p); if err!=nil { t.Fatal(err) }
 d:=r.Definitions()["vacuum_cost_delay"]
 // Explicit time units round to microseconds; bare values retain full precision.
 if s.DefaultTyped[d.Name].Number!=1.235 { t.Fatal("unit rounding",s.DefaultTyped[d.Name]) }
 p.physicalDefaultProvider[d.Name]="1.234567"
 s,err=capture(p); if err!=nil { t.Fatal(err) }
 if s.DefaultTyped[d.Name].Number!=1.234567 { t.Fatal("precision lost",s.DefaultTyped[d.Name]) }
 calls:=p.calls
 p.physicalDefaultProvider[d.Name]="2.5"
 for range 3 {
  s.Values[d.Name]="9ms"; s.Typed[d.Name]=settings.Value{Number:9}
  s.ResetAllLocked(r)
  if s.Value(d.Name)!=d.Format(s.DefaultTyped[d.Name]) || s.DefaultTyped[d.Name].Number!=1.234567 || s.TypedValue(d.Name).Number!=1.234567 || p.calls!=calls { t.Fatal("reset changed captured default",s.DefaultTyped[d.Name],p.calls) }
 }
 next,err:=capture(p); if err!=nil || next.DefaultTyped[d.Name].Number!=2.5 || s.DefaultTyped[d.Name].Number!=1.234567 { t.Fatal("replacement capture",err) }
}
func TestVacuumSettingsAccess(t *testing.T) {
 r:=settings.NewRegistry(nil,false)
 count:=0
 for name,d:=range r.Definitions() {
  selected:=false
  for _,tc:=range vacuumImmutableDefaults { if name==tc.name { selected=true } }
  switch name { case "vacuum_cost_delay","vacuum_cost_limit","vacuum_cost_page_dirty","vacuum_cost_page_hit","vacuum_cost_page_miss","vacuum_failsafe_age","vacuum_freeze_min_age","vacuum_freeze_table_age","vacuum_max_eager_freeze_failure_rate","vacuum_multixact_failsafe_age","vacuum_multixact_freeze_min_age","vacuum_multixact_freeze_table_age","vacuum_truncate": selected=true }
  if !selected { continue }; count++
  if err:=SettingReadAccess(d,name,false); err!=nil { t.Fatal(name,err) }
  calls:=0; err:=SettingAccess(d,func(string)(bool,error){calls++;return false,nil})
  if calls!=0 { t.Fatal("unexpected authority callback",name) }
  if d.Access==settings.Session { if err!=nil { t.Fatal(name,err) }; continue }
  suffix:="now"; if d.Access==settings.StartupOnly { suffix="without restarting the server" }
  e,ok:=err.(*sqlstate.Error); if !ok || e.Code!="55P02" || e.Message!=fmt.Sprintf("parameter %q cannot be changed %s",name,suffix) { t.Fatal(name,err) }
 }
 if count!=28 { t.Fatal("access inventory",count) }
}
