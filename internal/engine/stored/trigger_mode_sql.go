package stored

import "stuntgres/internal/engine/catalog"

func TriggerModeSQL(relation, name string, mode catalog.TriggerMode) string {
	action := "ENABLE"
	switch mode.Normalized() {
	case catalog.TriggerDisabled:
		action = "DISABLE"
	case catalog.TriggerReplica:
		action = "ENABLE REPLICA"
	case catalog.TriggerAlways:
		action = "ENABLE ALWAYS"
	}
	return "ALTER TABLE ONLY " + relation + " " + action + " TRIGGER " + catalog.QuoteIdentifier(name) + ";\n"
}
