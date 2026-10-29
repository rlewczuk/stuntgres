package stored

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
)

// SelectRuleDefinitionSQL wraps an owned SELECT body without trimming its
// leading space, changing its wrapping, or inventing an executable rule payload.
func SelectRuleDefinitionSQL(ctx context.Context, relation *catalog.Relation, body string, renderer Deparser) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if body == "" {
		return "", fmt.Errorf("stuntgres: missing return-rule body")
	}
	target, err := renderer.RelationName(relation, !renderer.Pretty)
	if err != nil {
		return "", err
	}
	return "CREATE RULE " + renderer.Identifier("_RETURN") + " AS\n    ON SELECT TO " + target + " DO INSTEAD " + body + ";", ctx.Err()
}

// ForeignKeyTriggerDefinitionSQL presents validated logical RI attributes. These
// internal triggers have no user executable payload and no native replay recipe.
func ForeignKeyTriggerDefinitionSQL(ctx context.Context, fact catalog.ForeignKeyTriggerFact, attributes catalog.ForeignKeyTriggerAttributes, renderer Deparser) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	relation, ok := renderer.View.Relation(fact.Relation)
	if !ok {
		return "", ids.ErrCatalogReplaced
	}
	opposite, ok := renderer.View.Relation(attributes.Opposite)
	if !ok {
		return "", ids.ErrCatalogReplaced
	}
	function, ok := regobj.RoutineIdentityByOID(renderer.View, attributes.Function)
	if !ok {
		return "", fmt.Errorf("stuntgres: missing RI trigger function")
	}
	target, err := renderer.RelationName(relation, !renderer.Pretty)
	if err != nil {
		return "", err
	}
	// FROM has its own visibility decision, even in the default format.
	from, err := renderer.RelationName(opposite, false)
	if err != nil {
		return "", err
	}
	name, err := renderer.routineIdentityName(function)
	if err != nil {
		return "", err
	}
	event := "UPDATE"
	switch attributes.Flags {
	case 5:
		event = "INSERT"
	case 9:
		event = "DELETE"
	case 17:
	default:
		return "", fmt.Errorf("stuntgres: invalid RI trigger event")
	}
	timing := "NOT DEFERRABLE INITIALLY IMMEDIATE"
	if attributes.Deferrable {
		timing = "DEFERRABLE INITIALLY IMMEDIATE"
		if attributes.InitiallyDeferred {
			timing = "DEFERRABLE INITIALLY DEFERRED"
		}
	}
	return "CREATE CONSTRAINT TRIGGER " + renderer.Identifier(fact.Name) + " AFTER " + event + " ON " + target + " FROM " + from + " " + timing + " FOR EACH ROW EXECUTE FUNCTION " + name + "()", ctx.Err()
}
