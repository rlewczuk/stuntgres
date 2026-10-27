package session

import (
	"context"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
)

type resourceDefaults struct {
	values map[string]string
	calls  map[string]int
}

func (p resourceDefaults) SessionDefault(_ context.Context, d settings.Definition) (string, bool, error) {
	p.calls[d.Name]++
	v, ok := p.values[d.Name]
	return v, ok, nil
}

func TestResourceSettingsStartupDefaults(t *testing.T) {
	r := settings.NewRegistry(nil, false)
	capture := func(p settings.SessionDefaultProvider) (*SettingsStore, error) {
		s := &SettingsStore{Defaults: map[string]string{}, Values: map[string]string{}}
		err := s.Initialize(t.Context(), r.Definitions(), r.Order(), p)
		return s, err
	}
	for _, tc := range []struct{ name, equivalent, changed string }{
		{"autovacuum_work_mem", "-1", "64"}, {"bgwriter_delay", "0.2s", "201"}, {"bgwriter_flush_after", "512kB", "0"}, {"bgwriter_lru_maxpages", "100", "0"}, {"bgwriter_lru_multiplier", "2.0", "1.5"},
		{"commit_timestamp_buffers", "256kB", "16"}, {"dynamic_shared_memory_type", "POSIX", "mmap"}, {"file_extend_method", "POSIX_FALLOCATE", "write_zeros"}, {"huge_page_size", "0MB", "2048"}, {"huge_pages", "TRY", "on"},
		{"io_max_combine_limit", "128kB", "128"}, {"io_max_concurrency", "64", "1"}, {"io_method", "WORKER", "sync"}, {"io_workers", "3", "1"}, {"max_files_per_process", "1000", "64"}, {"max_notify_queue_pages", "1048576", "64"},
		{"max_prepared_transactions", "0", "100"}, {"max_worker_processes", "8", "0"}, {"min_dynamic_shared_memory", "0MB", "1"}, {"multixact_member_buffers", "256kB", "16"}, {"multixact_offset_buffers", "128kB", "32"}, {"notify_buffers", "128kB", "32"},
		{"serializable_buffers", "256kB", "16"}, {"shared_buffers", "128MB", "16"}, {"shared_memory_type", "MMAP", "sysv"}, {"subtransaction_buffers", "256kB", "16"}, {"transaction_buffers", "256kB", "16"},
	} {
		if _, err := capture(physicalDefaultProvider{tc.name: tc.equivalent}); err != nil {
			t.Fatal("equivalent immutable", tc, err)
		}
		if _, err := capture(physicalDefaultProvider{tc.name: tc.changed}); err == nil || err.Error() != "stuntgres: default provider cannot change "+tc.name {
			t.Fatal("changed immutable", tc, err)
		}
	}
	p := resourceDefaults{map[string]string{"work_mem": "1024kB", "max_stack_depth": "1024", "temp_buffers": "2048", "temp_file_limit": "2048", "hash_mem_multiplier": "1.25"}, map[string]int{}}
	old, err := capture(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range r.Order() {
		if p.calls[name] != 1 {
			t.Fatal("provider call count", name, p.calls[name])
		}
	}
	p.values["work_mem"] = "2048"
	p.values["temp_buffers"] = "3072"
	old.Values["work_mem"] = "8MB"
	old.Values["temp_file_limit"] = "4MB"
	old.ResetAllLocked(r)
	for name, want := range map[string]string{"work_mem": "1MB", "max_stack_depth": "1MB", "temp_buffers": "16MB", "temp_file_limit": "2MB", "hash_mem_multiplier": "1.25"} {
		if old.Value(name) != want || !old.ProviderDefaults[name] || p.calls[name] != 1 {
			t.Fatal("captured default", name, old.Value(name), p.calls[name])
		}
	}
	fresh, err := capture(p)
	if err != nil || fresh.Value("work_mem") != "2MB" || fresh.Value("temp_buffers") != "24MB" || old.Value("work_mem") != "1MB" {
		t.Fatal("new physical capture", err)
	}
	for _, tc := range []struct{ name, input string }{{"max_stack_depth", "7681"}, {"vacuum_buffer_usage_limit", "127"}, {"io_max_concurrency", "0"}, {"transaction_buffers", "17"}} {
		if _, err := capture(physicalDefaultProvider{tc.name: tc.input}); err == nil {
			t.Fatal("provider bypassed pure validator", tc)
		}
	}
}

func TestResourceSettingsTempInitialization(t *testing.T) {
	r := settings.NewRegistry(nil, false)
	s := &SettingsStore{Defaults: map[string]string{}, Values: map[string]string{}}
	if err := s.Initialize(t.Context(), r.Definitions(), r.Order(), physicalDefaultProvider{"temp_buffers": "2048"}); err != nil {
		t.Fatal(err)
	}
	s.InitializeTemporaryBuffers()
	if s.temporaryBuffers != 2048 {
		t.Fatal("did not capture typed default", s.temporaryBuffers)
	}
	s.Touch("temp_buffers", SettingLocal)
	s.Typed["temp_buffers"] = settings.Value{Number: 3072}
	s.Values["temp_buffers"] = "24MB"
	s.InitializeTemporaryBuffers()
	s.FinishTransaction(false)
	if s.temporaryBuffers != 2048 || s.Value("temp_buffers") != "16MB" {
		t.Fatal("rollback reset initialization", s.temporaryBuffers, s.Value("temp_buffers"))
	}
	s.Values["temp_buffers"] = "8MB"
	s.Typed["temp_buffers"] = settings.Value{Number: 1024}
	s.ResetAllLocked(r)
	s.InitializeTemporaryBuffers()
	if s.temporaryBuffers != 2048 {
		t.Fatal("reset recaptured initialized count")
	}
	s.EndSession(ids.SessionToken{}, 0)
	s.InitializeTemporaryBuffers()
	if s.temporaryBuffers != 0 || s.DefaultTyped != nil {
		t.Fatal("ended physical session retained/recreated initialization")
	}
	for _, name := range []string{"max_stack_depth", "temp_file_limit"} {
		d, _ := r.Lookup(name)
		called := false
		if err := SettingAccess(d, func(got string) (bool, error) {
			called = true
			if got != name {
				t.Fatal(got)
			}
			return true, nil
		}); err != nil || !called {
			t.Fatal("missing parameter grant eligibility", name, err)
		}
	}
}
