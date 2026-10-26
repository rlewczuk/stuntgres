package settings

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestWALSettingsEnums(t *testing.T) {
	r := NewRegistry(nil,false)
	for _, tc := range []struct { name string; visible []string; aliases map[string]string }{
		{"archive_mode",[]string{"always","on","off"},map[string]string{"TRUE":"on","yes":"on","1":"on","false":"off","NO":"off","0":"off"}},
		{"recovery_prefetch",[]string{"off","on","try"},map[string]string{"TRUE":"on","yes":"on","1":"on","false":"off","NO":"off","0":"off"}},
		{"recovery_target_action",[]string{"pause","promote","shutdown"},nil},
		{"synchronous_commit",[]string{"local","remote_write","remote_apply","on","off"},map[string]string{"TRUE":"on","yes":"on","1":"on","false":"off","NO":"off","0":"off"}},
		{"wal_compression",[]string{"pglz","lz4","zstd","on","off"},map[string]string{"ON":"pglz","true":"pglz","YES":"pglz","1":"pglz","false":"off","NO":"off","0":"off"}},
		{"wal_level",[]string{"minimal","replica","logical"},map[string]string{"archive":"replica","HOT_STANDBY":"replica"}},
		{"wal_sync_method",[]string{"fsync","fdatasync","open_sync","open_datasync"},nil},
	} {
		d,err := r.Lookup(tc.name); if err != nil { t.Fatal(err) }
		if !reflect.DeepEqual(d.EnumValues,tc.visible) { t.Fatal(tc.name,d.EnumValues) }
		for _, input := range tc.visible {
			want := input; if tc.name == "wal_compression" && input == "on" { want = "pglz" }
			v,err := d.Parse(input); if err != nil || d.Format(v) != want { t.Fatal(tc.name,input,v,err) }
		}
		for input,want := range tc.aliases { v,err := d.Parse(input); if err != nil || d.Format(v) != want { t.Fatal(tc.name,input,v,err) } }
		for _, input := range []string{"o"," on ","t","n","unknown"} {
			_,err := d.Parse(input); var diagnostic *sqlstate.Error
			if !errors.As(err,&diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != "invalid value for parameter \""+tc.name+"\": \""+input+"\"" || diagnostic.Hint == "" { t.Fatal(tc.name,input,err) }
		}
	}
}

func TestWALSettingsDefaults(t *testing.T) {
	r := NewRegistry(nil,false)
	if len(walBuiltins) != 40 || !sort.StringsAreSorted(r.Order()) { t.Fatal("WAL inventory order") }
	counts := map[Access]int{}
	for _, original := range walBuiltins {
		d,err := r.Lookup(original.Name); if err != nil { t.Fatal(err) }; counts[d.Access]++
		if d.ReadRestricted || d.BootDefaultNull { t.Fatal("unrestricted non-NULL WAL metadata",d.Name) }
		found := 0; for _, name := range r.Order() { if name == d.Name { found++ } }; if found != 1 { t.Fatal(d.Name,found) }
	}
	if counts[StartupOnly] != 13 || counts[Sighup] != 20 || counts[Privileged] != 4 || counts[Session] != 3 { t.Fatal(counts) }
	for _, tc := range []struct{ name,boot,current,reset,show string }{
		{"archive_command","","(disabled)","",""},
		{"wal_buffers","-1","512","512","4MB"},
		{"checkpoint_flush_after","32","32","32","256kB"},
		{"wal_summary_keep_time","14400","14400","14400","10d"},
	} {
		d,_ := r.Lookup(tc.name)
		if d.Raw(d.BootValue()) != tc.boot || d.CurrentRaw(d.DefaultValue()) != tc.current || d.Raw(d.DefaultValue()) != tc.reset || d.Format(d.DefaultValue()) != tc.show { t.Fatal(tc.name,d.BootValue(),d.DefaultValue()) }
	}
	d,_ := r.Lookup("commit_delay"); if d.Unit != UnitNone { t.Fatal("commit_delay has no GUC unit") }
}
