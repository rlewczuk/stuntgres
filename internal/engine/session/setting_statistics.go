package session

import (
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqlstate"
)

// checkStatistics is a bool check hook, not a global state invariant. Only
// explicit assignments run it; cached resets and journal restoration bypass it.
// Caller owns Settings.Mu. Effective peers include captured provider defaults.
func (s *SettingsStore) checkStatistics(name string, value *settings.Value) error {
	if value == nil || !value.Boolean {
		return nil
	}
	var detail string
	switch name {
	case "log_statement_stats":
		if !s.TypedValue("log_parser_stats").Boolean && !s.TypedValue("log_planner_stats").Boolean && !s.TypedValue("log_executor_stats").Boolean {
			return nil
		}
		detail = `Cannot enable "log_statement_stats" when "log_parser_stats", "log_planner_stats", or "log_executor_stats" is true.`
	case "log_parser_stats", "log_planner_stats", "log_executor_stats":
		if !s.TypedValue("log_statement_stats").Boolean {
			return nil
		}
		detail = `Cannot enable parameter when "log_statement_stats" is true.`
	default:
		return nil
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: `invalid value for parameter "` + name + `": 1`, Detail: detail}
}
