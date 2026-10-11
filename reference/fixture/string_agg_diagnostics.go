package fixture

import (
	"strings"
	"unicode/utf8"
)

func StringAggPosition(q, token string, last bool) string {
	i := strings.Index(q, token)
	if last {
		i = strings.LastIndex(q, token)
	}
	if i < 0 {
		panic("missing diagnostic token")
	}
	return Position(utf8.RuneCountInString(q[:i]) + 1)
}
