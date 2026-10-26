package settings

import (
	"reflect"
	"testing"
)

func TestPresetSettingsDefinitions(t *testing.T) {
	r := NewRegistry(nil,false)
	for _, tc := range []struct { name, current, raw, boot, min, max string; kind Type; unit Unit }{
		{"block_size","8192","8192","8192","8192","8192",Integer,UnitNone},
		{"data_checksums","off","off","off","","",Boolean,UnitNone},
		{"data_directory_mode","0700","0700","448","0","511",Integer,UnitNone},
		{"debug_assertions","off","off","off","","",Boolean,UnitNone},
		{"huge_pages_status","off","off","unknown","","",Enum,UnitNone},
		{"in_hot_standby","off","off","off","","",Boolean,UnitNone},
		{"integer_datetimes","on","on","on","","",Boolean,UnitNone},
		{"max_function_args","100","100","100","100","100",Integer,UnitNone},
		{"max_identifier_length","63","63","63","63","63",Integer,UnitNone},
		{"max_index_keys","32","32","32","32","32",Integer,UnitNone},
		{"num_os_semaphores","0","0","0","0","2147483647",Integer,UnitNone},
		{"segment_size","1GB","131072","131072","131072","131072",Integer,Blocks8kB},
		{"server_encoding","UTF8","UTF8","SQL_ASCII","","",String,UnitNone},
		{"server_version","18.0","18.0","18.0","","",String,UnitNone},
		{"server_version_num","180000","180000","180000","180000","180000",Integer,UnitNone},
		{"shared_memory_size","0","0","0","0","2147483647",Integer,Megabytes},
		{"shared_memory_size_in_huge_pages","-1","-1","-1","-1","2147483647",Integer,UnitNone},
		{"ssl_library","","","","","",String,UnitNone},
		{"wal_block_size","8192","8192","8192","8192","8192",Integer,UnitNone},
		{"wal_segment_size","16MB","16777216","16777216","1048576","1073741824",Integer,Bytes},
	} {
		d,err := r.Lookup(tc.name); if err != nil { t.Fatal(err) }
		if d.Type != tc.kind || d.Unit != tc.unit || d.Access != ReadOnly || !d.FixedDefault || d.ReadRestricted || d.BootDefaultNull || d.Category != "Preset Options" || d.Description == "" { t.Fatal(tc.name,d) }
		if d.Format(d.DefaultValue()) != tc.current || d.CurrentRaw(d.DefaultValue()) != tc.raw || d.Raw(d.BootValue()) != tc.boot { t.Fatal(tc.name,d.DefaultValue(),d.BootValue()) }
		if d.Type == Integer && (d.Raw(Value{Number:float64(d.IntegerMin)}) != tc.min || d.Raw(Value{Number:float64(d.IntegerMax)}) != tc.max) { t.Fatal(tc.name,"bounds",d) }
		if d.NativeStartup != nil && (d.NativeStartup.Source != "default" || d.NativeStartup.ResetNull) { t.Fatal(tc.name,d.NativeStartup) }
		if d.Name == "huge_pages_status" && !reflect.DeepEqual(d.EnumValues,[]string{"off","on","unknown"}) { t.Fatal(d.EnumValues) }
	}
	for _, d := range []Definition{
		{Name:"preset.invalid",Type:Integer,Unit:Blocks8kB},
		{Name:"preset.invalid",Type:String,FixedDefault:true},
	} { if _,err := Validate(d); err == nil { t.Fatal("accepted builtin metadata",d) } }
}

func TestPresetSettingsPresentation(t *testing.T) {
	r := NewRegistry(nil,false)
	for _, tc := range []struct{name,input,show,raw string}{
		{"segment_size","1GB","1GB","131072"},
		{"segment_size","1048576kB","1GB","131072"},
		{"wal_segment_size","16MB","16MB","16777216"},
		{"data_directory_mode","0700","0700","448"},
		{"data_directory_mode","448","0700","448"},
	} {
		d,_ := r.Lookup(tc.name); v,err := d.Parse(tc.input)
		if err != nil || d.Format(v) != tc.show || d.Raw(v) != tc.raw { t.Fatal(tc, v,err) }
	}
	d,_ := r.Lookup("segment_size")
	if _,err := d.Parse("1ms"); err == nil { t.Fatal("accepted time unit for disk blocks") }
	if unitHint(Blocks8kB) != `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".` { t.Fatal(unitHint(Blocks8kB)) }
}
