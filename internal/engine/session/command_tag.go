package session

import (
	"strings"

	"stuntgres/internal/parser"
)

// ForeignDDLTag is the command tag of a foreign-data DDL statement.
func ForeignDDLTag(s *parser.ForeignDDL) string {
	if s.Action == "import" {
		return "IMPORT FOREIGN SCHEMA"
	}
	kind := "FOREIGN TABLE"
	switch s.Kind {
	case "wrapper":
		kind = "FOREIGN DATA WRAPPER"
	case "server":
		kind = "SERVER"
	case "mapping":
		kind = "USER MAPPING"
	}
	return strings.ToUpper(s.Action) + " " + kind
}
