package reference_test

import (
 "database/sql"
 "fmt"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

// Independent PostgreSQL REL_18_6 expectations; numeric text uses base units.
type vacuumSetting struct { name, kind, context, unit, category, raw, min, max, show, description, extra string }
var vacuumSettings = []vacuumSetting{
 {"autovacuum", "bool", "sighup", "", "Vacuuming / Automatic Vacuuming", "on", "", "", "on", "Starts the autovacuum subprocess.", ""},
 {"autovacuum_analyze_scale_factor", "real", "sighup", "", "Vacuuming / Automatic Vacuuming", "0.1", "0", "100", "0.1", "Number of tuple inserts, updates, or deletes prior to analyze as a fraction of reltuples.", ""},
 {"autovacuum_analyze_threshold", "integer", "sighup", "", "Vacuuming / Automatic Vacuuming", "50", "0", "2147483647", "50", "Minimum number of tuple inserts, updates, or deletes prior to analyze.", ""},
 {"autovacuum_freeze_max_age", "integer", "postmaster", "", "Vacuuming / Automatic Vacuuming", "200000000", "100000", "2000000000", "200000000", "Age at which to autovacuum a table to prevent transaction ID wraparound.", ""},
 {"autovacuum_max_workers", "integer", "sighup", "", "Vacuuming / Automatic Vacuuming", "3", "1", "262143", "3", "Sets the maximum number of simultaneously running autovacuum worker processes.", ""},
 {"autovacuum_multixact_freeze_max_age", "integer", "postmaster", "", "Vacuuming / Automatic Vacuuming", "400000000", "10000", "2000000000", "400000000", "Multixact age at which to autovacuum a table to prevent multixact wraparound.", ""},
 {"autovacuum_naptime", "integer", "sighup", "s", "Vacuuming / Automatic Vacuuming", "60", "1", "2147483", "1min", "Time to sleep between autovacuum runs.", ""},
 {"autovacuum_vacuum_cost_delay", "real", "sighup", "ms", "Vacuuming / Automatic Vacuuming", "2", "-1", "100", "2ms", "Vacuum cost delay in milliseconds, for autovacuum.", "-1 means use \"vacuum_cost_delay\"."},
 {"autovacuum_vacuum_cost_limit", "integer", "sighup", "", "Vacuuming / Automatic Vacuuming", "-1", "-1", "10000", "-1", "Vacuum cost amount available before napping, for autovacuum.", "-1 means use \"vacuum_cost_limit\"."},
 {"autovacuum_vacuum_insert_scale_factor", "real", "sighup", "", "Vacuuming / Automatic Vacuuming", "0.2", "0", "100", "0.2", "Number of tuple inserts prior to vacuum as a fraction of reltuples.", ""},
 {"autovacuum_vacuum_insert_threshold", "integer", "sighup", "", "Vacuuming / Automatic Vacuuming", "1000", "-1", "2147483647", "1000", "Minimum number of tuple inserts prior to vacuum.", "-1 disables insert vacuums."},
 {"autovacuum_vacuum_max_threshold", "integer", "sighup", "", "Vacuuming / Automatic Vacuuming", "100000000", "-1", "2147483647", "100000000", "Maximum number of tuple updates or deletes prior to vacuum.", "-1 disables the maximum threshold."},
 {"autovacuum_vacuum_scale_factor", "real", "sighup", "", "Vacuuming / Automatic Vacuuming", "0.2", "0", "100", "0.2", "Number of tuple updates or deletes prior to vacuum as a fraction of reltuples.", ""},
 {"autovacuum_vacuum_threshold", "integer", "sighup", "", "Vacuuming / Automatic Vacuuming", "50", "0", "2147483647", "50", "Minimum number of tuple updates or deletes prior to vacuum.", ""},
 {"autovacuum_worker_slots", "integer", "postmaster", "", "Vacuuming / Automatic Vacuuming", "16", "1", "262143", "16", "Sets the number of backend slots to allocate for autovacuum workers.", ""},
 {"vacuum_cost_delay", "real", "user", "ms", "Vacuuming / Cost-Based Vacuum Delay", "0", "0", "100", "0", "Vacuum cost delay in milliseconds.", ""},
 {"vacuum_cost_limit", "integer", "user", "", "Vacuuming / Cost-Based Vacuum Delay", "200", "1", "10000", "200", "Vacuum cost amount available before napping.", ""},
 {"vacuum_cost_page_dirty", "integer", "user", "", "Vacuuming / Cost-Based Vacuum Delay", "20", "0", "10000", "20", "Vacuum cost for a page dirtied by vacuum.", ""},
 {"vacuum_cost_page_hit", "integer", "user", "", "Vacuuming / Cost-Based Vacuum Delay", "1", "0", "10000", "1", "Vacuum cost for a page found in the buffer cache.", ""},
 {"vacuum_cost_page_miss", "integer", "user", "", "Vacuuming / Cost-Based Vacuum Delay", "2", "0", "10000", "2", "Vacuum cost for a page not found in the buffer cache.", ""},
 {"vacuum_failsafe_age", "integer", "user", "", "Vacuuming / Freezing", "1600000000", "0", "2100000000", "1600000000", "Age at which VACUUM should trigger failsafe to avoid a wraparound outage.", ""},
 {"vacuum_freeze_min_age", "integer", "user", "", "Vacuuming / Freezing", "50000000", "0", "1000000000", "50000000", "Minimum age at which VACUUM should freeze a table row.", ""},
 {"vacuum_freeze_table_age", "integer", "user", "", "Vacuuming / Freezing", "150000000", "0", "2000000000", "150000000", "Age at which VACUUM should scan whole table to freeze tuples.", ""},
 {"vacuum_max_eager_freeze_failure_rate", "real", "user", "", "Vacuuming / Freezing", "0.03", "0", "1", "0.03", "Fraction of pages in a relation vacuum can scan and fail to freeze before disabling eager scanning.", "A value of 0.0 disables eager scanning and a value of 1.0 will eagerly scan up to 100 percent of the all-visible pages in the relation. If vacuum successfully freezes these pages, the cap is lower than 100 percent, because the goal is to amortize page freezing across multiple vacuums."},
 {"vacuum_multixact_failsafe_age", "integer", "user", "", "Vacuuming / Freezing", "1600000000", "0", "2100000000", "1600000000", "Multixact age at which VACUUM should trigger failsafe to avoid a wraparound outage.", ""},
 {"vacuum_multixact_freeze_min_age", "integer", "user", "", "Vacuuming / Freezing", "5000000", "0", "1000000000", "5000000", "Minimum age at which VACUUM should freeze a MultiXactId in a table row.", ""},
 {"vacuum_multixact_freeze_table_age", "integer", "user", "", "Vacuuming / Freezing", "150000000", "0", "2000000000", "150000000", "Multixact age at which VACUUM should scan whole table to freeze tuples.", ""},
 {"vacuum_truncate", "bool", "user", "", "Vacuuming / Default Behavior", "on", "", "", "on", "Enables vacuum to truncate empty pages at the end of the table.", ""},
}

func vacuumDefinition(name string) vacuumSetting {
 for _, d := range vacuumSettings { if d.name == name { return d } }
 panic("missing independent vacuum expectation: "+name)
}
func vacuumPredicate() string {
 names := make([]string,len(vacuumSettings))
 for i,d := range vacuumSettings { names[i]=physicalLiteral(d.name) }
 return "name IN ("+strings.Join(names,",")+")"
}
// The pinned official image's initdb output explicitly writes worker slots,
// even though its initialized value equals the compiled boot value. Native
// initialization has no configuration file. All other family defaults retain
// source default on both backends.
func (s *physicalCase) vacuumInitialSource(d vacuumSetting) string {
 if s.pg && d.name=="autovacuum_worker_slots" { return "configuration file" }
 return "default"
}
func (s *physicalCase) vacuumValue(name, show, raw, source string, prepared bool) {
 s.t.Helper()
 for _, query := range []struct{sql,column string}{{"SHOW "+name,strings.ToLower(name)},{"SELECT current_setting($1)","current_setting"}} {
  var args []any
  if query.column=="current_setting" { args=[]any{name} }
  step:=fixture.Select(query.sql,[]string{query.column},[]string{"TEXT"},[][]any{{show}},args...)
  step.Prepared=prepared
  step.CheckMetadata=func(t *testing.T,rows *sql.Rows) { settingsTextMetadata(t,rows,s.pg,query.column=="current_setting") }
  s.run(step)
 }
 d:=vacuumDefinition(strings.ToLower(name))
 if source=="default" { source=s.vacuumInitialSource(d) }
 step:=fixture.Select("SELECT setting,source,reset_val FROM pg_settings WHERE name=$1",[]string{"setting","source","reset_val"},[]string{"TEXT","TEXT","TEXT"},[][]any{{raw,source,d.raw}},d.name)
 step.Prepared=prepared; s.run(step)
}
func vacuumInvalid(name,input string) fixture.Diagnostic { return physicalDiagnostic("22023",fmt.Sprintf("invalid value for parameter %q: %q",name,input)) }
func vacuumBounds(d vacuumSetting,value string) fixture.Diagnostic {
 unit:=""; if d.unit!="" { unit=" "+d.unit }
 return physicalDiagnostic("22023",fmt.Sprintf("%s%s is outside the valid range for parameter %q (%s%s .. %s%s)",value,unit,d.name,d.min,unit,d.max,unit))
}
