package settings

// AutoExplainDefinitions borrows immutable physical-module metadata. Loading
// installs these definitions in one session, never in the shared registry.
func AutoExplainDefinitions() []Definition { return autoExplainDefinitions }

var autoExplainDefinitions = func() []Definition {
	definitions := []Definition{
		{Name: "auto_explain.log_min_duration", Description: "Sets the minimum execution time above which plans will be logged.", ExtraDescription: "-1 disables logging plans. 0 means log all plans.", Type: Integer, Unit: Milliseconds, BootDefault: "-1", IntegerMin: -1, IntegerMax: 2147483647},
		{Name: "auto_explain.log_parameter_max_length", Description: "Sets the maximum length of query parameter values to log.", ExtraDescription: "-1 means log values in full.", Type: Integer, Unit: Bytes, BootDefault: "-1", IntegerMin: -1, IntegerMax: 2147483647},
		{Name: "auto_explain.log_analyze", Description: "Use EXPLAIN ANALYZE for plan logging.", Type: Boolean, BootDefault: "off"},
		{Name: "auto_explain.log_verbose", Description: "Use EXPLAIN VERBOSE for plan logging.", Type: Boolean, BootDefault: "off"},
		{Name: "auto_explain.log_buffers", Description: "Log buffers usage.", Type: Boolean, BootDefault: "off"},
		{Name: "auto_explain.log_wal", Description: "Log WAL usage.", Type: Boolean, BootDefault: "off"},
		{Name: "auto_explain.log_timing", Description: "Collect timing data, not just row counts.", Type: Boolean, BootDefault: "on"},
		{Name: "auto_explain.log_triggers", Description: "Include trigger statistics in plans.", ExtraDescription: "This has no effect unless log_analyze is also set.", Type: Boolean, BootDefault: "off"},
		{Name: "auto_explain.log_nested_statements", Description: "Log nested statements.", Type: Boolean, BootDefault: "off"},
		{Name: "auto_explain.log_format", Description: "EXPLAIN format to be used for plan logging.", Type: Enum, BootDefault: "text", EnumValues: []string{"text", "xml", "json", "yaml"}},
		{Name: "auto_explain.sample_rate", Description: "Fraction of queries to process.", Type: Floating, BootDefault: "1", FloatMin: 0, FloatMax: 1},
		{Name: "auto_explain.log_level", Description: "Log level for the plan.", Type: Enum, BootDefault: "log", EnumValues: []string{"debug5", "debug4", "debug3", "debug2", "debug1", "info", "notice", "warning", "log"}},
	}
	for i := range definitions {
		definitions[i].Access = Privileged
		definitions[i].Category = "Customized Options"
		definitions[i].bootValue, _ = definitions[i].Parse(definitions[i].BootDefault)
	}
	return definitions
}()
