package table

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func deparseRewrite(c *exec.Context, in DeparseInputs, signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor, renderer stored.Deparser) (sqltype.Value, error) {
	if err := c.Context.Err(); err != nil {
		return sqltype.Value{}, err
	}
	oid, err := values[0].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	trigger := signature == functions.DeparseTrigger || signature == functions.DeparseTriggerPretty
	presentation := stored.Presentation{Deparse: true, Pretty: renderer.Pretty, Path: in.Path, Output: in.Output, NonstandardStrings: in.NonstandardStrings, TemporaryOwner: in.TemporaryOwner}
	for _, relation := range in.Catalog.Relations() {
		if err := c.Context.Err(); err != nil {
			return sqltype.Value{}, err
		}
		if trigger {
			if relation.Triggers != nil {
				if _, ok := relation.Triggers.(stored.TriggerList); !ok {
					return sqltype.Value{}, fmt.Errorf("stuntgres: invalid trigger payload")
				}
			}
			for _, definition := range stored.TriggersOf(relation) {
				if definition.OID() == ids.OID(oid) {
					text, err := stored.TriggerDefinitionSQL(c.Context, relation, definition, renderer)
					if err != nil {
						return sqltype.Value{}, err
					}
					return sqltype.String(result, text), nil
				}
			}
			continue
		}
		if relation.ReturnRuleOID() != 0 && relation.ReturnRuleOID() == ids.OID(oid) {
			var body string
			if relation.View != nil {
				if stored.ViewOf(relation) == nil {
					return sqltype.Value{}, fmt.Errorf("stuntgres: invalid return-rule payload")
				}
				presentation.Columns = relation.Columns
				body, err = stored.ViewOf(relation).SelectSQL(c.Context, in.Catalog, presentation)
			} else if in.BuiltinViewDefinition != nil {
				body, err = in.BuiltinViewDefinition(c.Context, in.Catalog, relation, renderer.Pretty)
			}
			if err != nil {
				return sqltype.Value{}, err
			}
			if body == "" {
				return sqltype.Value{}, fmt.Errorf("stuntgres: missing advertised return-rule definition")
			}
			text, err := stored.SelectRuleDefinitionSQL(c.Context, relation, body, renderer)
			if err != nil {
				return sqltype.Value{}, err
			}
			return sqltype.String(result, text), nil
		}
		if relation.Rules != nil {
			if _, ok := relation.Rules.(stored.RuleList); !ok {
				return sqltype.Value{}, fmt.Errorf("stuntgres: invalid rule payload")
			}
		}
		for _, rule := range stored.RulesOf(relation) {
			if rule.OID == ids.OID(oid) {
				if err := stored.ValidateRules(in.Catalog, relation); err != nil {
					return sqltype.Value{}, err
				}
				text, err := stored.RuleDefinitionSQL(c.Context, in.Catalog, relation, rule, presentation, in.TemporaryOwner)
				if err != nil {
					return sqltype.Value{}, err
				}
				return sqltype.String(result, text), nil
			}
		}
	}
	if trigger {
		for fact := range catalog.ForeignKeyTriggerFacts(in.Catalog) {
			if err := c.Context.Err(); err != nil {
				return sqltype.Value{}, err
			}
			if fact.OID != ids.OID(oid) {
				continue
			}
			attributes, err := fact.Attributes(in.Catalog, func(parent ids.ObjectAddress, key catalog.IndexKey) (int32, bool) {
				if key.Derived == nil {
					return key.Attribute, true
				}
				if column, ok := key.Derived.Definition.(plan.ColumnExpr); ok && column.Origin.Address.ClassID == parent.ClassID && column.Origin.Address.ObjectID == parent.ObjectID {
					return column.Origin.Address.SubID, true
				}
				return 0, false
			})
			if err != nil {
				return sqltype.Value{}, err
			}
			text, err := stored.ForeignKeyTriggerDefinitionSQL(c.Context, fact, attributes, renderer)
			if err != nil {
				return sqltype.Value{}, err
			}
			return sqltype.String(result, text), nil
		}
	}
	if err := c.Context.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Null(result), nil
}
