package engine

import (
	"stuntgres/internal/parser"
)

func ddlHostName(parts ...string) parser.ObjectName {
	name := parser.ObjectName{}
	for _, part := range parts {
		name.Parts = append(name.Parts, parser.Identifier{Text: part})
	}
	return name
}
