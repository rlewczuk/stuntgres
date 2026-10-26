package settings

const (
	vacuumAutomatic = "Vacuuming / Automatic Vacuuming"
	vacuumCost = "Vacuuming / Cost-Based Vacuum Delay"
	vacuumDefault = "Vacuuming / Default Behavior"
	vacuumFreezing = "Vacuuming / Freezing"
)

// Vacuum settings describe setting-only physical simulations, without hooks.
var vacuumBuiltins = []Definition{
	{Name: "autovacuum", Type: Boolean, Access: Sighup, BootDefault: "on", Category: vacuumAutomatic, Description: "Starts the autovacuum subprocess."},
	{Name: "autovacuum_analyze_scale_factor", Type: Floating, Access: Sighup, BootDefault: "0.1", FloatMin: 0, FloatMax: 100, Category: vacuumAutomatic, Description: "Number of tuple inserts, updates, or deletes prior to analyze as a fraction of reltuples."},
	{Name: "autovacuum_analyze_threshold", Type: Integer, Access: Sighup, BootDefault: "50", IntegerMin: 0, IntegerMax: 2147483647, Category: vacuumAutomatic, Description: "Minimum number of tuple inserts, updates, or deletes prior to analyze."},
	{Name: "autovacuum_freeze_max_age", Type: Integer, Access: StartupOnly, BootDefault: "200000000", IntegerMin: 100000, IntegerMax: 2000000000, Category: vacuumAutomatic, Description: "Age at which to autovacuum a table to prevent transaction ID wraparound."},
	{Name: "autovacuum_max_workers", Type: Integer, Access: Sighup, BootDefault: "3", IntegerMin: 1, IntegerMax: 262143, Category: vacuumAutomatic, Description: "Sets the maximum number of simultaneously running autovacuum worker processes."},
	{Name: "autovacuum_multixact_freeze_max_age", Type: Integer, Access: StartupOnly, BootDefault: "400000000", IntegerMin: 10000, IntegerMax: 2000000000, Category: vacuumAutomatic, Description: "Multixact age at which to autovacuum a table to prevent multixact wraparound."},
	{Name: "autovacuum_naptime", Type: Integer, Access: Sighup, Unit: Seconds, BootDefault: "60", IntegerMin: 1, IntegerMax: 2147483, Category: vacuumAutomatic, Description: "Time to sleep between autovacuum runs."},
	{Name: "autovacuum_vacuum_cost_delay", Type: Floating, Access: Sighup, Unit: Milliseconds, BootDefault: "2", FloatMin: -1, FloatMax: 100, Category: vacuumAutomatic, Description: "Vacuum cost delay in milliseconds, for autovacuum.", ExtraDescription: "-1 means use \"vacuum_cost_delay\"."},
	{Name: "autovacuum_vacuum_cost_limit", Type: Integer, Access: Sighup, BootDefault: "-1", IntegerMin: -1, IntegerMax: 10000, Category: vacuumAutomatic, Description: "Vacuum cost amount available before napping, for autovacuum.", ExtraDescription: "-1 means use \"vacuum_cost_limit\"."},
	{Name: "autovacuum_vacuum_insert_scale_factor", Type: Floating, Access: Sighup, BootDefault: "0.2", FloatMin: 0, FloatMax: 100, Category: vacuumAutomatic, Description: "Number of tuple inserts prior to vacuum as a fraction of reltuples."},
	{Name: "autovacuum_vacuum_insert_threshold", Type: Integer, Access: Sighup, BootDefault: "1000", IntegerMin: -1, IntegerMax: 2147483647, Category: vacuumAutomatic, Description: "Minimum number of tuple inserts prior to vacuum.", ExtraDescription: "-1 disables insert vacuums."},
	{Name: "autovacuum_vacuum_max_threshold", Type: Integer, Access: Sighup, BootDefault: "100000000", IntegerMin: -1, IntegerMax: 2147483647, Category: vacuumAutomatic, Description: "Maximum number of tuple updates or deletes prior to vacuum.", ExtraDescription: "-1 disables the maximum threshold."},
	{Name: "autovacuum_vacuum_scale_factor", Type: Floating, Access: Sighup, BootDefault: "0.2", FloatMin: 0, FloatMax: 100, Category: vacuumAutomatic, Description: "Number of tuple updates or deletes prior to vacuum as a fraction of reltuples."},
	{Name: "autovacuum_vacuum_threshold", Type: Integer, Access: Sighup, BootDefault: "50", IntegerMin: 0, IntegerMax: 2147483647, Category: vacuumAutomatic, Description: "Minimum number of tuple updates or deletes prior to vacuum."},
	{Name: "autovacuum_worker_slots", Type: Integer, Access: StartupOnly, BootDefault: "16", IntegerMin: 1, IntegerMax: 262143, Category: vacuumAutomatic, Description: "Sets the number of backend slots to allocate for autovacuum workers."},
	{Name: "vacuum_cost_delay", Type: Floating, Unit: Milliseconds, BootDefault: "0", FloatMin: 0, FloatMax: 100, Category: vacuumCost, Description: "Vacuum cost delay in milliseconds."},
	{Name: "vacuum_cost_limit", Type: Integer, BootDefault: "200", IntegerMin: 1, IntegerMax: 10000, Category: vacuumCost, Description: "Vacuum cost amount available before napping."},
	{Name: "vacuum_cost_page_dirty", Type: Integer, BootDefault: "20", IntegerMin: 0, IntegerMax: 10000, Category: vacuumCost, Description: "Vacuum cost for a page dirtied by vacuum."},
	{Name: "vacuum_cost_page_hit", Type: Integer, BootDefault: "1", IntegerMin: 0, IntegerMax: 10000, Category: vacuumCost, Description: "Vacuum cost for a page found in the buffer cache."},
	{Name: "vacuum_cost_page_miss", Type: Integer, BootDefault: "2", IntegerMin: 0, IntegerMax: 10000, Category: vacuumCost, Description: "Vacuum cost for a page not found in the buffer cache."},
	{Name: "vacuum_failsafe_age", Type: Integer, BootDefault: "1600000000", IntegerMin: 0, IntegerMax: 2100000000, Category: vacuumFreezing, Description: "Age at which VACUUM should trigger failsafe to avoid a wraparound outage."},
	{Name: "vacuum_freeze_min_age", Type: Integer, BootDefault: "50000000", IntegerMin: 0, IntegerMax: 1000000000, Category: vacuumFreezing, Description: "Minimum age at which VACUUM should freeze a table row."},
	{Name: "vacuum_freeze_table_age", Type: Integer, BootDefault: "150000000", IntegerMin: 0, IntegerMax: 2000000000, Category: vacuumFreezing, Description: "Age at which VACUUM should scan whole table to freeze tuples."},
	{Name: "vacuum_max_eager_freeze_failure_rate", Type: Floating, BootDefault: "0.03", FloatMin: 0, FloatMax: 1, Category: vacuumFreezing, Description: "Fraction of pages in a relation vacuum can scan and fail to freeze before disabling eager scanning.", ExtraDescription: "A value of 0.0 disables eager scanning and a value of 1.0 will eagerly scan up to 100 percent of the all-visible pages in the relation. If vacuum successfully freezes these pages, the cap is lower than 100 percent, because the goal is to amortize page freezing across multiple vacuums."},
	{Name: "vacuum_multixact_failsafe_age", Type: Integer, BootDefault: "1600000000", IntegerMin: 0, IntegerMax: 2100000000, Category: vacuumFreezing, Description: "Multixact age at which VACUUM should trigger failsafe to avoid a wraparound outage."},
	{Name: "vacuum_multixact_freeze_min_age", Type: Integer, BootDefault: "5000000", IntegerMin: 0, IntegerMax: 1000000000, Category: vacuumFreezing, Description: "Minimum age at which VACUUM should freeze a MultiXactId in a table row."},
	{Name: "vacuum_multixact_freeze_table_age", Type: Integer, BootDefault: "150000000", IntegerMin: 0, IntegerMax: 2000000000, Category: vacuumFreezing, Description: "Multixact age at which VACUUM should scan whole table to freeze tuples."},
	{Name: "vacuum_truncate", Type: Boolean, BootDefault: "on", Category: vacuumDefault, Description: "Enables vacuum to truncate empty pages at the end of the table."},
}
