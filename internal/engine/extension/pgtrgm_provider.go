package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

const (
	PgTrgmOperatorStart = len(functions.PgTrgmMembers)
	PgTrgmMemberCount   = PgTrgmOperatorStart + 10
)

type PgTrgmProvider struct{ control catalog.ExtensionControl }

var StockPgTrgm = &PgTrgmProvider{control: catalog.ExtensionControl{
	Version: "1.6", Trusted: true, Superuser: true, Relocatable: true,
	Comment: new("text similarity measurement and index searching based on trigrams"),
}}

func (p *PgTrgmProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (p *PgTrgmProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.6"}}
}
func (p *PgTrgmProvider) ValidateProcedure(v catalog.Read, procedure *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, procedure, p)
}
func (p *PgTrgmProvider) ValidateExtension(v catalog.Read, ext *catalog.Extension) error {
	return ValidateCurrentInstallation(v, ext, p)
}
func PgTrgmProviderOf(procedure *catalog.Procedure) *PgTrgmProvider {
	p, _ := procedure.Implementation.(*PgTrgmProvider)
	return p
}

var pgTrgmOperators = [...]struct {
	name       string
	procedure  int
	commutator int
	line       int
}{
	{"%", 4, 0, 33},
	{"<%", 5, 2, 57},
	{"%>", 6, 1, 66},
	{"<<%", 7, 4, 21},
	{"%>>", 8, 3, 30},
	{"<->", 9, 5, 80},
	{"<<->", 10, 7, 97},
	{"<->>", 11, 6, 104},
	{"<<<->", 12, 9, 49},
	{"<->>>", 13, 8, 56},
}

func (p *PgTrgmProvider) validateRecipe(v catalog.Read, ext *catalog.Extension) error {
	if p != StockPgTrgm || v.Owner() == nil || !currentManifest(v, ext) || ext.Name() != "pg_trgm" || ext.Version != "1.6" || len(ext.Members) != PgTrgmMemberCount {
		return fmt.Errorf("stuntgres: invalid pg_trgm manifest")
	}
	for _, address := range ext.Members[:PgTrgmOperatorStart] {
		procedure, ok := v.Procedure(address)
		if !ok {
			return InvalidProviderDescriptor()
		}
		if _, err := ProcedureSymbol(v, procedure); err != nil {
			return err
		}
	}
	for i, recipe := range pgTrgmOperators {
		op, ok := v.Operator(ext.Members[PgTrgmOperatorStart+i])
		member := functions.PgTrgmMembers[recipe.procedure]
		restriction, join := ids.OID(0), ids.OID(0)
		if member.Threshold != "" {
			restriction, join = 5040, 5041
		}
		if !ok || op.Name() != recipe.name || op.Namespace() != ext.TargetNamespace || op.Extension != ext.Address() || op.Left != 25 || op.Right != 25 || op.Result != member.Result || op.Function != ext.Members[recipe.procedure].ObjectID || op.Commutator != ext.Members[PgTrgmOperatorStart+recipe.commutator].ObjectID || op.Negator != 0 || op.Hash || op.Merge || op.Restriction != restriction || op.Join != join {
			return InvalidProviderDescriptor()
		}
		if err := ValidateProviderHeader(v, op, ids.OperatorClassID); err != nil {
			return err
		}
	}
	return nil
}
