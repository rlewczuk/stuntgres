package session

import (
	"testing"

	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
)

func TestReplicationSettingsProviderDefaults(t *testing.T) {
	r := settings.NewRegistry(nil, false)
	capture := func(p settings.SessionDefaultProvider) (*SettingsStore, error) {
		s := &SettingsStore{Defaults: map[string]string{}, Values: map[string]string{}}
		err := s.Initialize(t.Context(), r.Definitions(), r.Order(), p)
		return s, err
	}
	for _, tc := range []struct{ name, equivalent, changed string }{
		{"hot_standby", "true", "off"}, {"hot_standby_feedback", "false", "on"},
		{"idle_replication_slot_timeout", "0s", "1s"}, {"max_active_replication_origins", "0xA", "11"},
		{"max_logical_replication_workers", "4", "5"}, {"max_parallel_apply_workers_per_subscription", "2", "3"},
		{"max_replication_slots", "10", "11"}, {"max_slot_wal_keep_size", "-1", "0"},
		{"max_standby_archive_delay", "30000ms", "1s"}, {"max_standby_streaming_delay", "30s", "1s"},
		{"max_sync_workers_per_subscription", "2", "3"}, {"max_wal_senders", "10", "11"},
		{"primary_conninfo", "", "host=absent"}, {"primary_slot_name", "", "other"},
		{"recovery_min_apply_delay", "0ms", "1ms"}, {"sync_replication_slots", "false", "true"},
		{"synchronized_standby_slots", "", "other"}, {"synchronous_standby_names", "", "other"},
		{"track_commit_timestamp", "false", "true"}, {"wal_keep_size", "0MB", "1GB"},
		{"wal_receiver_create_temp_slot", "false", "true"}, {"wal_receiver_status_interval", "10000ms", "11s"},
		{"wal_receiver_timeout", "1min", "2min"}, {"wal_retrieve_retry_interval", "5000ms", "6s"},
	} {
		if _, err := capture(physicalDefaultProvider{tc.name: tc.equivalent}); err != nil {
			t.Fatal("normalized immutable", tc.name, err)
		}
		if _, err := capture(physicalDefaultProvider{tc.name: tc.changed}); err == nil || err.Error() != "stuntgres: default provider cannot change "+tc.name {
			t.Fatal("changed immutable", tc.name, err)
		}
	}
	p := physicalDefaultProvider{"wal_sender_timeout": "2s", "output_plugin_libraries": "provider_plugin"}
	old, err := capture(p)
	if err != nil { t.Fatal(err) }
	p["wal_sender_timeout"], p["output_plugin_libraries"] = "3s", "replacement_plugin"
	old.Values["wal_sender_timeout"], old.Values["output_plugin_libraries"] = "4s", "override"
	old.ResetAllLocked(r)
	for name, value := range map[string]string{"wal_sender_timeout": "2s", "output_plugin_libraries": "provider_plugin"} {
		if old.Value(name) != value || !old.ProviderDefaults[name] { t.Fatal("captured default/source",name,old.Value(name)) }
		if old.Defaults[name] != value { t.Fatal("DEFAULT changed",name,old.Defaults[name]) }
	}
	fresh, err := capture(p)
	if err != nil || fresh.Value("wal_sender_timeout") != "3s" || fresh.Value("output_plugin_libraries") != "replacement_plugin" || old.Value("wal_sender_timeout") != "2s" { t.Fatal("fresh physical capture",err) }
	for _, name := range []string{"output_plugin_libraries", "synchronous_standby_names", "synchronized_standby_slots"} {
		input, err := SetInput(nil, &parser.Set{Name: parser.Identifier{Text: name}, Values: []string{"a", "b"}}, false)
		if err != nil || input == nil || *input != "a, b" { t.Fatal(name,input,err) }
	}
}
