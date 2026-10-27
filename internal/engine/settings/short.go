package settings

// Logging short-option counterparts retain their ordinary SQL descriptors.
var shortBuiltins = []Definition{
	{Name: "log_parser_stats", Type: Boolean, Access: Privileged, BootDefault: "off", Category: "Statistics / Monitoring", Description: "Writes parser performance statistics to the server log."},
	{Name: "log_planner_stats", Type: Boolean, Access: Privileged, BootDefault: "off", Category: "Statistics / Monitoring", Description: "Writes planner performance statistics to the server log."},
	{Name: "log_executor_stats", Type: Boolean, Access: Privileged, BootDefault: "off", Category: "Statistics / Monitoring", Description: "Writes executor performance statistics to the server log."},
}
