package settings

// pgStatStatementsDefinitions is the stock startup-activated logical module.
// SQL installation is database-local and never owns recognition or values.
var pgStatStatementsDefinitions = []Definition{
	{Name: "pg_stat_statements.track", Description: "Selects which statements are tracked by pg_stat_statements.", Category: "Customized Options", Type: Enum, Access: Privileged, BootDefault: "top", EnumValues: []string{"none", "top", "all"}},
	{Name: "pg_stat_statements.track_utility", Description: "Selects whether utility commands are tracked by pg_stat_statements.", Category: "Customized Options", Type: Boolean, Access: Privileged, BootDefault: "on"},
	{Name: "pg_stat_statements.track_planning", Description: "Selects whether planning duration is tracked by pg_stat_statements.", Category: "Customized Options", Type: Boolean, Access: Privileged, BootDefault: "off"},
	{Name: "pg_stat_statements.max", Description: "Sets the maximum number of statements tracked by pg_stat_statements.", Category: "Customized Options", Type: Integer, Access: StartupOnly, BootDefault: "5000", FixedDefault: true, IntegerMin: 100, IntegerMax: 1073741823},
	{Name: "pg_stat_statements.save", Description: "Save pg_stat_statements statistics across server shutdowns.", Category: "Customized Options", Type: Boolean, Access: Sighup, BootDefault: "on"},
}
