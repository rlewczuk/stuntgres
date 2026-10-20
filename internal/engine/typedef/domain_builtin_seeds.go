package typedef

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// BuiltinDomainCheckSeed is the CHECK declaration of a builtin domain.
type BuiltinDomainCheckSeed struct {
	ID          ids.OID
	Constraints []parser.ColumnConstraint
}

// BuiltinDomainDefaultSeed is the DEFAULT expression of a builtin domain.
type BuiltinDomainDefaultSeed struct {
	ID         ids.OID
	Expression parser.Expr
}

// BuiltinDomainCheckSeeds parses the CHECK constraints of the
// information_schema domains, in installation order.
func BuiltinDomainCheckSeeds() ([]BuiltinDomainCheckSeed, error) {
	declarations := [...]struct {
		id          ids.OID
		declaration string
	}{
		{13256, `CREATE DOMAIN information_schema.yes_or_no AS character varying(3) COLLATE "C" CONSTRAINT yes_or_no_check CHECK (VALUE IN ('YES','NO'))`},
		{13243, `CREATE DOMAIN information_schema.cardinal_number AS integer CONSTRAINT cardinal_number_domain_check CHECK (VALUE >= 0)`},
	}
	seeds := make([]BuiltinDomainCheckSeed, len(declarations))
	for i, recipe := range declarations {
		raw, err := parser.Parse(recipe.declaration)
		if err != nil {
			return nil, err
		}
		seeds[i] = BuiltinDomainCheckSeed{ID: recipe.id, Constraints: raw[0].(*parser.CreateDomain).Constraints}
	}
	return seeds, nil
}

// BuiltinDomainDefaultSeeds returns the DEFAULT of information_schema.time_stamp.
func BuiltinDomainDefaultSeeds() []BuiltinDomainDefaultSeed {
	return []BuiltinDomainDefaultSeed{{ID: 13254, Expression: parser.CurrentDateTimeExpr{Kind: parser.CurrentTimestamp, HasPrecision: true, Precision: 2}}}
}
