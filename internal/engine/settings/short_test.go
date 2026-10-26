package settings

import "testing"

func TestShortSettingsDescriptors(t *testing.T) {
	registry := NewRegistry(nil, false)
	cases := []struct{ name, description, category, initial string; access Access }{
		{"enable_bitmapscan", "Enables the planner's use of bitmap-scan plans.", "Query Tuning / Planner Method Configuration", "on", Session},
		{"enable_hashjoin", "Enables the planner's use of hash join plans.", "Query Tuning / Planner Method Configuration", "on", Session},
		{"enable_indexscan", "Enables the planner's use of index-scan plans.", "Query Tuning / Planner Method Configuration", "on", Session},
		{"enable_mergejoin", "Enables the planner's use of merge join plans.", "Query Tuning / Planner Method Configuration", "on", Session},
		{"enable_nestloop", "Enables the planner's use of nested-loop join plans.", "Query Tuning / Planner Method Configuration", "on", Session},
		{"enable_indexonlyscan", "Enables the planner's use of index-only-scan plans.", "Query Tuning / Planner Method Configuration", "on", Session},
		{"enable_seqscan", "Enables the planner's use of sequential-scan plans.", "Query Tuning / Planner Method Configuration", "on", Session},
		{"enable_tidscan", "Enables the planner's use of TID scan plans.", "Query Tuning / Planner Method Configuration", "on", Session},
		{"fsync", "Forces synchronization of updates to disk.", "Write-Ahead Log / Settings", "on", Sighup},
		{"log_parser_stats", "Writes parser performance statistics to the server log.", "Statistics / Monitoring", "off", Privileged},
		{"log_planner_stats", "Writes planner performance statistics to the server log.", "Statistics / Monitoring", "off", Privileged},
		{"log_executor_stats", "Writes executor performance statistics to the server log.", "Statistics / Monitoring", "off", Privileged},
	}
	for _, tc := range cases {
		d, err := registry.Lookup(tc.name)
		if err != nil { t.Fatal(err) }
		if d.Type != Boolean || d.Access != tc.access || d.Description != tc.description || d.Category != tc.category || d.BootDefault != tc.initial || d.ReadRestricted || d.Unit != UnitNone || len(d.EnumValues) != 0 || d.IntegerMin != 0 || d.IntegerMax != 0 || d.FloatMin != 0 || d.FloatMax != 0 { t.Fatalf("%s: %+v", tc.name, d) }
		extra := ""
		if tc.name == "fsync" { extra = "The server will use the fsync() system call in several places to make sure that updates are physically written to disk. This ensures that a database cluster will recover to a consistent state after an operating system or hardware crash." }
		if d.ExtraDescription != extra { t.Fatal(tc.name, d.ExtraDescription) }
		count := 0; for _, name := range registry.Order() { if name == tc.name { count++ } }; if count != 1 { t.Fatal(tc.name, count) }
		for _, v := range []struct{ input, want string }{{"TRUE", "on"}, {"0", "off"}} {
			parsed, err := d.Parse(v.input); if err != nil || d.Format(parsed) != v.want || d.Raw(parsed) != v.want { t.Fatal(tc.name, parsed, err) }
		}
	}
}
