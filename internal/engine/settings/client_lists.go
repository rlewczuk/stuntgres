package settings

import (
	"fmt"
	"strings"

	"stuntgres/internal/sqlstate"
)

func normalizeCreateroleSelfGrant(input string) (string, *sqlstate.Error, error) {
	v, err := ParseCreateroleSelfGrant(input)
	return v.Text, nil, err
}

// ParseCreateroleSelfGrant preserves presentation while caching the options.
// Validate the complete list before interpreting any keyword.
func ParseCreateroleSelfGrant(input string) (Value, error) {
	items, err := ParseIdentifierList("createrole_self_grant", input)
	if err != nil {
		return Value{}, err
	}
	v := Value{Text: input}
	for _, item := range items {
		switch {
		case strings.EqualFold(item, "set"):
			v.SelfGrant |= SelfGrantSet
		case strings.EqualFold(item, "inherit"):
			v.SelfGrant |= SelfGrantInherit
		default:
			return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "createrole_self_grant": "%s"`, input), Detail: fmt.Sprintf(`Unrecognized key word: "%s".`, item)}
		}
	}
	return v, nil
}

func normalizeRestrictedRelationKind(input string) (string, *sqlstate.Error, error) {
	return normalizeKeywordList("restrict_nonsystem_relation_kind", input, "view", "foreign-table")
}

func normalizeKeywordList(parameter, input string, first, second string) (string, *sqlstate.Error, error) {
	items, err := ParseIdentifierList(parameter, input)
	if err != nil {
		return "", nil, err
	}
	for _, item := range items {
		if !strings.EqualFold(item, first) && !strings.EqualFold(item, second) {
			return "", nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "%s": "%s"`, parameter, input), Detail: fmt.Sprintf(`Unrecognized key word: "%s".`, item)}
		}
	}
	return input, nil, nil
}
