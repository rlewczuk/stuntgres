package settings

import (
	"fmt"
	"strings"

	"stuntgres/internal/sqlstate"
)

func normalizeCreateroleSelfGrant(input string) (string, *sqlstate.Error, error) {
	return normalizeKeywordList("createrole_self_grant", input, "set", "inherit")
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
