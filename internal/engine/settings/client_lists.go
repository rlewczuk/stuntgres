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
	v, err := ParseRestrictedRelationKinds(input)
	return v.Text, nil, err
}

// ParseRestrictedRelationKinds validates the entire list before decoding flags.
func ParseRestrictedRelationKinds(input string) (Value, error) {
	items, err := ParseIdentifierList("restrict_nonsystem_relation_kind", input)
	if err != nil {
		return Value{}, err
	}
	v := Value{Text: input}
	for _, item := range items {
		switch {
		case strings.EqualFold(item, "view"):
			v.RestrictedRelations |= RestrictView
		case strings.EqualFold(item, "foreign-table"):
			v.RestrictedRelations |= RestrictForeignTable
		default:
			return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf(`invalid value for parameter "restrict_nonsystem_relation_kind": "%s"`, input), Detail: fmt.Sprintf(`Unrecognized key word: "%s".`, item)}
		}
	}
	return v, nil
}
