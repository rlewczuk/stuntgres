package view

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// triggerTransitions checks ordered declarations before OF column or routine
// lookup. Semantic declaration errors deliberately have no source position.
func triggerTransitions(p *stored.TriggerParts, relation *catalog.Relation, s *parser.CreateTrigger) error {
	for _, transition := range s.Transitions {
		if transition.Row {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "ROW variable naming in the REFERENCING clause is not supported", Hint: "Use OLD TABLE or NEW TABLE for naming transition tables."}
		}
		kind := ""
		if relation.RelationKind == catalog.ForeignRelation {
			kind = "foreign table"
		} else if relation.RelationKind == catalog.ViewRelation {
			kind = "view"
		}
		if kind != "" {
			detail := "Triggers on foreign tables cannot have transition tables."
			if kind == "view" {
				detail = "Triggers on views cannot have transition tables."
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf("%q is a %s", relation.Name(), kind), Detail: detail}
		}
		if s.Row {
			if relation.Membership != nil {
				return sqlError("0A000", "ROW triggers with transition tables are not supported on partitions")
			}
			if relation.Inheritance != nil && len(relation.Inheritance.Parents) != 0 {
				return sqlError("0A000", "ROW triggers with transition tables are not supported on inheritance children")
			}
		}
		if s.Timing != "after" {
			return sqlError("42P17", "transition table name can only be specified for an AFTER trigger")
		}
		for _, event := range s.Events {
			if event.Name == "truncate" {
				return sqlError("0A000", "TRUNCATE triggers with transition tables are not supported")
			}
		}
		if len(s.Events) != 1 {
			return sqlError("0A000", "transition tables cannot be specified for triggers with more than one event")
		}
		event := s.Events[0]
		if len(event.Columns) != 0 {
			return sqlError("0A000", "transition tables cannot be specified for triggers with column lists")
		}
		if transition.New {
			if event.Name != "insert" && event.Name != "update" {
				return sqlError("42P17", "NEW TABLE can only be specified for an INSERT or UPDATE trigger")
			}
			if p.NewTable != "" {
				return sqlError("42P17", "NEW TABLE cannot be specified multiple times")
			}
			p.NewTable = transition.Alias.Text
		} else {
			if event.Name != "delete" && event.Name != "update" {
				return sqlError("42P17", "OLD TABLE can only be specified for a DELETE or UPDATE trigger")
			}
			if p.OldTable != "" {
				return sqlError("42P17", "OLD TABLE cannot be specified multiple times")
			}
			p.OldTable = transition.Alias.Text
		}
	}
	if p.OldTable != "" && p.OldTable == p.NewTable {
		return sqlError("42P17", "OLD TABLE name and NEW TABLE name cannot be the same")
	}
	return nil
}
