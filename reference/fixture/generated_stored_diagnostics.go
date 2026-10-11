package fixture

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func GeneratedStoredPosition(q, token string) string {
	return fmt.Sprint(utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1)
}
