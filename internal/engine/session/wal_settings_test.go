package session

import (
	"context"
	"testing"

	"stuntgres/internal/engine/settings"
)

func TestWALSettingsProviderDefaults(t *testing.T) {
	r := settings.NewRegistry(nil,false)
	capture := func(provider settings.SessionDefaultProvider) (*SettingsStore,error) {
		s := &SettingsStore{Defaults:map[string]string{},Values:map[string]string{}}
		err := s.Initialize(t.Context(),r.Definitions(),r.Order(),provider)
		return s,err
	}
	for _, tc := range []struct{ name,equivalent,changed string }{
		{"archive_cleanup_command","","other"},{"archive_command","","other"},{"archive_library","","other"},
		{"archive_mode","FALSE","on"},{"archive_timeout","0s","1s"},{"checkpoint_completion_target","0.90","0.5"},
		{"checkpoint_flush_after","256kB","512kB"},{"checkpoint_timeout","5min","10min"},{"checkpoint_warning","30s","1min"},
		{"fsync","true","off"},{"full_page_writes","yes","off"},{"max_wal_size","1GB","2GB"},{"min_wal_size","80MB","160MB"},
		{"recovery_end_command","","other"},{"recovery_prefetch","TRY","on"},{"recovery_target","","immediate"},
		{"recovery_target_action","PAUSE","promote"},{"recovery_target_inclusive","true","off"},
		{"recovery_target_lsn","","0/123"},{"recovery_target_name","","other"},{"recovery_target_time","","2026-01-01"},
		{"recovery_target_timeline","latest","current"},{"recovery_target_xid","","123"},{"restore_command","","other"},
		{"summarize_wal","false","on"},{"wal_buffers","4MB","8MB"},{"wal_decode_buffer_size","512kB","1MB"},
		{"wal_level","hot_standby","logical"},{"wal_log_hints","false","on"},{"wal_summary_keep_time","10d","1d"},
		{"wal_sync_method","FDATASYNC","fsync"},{"wal_writer_delay","200ms","1s"},{"wal_writer_flush_after","1MB","2MB"},
	} {
		if _,err := capture(physicalDefaultProvider{tc.name:tc.equivalent}); err != nil { t.Fatal("equivalent",tc.name,err) }
		if _,err := capture(physicalDefaultProvider{tc.name:tc.changed}); err == nil || err.Error() != "stuntgres: default provider cannot change "+tc.name { t.Fatal("changed immutable",tc.name,err) }
	}
	p := &walDefaultProvider{values:physicalDefaultProvider{
		"commit_delay":"12345","commit_siblings":"2.5","synchronous_commit":"REMOTE_APPLY","wal_compression":"YES",
		"wal_init_zero":"off","wal_recycle":"off","wal_skip_threshold":"1537kB",
	}}
	old,err := capture(p); if err != nil { t.Fatal(err) }
	want := map[string]string{"commit_delay":"12345","commit_siblings":"2","synchronous_commit":"remote_apply","wal_compression":"pglz","wal_init_zero":"off","wal_recycle":"off","wal_skip_threshold":"1537kB"}
	for name,value := range want {
		if old.Value(name) != value || !old.ProviderDefaults[name] || p.calls[name] != 1 { t.Fatal("provider capture",name,old.Value(name),p.calls[name]) }
		old.Values[name] = "override"
		p.values[name] = map[string]string{"commit_delay":"7","commit_siblings":"9","synchronous_commit":"off","wal_compression":"zstd","wal_init_zero":"on","wal_recycle":"on","wal_skip_threshold":"2MB"}[name]
	}
	old.ResetAllLocked(r)
	for name,value := range want { if old.Value(name) != value || old.Defaults[name] != value || p.calls[name] != 1 { t.Fatal("reset recaptured",name,old.Value(name)) } }
	if old.DefaultTyped["wal_skip_threshold"].Number != 1537 || old.DefaultTyped["commit_siblings"].Number != 2 { t.Fatal("lost typed precision") }
	fresh,err := capture(p); if err != nil { t.Fatal(err) }
	for name,value := range p.values { if fresh.Value(name) != value || old.Value(name) != want[name] || p.calls[name] != 2 { t.Fatal("replacement capture",name,fresh.Value(name),value) } }
	if old.Value("archive_command") != "(disabled)" || old.Defaults["archive_command"] != "" || old.DefaultTyped["archive_command"].Text != "" { t.Fatal("archive command default presentation") }
	buffers,_ := r.Lookup("wal_buffers")
	if old.Value("wal_buffers") != "4MB" || old.DefaultTyped["wal_buffers"].Number != 512 || buffers.Raw(buffers.BootValue()) != "-1" { t.Fatal("buffer auto default") }
	// The provider's borrowed descriptor copy cannot mutate registry metadata.
	compression,_ := r.Lookup("wal_compression"); buffers,_ = r.Lookup("wal_buffers")
	if compression.EnumValues[0] != "pglz" || buffers.NativeStartup.Value != "512" { t.Fatal("provider owns registry metadata") }
}

type walDefaultProvider struct {
	values physicalDefaultProvider
	calls map[string]int
}
func (p *walDefaultProvider) SessionDefault(_ context.Context,d settings.Definition) (string,bool,error) {
	if p.calls == nil { p.calls = map[string]int{} }; p.calls[d.Name]++
	if d.Name == "wal_compression" { d.EnumValues[0] = "corrupted" }
	if d.Name == "wal_buffers" { d.NativeStartup.Value = "1024" }
	value,present := p.values[d.Name]; return value,present,nil
}
