package engine

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

var clientMinMessages = SettingDefinition{Name: "client_min_messages", Type: SettingEnum, BootDefault: "notice", Description: "Sets the message levels that are sent to the client.", EnumValues: []string{"debug5", "debug4", "debug3", "debug2", "debug1", "log", "notice", "warning", "error"}, Normalize: normalizeClientMinMessages}

func normalizeClientMinMessages(value string) (string, *sqlstate.Error, error) {
	if !utf8.ValidString(value) {
		return "", nil, errors.New("stuntgres: invalid UTF-8 setting")
	}
	normalized := strings.ToLower(value)
	switch normalized {
	case "debug":
		return "debug2", nil, nil
	case "debug5", "debug4", "debug3", "debug2", "debug1", "log", "info", "notice", "warning", "error":
		return normalized, nil, nil
	default:
		return "", nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "client_min_messages": "%s"`, value), Hint: "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error."}
	}
}

// messageLevels lists client message levels from least to most severe.
var messageLevels = [...]string{"debug5", "debug4", "debug3", "debug2", "debug1", "log", "info", "notice", "warning", "error"}

// messageLevelRank returns a level's index in messageLevels; "debug" means debug2.
func messageLevelRank(name string) (int, bool) {
	if name == "debug" {
		name = "debug2"
	}
	for rank, level := range messageLevels {
		if level == name {
			return rank, true
		}
	}
	return 0, false
}
