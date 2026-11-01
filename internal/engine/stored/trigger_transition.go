package stored

import "strings"

// writeTriggerTransitions shares only declaration spelling between display and
// native post-data replay; neither path captures invocation relations or rows.
func writeTriggerTransitions(sql *strings.Builder, p TriggerParts, quote func(string) string) {
	if p.OldTable == "" && p.NewTable == "" {
		return
	}
	sql.WriteString(" REFERENCING")
	if p.OldTable != "" {
		sql.WriteString(" OLD TABLE AS ")
		sql.WriteString(quote(p.OldTable))
	}
	if p.NewTable != "" {
		sql.WriteString(" NEW TABLE AS ")
		sql.WriteString(quote(p.NewTable))
	}
}
