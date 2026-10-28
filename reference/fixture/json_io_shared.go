package fixture

import (
	"database/sql/driver"
	"strings"
)

type JSONReferenceValuer string

func (v JSONReferenceValuer) Value() (driver.Value, error) { return string(v), nil }

func JSONReferenceQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
