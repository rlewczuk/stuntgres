package stored

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type RuleAction struct {
	syntax                parser.Statement
	binding               analyze.Binding
	presentation          *View
	modifierPresentations map[parser.Statement]*View
}

type Rule struct {
	OID                   ids.OID
	Name                  string
	event                 string
	instead               bool
	condition             expr.Expr
	conditionPresentation *View
	conditionBinding      *analyze.Binding
	actions               []RuleAction
	references            []ids.ObjectAddress
}

func RuleEvent(stmt parser.Statement) string {
	switch stmt.(type) {
	case *parser.Insert:
		return "INSERT"
	case *parser.Update:
		return "UPDATE"
	case *parser.Delete:
		return "DELETE"
	case *parser.Select:
		return "SELECT"
	}
	return ""
}

func ruleEventCode(event string) string {
	switch event {
	case "SELECT":
		return "1"
	case "UPDATE":
		return "2"
	case "INSERT":
		return "3"
	case "DELETE":
		return "4"
	}
	return ""
}
