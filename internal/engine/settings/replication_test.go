package settings

import (
 "errors"
 "fmt"
 "sort"
 "testing"

 "stuntgres/internal/sqlstate"
)

type replicationExpected struct {
 name string
 kind Type
 access Access
 unit Unit
 raw, show, category, description, extra string
 min, max int32
}
var replicationExpectedDefinitions = []replicationExpected{
	{"hot_standby", Boolean, StartupOnly, UnitNone, "on", "on", "Replication / Standby Servers", "Allows connections and queries during recovery.", "", 0, 0},
	{"hot_standby_feedback", Boolean, Sighup, UnitNone, "off", "off", "Replication / Standby Servers", "Allows feedback from a hot standby to the primary that will avoid query conflicts.", "", 0, 0},
	{"idle_replication_slot_timeout", Integer, Sighup, Seconds, "0", "0", "Replication / Sending Servers", "Sets the duration a replication slot can remain idle before it is invalidated.", "", 0, 2147483647},
	{"max_active_replication_origins", Integer, StartupOnly, UnitNone, "10", "10", "Replication / Subscribers", "Sets the maximum number of active replication origins.", "", 0, 262143},
	{"max_logical_replication_workers", Integer, StartupOnly, UnitNone, "4", "4", "Replication / Subscribers", "Maximum number of logical replication worker processes.", "", 0, 262143},
	{"max_parallel_apply_workers_per_subscription", Integer, Sighup, UnitNone, "2", "2", "Replication / Subscribers", "Maximum number of parallel apply workers per subscription.", "", 0, 1024},
	{"max_replication_slots", Integer, StartupOnly, UnitNone, "10", "10", "Replication / Sending Servers", "Sets the maximum number of simultaneously defined replication slots.", "", 0, 262143},
	{"max_slot_wal_keep_size", Integer, Sighup, Megabytes, "-1", "-1", "Replication / Sending Servers", "Sets the maximum WAL size that can be reserved by replication slots.", "Replication slots will be marked as failed, and segments released for deletion or recycling, if this much space is occupied by WAL on disk. -1 means no maximum.", -1, 2147483647},
	{"max_standby_archive_delay", Integer, Sighup, Milliseconds, "30000", "30s", "Replication / Standby Servers", "Sets the maximum delay before canceling queries when a hot standby server is processing archived WAL data.", "-1 means wait forever.", -1, 2147483647},
	{"max_standby_streaming_delay", Integer, Sighup, Milliseconds, "30000", "30s", "Replication / Standby Servers", "Sets the maximum delay before canceling queries when a hot standby server is processing streamed WAL data.", "-1 means wait forever.", -1, 2147483647},
	{"max_sync_workers_per_subscription", Integer, Sighup, UnitNone, "2", "2", "Replication / Subscribers", "Maximum number of table synchronization workers per subscription.", "", 0, 262143},
	{"max_wal_senders", Integer, StartupOnly, UnitNone, "10", "10", "Replication / Sending Servers", "Sets the maximum number of simultaneously running WAL sender processes.", "", 0, 262143},
	{"output_plugin_libraries", String, Privileged, UnitNone, "pgoutput, test_decoding", "pgoutput, test_decoding", "Replication / Sending Servers", "Lists libraries that may be named as logical decoding output plugins.", "Users with REPLICATION privileges may only use plugins in this list when creating logical replication slots.", 0, 0},
	{"primary_conninfo", String, Sighup, UnitNone, "", "", "Replication / Standby Servers", "Sets the connection string to be used to connect to the sending server.", "", 0, 0},
	{"primary_slot_name", String, Sighup, UnitNone, "", "", "Replication / Standby Servers", "Sets the name of the replication slot to use on the sending server.", "", 0, 0},
	{"recovery_min_apply_delay", Integer, Sighup, Milliseconds, "0", "0", "Replication / Standby Servers", "Sets the minimum delay for applying changes during recovery.", "", 0, 2147483647},
	{"sync_replication_slots", Boolean, Sighup, UnitNone, "off", "off", "Replication / Standby Servers", "Enables a physical standby to synchronize logical failover replication slots from the primary server.", "", 0, 0},
	{"synchronized_standby_slots", String, Sighup, UnitNone, "", "", "Replication / Primary Server", "Lists streaming replication standby server replication slot names that logical WAL sender processes will wait for.", "Logical WAL sender processes will send decoded changes to output plugins only after the specified replication slots have confirmed receiving WAL.", 0, 0},
	{"synchronous_standby_names", String, Sighup, UnitNone, "", "", "Replication / Primary Server", "Number of synchronous standbys and list of names of potential synchronous ones.", "", 0, 0},
	{"track_commit_timestamp", Boolean, StartupOnly, UnitNone, "off", "off", "Replication / Sending Servers", "Collects transaction commit time.", "", 0, 0},
	{"wal_keep_size", Integer, Sighup, Megabytes, "0", "0", "Replication / Sending Servers", "Sets the size of WAL files held for standby servers.", "", 0, 2147483647},
	{"wal_receiver_create_temp_slot", Boolean, Sighup, UnitNone, "off", "off", "Replication / Standby Servers", "Sets whether a WAL receiver should create a temporary replication slot if no permanent slot is configured.", "", 0, 0},
	{"wal_receiver_status_interval", Integer, Sighup, Seconds, "10", "10s", "Replication / Standby Servers", "Sets the maximum interval between WAL receiver status reports to the sending server.", "", 0, 2147483},
	{"wal_receiver_timeout", Integer, Sighup, Milliseconds, "60000", "1min", "Replication / Standby Servers", "Sets the maximum wait time to receive data from the sending server.", "0 disables the timeout.", 0, 2147483647},
	{"wal_retrieve_retry_interval", Integer, Sighup, Milliseconds, "5000", "5s", "Replication / Standby Servers", "Sets the time to wait before retrying to retrieve WAL after a failed attempt.", "", 1, 2147483647},
	{"wal_sender_timeout", Integer, Session, Milliseconds, "60000", "1min", "Replication / Sending Servers", "Sets the maximum time to wait for WAL replication.", "", 0, 2147483647},
}
func TestReplicationSettingsDefinitions(t *testing.T) {
 for _, tracking := range []bool{false,true} {
  r := NewRegistry(nil, tracking)
  if !sort.StringsAreSorted(r.Order()) || len(replicationBuiltins) != 25 || len(replicationExpectedDefinitions) != 26 { t.Fatal("replication registry inventory") }
  types, contexts := map[Type]int{}, map[Access]int{}
  for _, tc := range replicationExpectedDefinitions {
   d, err := r.Lookup(tc.name); if err != nil { t.Fatal(err) }
   raw, show := tc.raw, tc.show
   if tc.name == "track_commit_timestamp" && tracking { raw,show = "on","on" }
   if d.Type != tc.kind || d.Access != tc.access || d.Unit != tc.unit || d.Raw(d.BootValue()) != raw || d.Format(d.DefaultValue()) != show || d.BootDefault != show || d.Category != tc.category || d.Description != tc.description || d.ExtraDescription != tc.extra || d.IntegerMin != tc.min || d.IntegerMax != tc.max || d.EnumValues != nil || d.NativeStartup != nil || d.BootDefaultNull || d.ReadRestricted != (tc.name == "primary_conninfo" || tc.name == "output_plugin_libraries") { t.Fatalf("%s descriptor: %+v",tc.name,d) }
   count := 0; for _, name := range r.Order() { if name == tc.name { count++ } }; if count != 1 { t.Fatal(tc.name,count) }
   types[d.Type]++; contexts[d.Access]++
  }
  if types[Boolean] != 5 || types[Integer] != 16 || types[String] != 5 || contexts[StartupOnly] != 6 || contexts[Sighup] != 18 || contexts[Session] != 1 || contexts[Privileged] != 1 { t.Fatal(types,contexts) }
 }
}
func TestReplicationSettingsValues(t *testing.T) {
 r := NewRegistry(nil,false)
 for _, tc := range replicationExpectedDefinitions {
  t.Run(tc.name,func(t *testing.T) {
   d,_ := r.Lookup(tc.name)
   check := func(input,raw,show string) { t.Helper(); v,err := d.Parse(input); if err != nil || d.Raw(v) != raw || d.Format(v) != show { t.Fatalf("%q got raw=%q show=%q err=%v",input,d.Raw(v),d.Format(v),err) } }
   invalid := func(input,hint string) { t.Helper(); _,err := d.Parse(input); var e *sqlstate.Error; if !errors.As(err,&e) || e.Code != "22023" || e.Hint != hint { t.Fatal(input,err) } }
   check(tc.raw,tc.raw,tc.show)
   switch tc.kind {
   case Boolean:
    for _, input := range []string{"on","ON","true","TRUE","t","tr","yes","y","1"} { check(input,"on","on") }
    for _, input := range []string{"off","OFF","false","FALSE","f","fa","no","n","0"} { check(input,"off","off") }
    invalid("o","")
   case String:
    for _, input := range []string{"", "not-a-real-library, \"odd name\"", "/missing/path", "a,,b", "\"unterminated", "Mixed Case"} { check(input,input,input) }
   case Integer:
    for _, bound := range []int32{tc.min,tc.max} { input := fmt.Sprint(bound); v,err := d.Parse(input); if err != nil || d.Raw(v) != input { t.Fatal(input,v,err) } }
    invalid(fmt.Sprint(int64(tc.min)-1),"")
    hint := ""; if tc.max == 2147483647 { hint = "Value exceeds integer range." }; invalid(fmt.Sprint(int64(tc.max)+1),hint)
    invalid("bogus","")
    switch tc.unit {
    case Milliseconds: check("1500ms","1500","1500ms"); check("2min","120000","2min"); check("1000us","1","1ms")
    case Seconds: check("2min","120","2min"); check("2000ms","2","2s")
    case Megabytes: check("1GB","1024","1GB"); check("2048kB","2","2MB")
    }
   }
  })
 }
}
