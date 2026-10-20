package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// builtinDomainCheckSeed is the CHECK declaration of a builtin domain.
type builtinDomainCheckSeed struct {
	id          ids.OID
	constraints []parser.ColumnConstraint
}

// builtinDomainDefaultSeed is the DEFAULT expression of a builtin domain.
type builtinDomainDefaultSeed struct {
	id         ids.OID
	expression parser.Expr
}

// builtinDomainCheckSeeds parses the CHECK constraints of the
// information_schema domains, in installation order.
func builtinDomainCheckSeeds() ([]builtinDomainCheckSeed, error) {
	declarations := [...]struct {
		id          ids.OID
		declaration string
	}{
		{13256, `CREATE DOMAIN information_schema.yes_or_no AS character varying(3) COLLATE "C" CONSTRAINT yes_or_no_check CHECK (VALUE IN ('YES','NO'))`},
		{13243, `CREATE DOMAIN information_schema.cardinal_number AS integer CONSTRAINT cardinal_number_domain_check CHECK (VALUE >= 0)`},
	}
	seeds := make([]builtinDomainCheckSeed, len(declarations))
	for i, recipe := range declarations {
		raw, err := parser.Parse(recipe.declaration)
		if err != nil {
			return nil, err
		}
		seeds[i] = builtinDomainCheckSeed{id: recipe.id, constraints: raw[0].(*parser.CreateDomain).Constraints}
	}
	return seeds, nil
}

// builtinDomainDefaultSeeds returns the DEFAULT of information_schema.time_stamp.
func builtinDomainDefaultSeeds() []builtinDomainDefaultSeed {
	return []builtinDomainDefaultSeed{{id: 13254, expression: parser.CurrentDateTimeExpr{Kind: parser.CurrentTimestamp, HasPrecision: true, Precision: 2}}}
}
