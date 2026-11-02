package extension

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

type PgStatStatementsProvider struct{ control catalog.ExtensionControl }

var StockPgStatStatements = &PgStatStatementsProvider{control: catalog.ExtensionControl{Version: "1.12", Superuser: true, Relocatable: true, Comment: new("track planning and execution statistics of all SQL statements executed")}}

func (p *PgStatStatementsProvider) Control() catalog.ExtensionControl { return p.control.Detached() }
func (*PgStatStatementsProvider) Versions() catalog.ExtensionVersions {
	return catalog.ExtensionVersions{Bases: []string{"1.12"}}
}
func PgStatStatementsProviderOf(p *catalog.Procedure) *PgStatStatementsProvider {
	provider, _ := p.Implementation.(*PgStatStatementsProvider)
	return provider
}
func (p *PgStatStatementsProvider) ValidateProcedure(v catalog.Read, r *catalog.Procedure) error {
	return ValidateProcedureOrigin(v, r, p)
}
func (p *PgStatStatementsProvider) ValidateExtension(v catalog.Read, e *catalog.Extension) error {
	return ValidateCurrentInstallation(v, e, p)
}
func (p *PgStatStatementsProvider) validateRecipe(v catalog.Read, e *catalog.Extension) error {
	if p != StockPgStatStatements || !currentManifest(v, e) || e.Name() != "pg_stat_statements" || e.Version != "1.12" || len(e.Members) != 2 {
		return fmt.Errorf("stuntgres: invalid pg_stat_statements manifest")
	}
	for _, a := range e.Members {
		proc, ok := v.Procedure(a)
		if !ok {
			return InvalidProviderDescriptor()
		}
		if _, err := ProcedureSymbol(v, proc); err != nil {
			return err
		}
	}
	return nil
}

var pgStatStatementsOutputs = func() *catalog.RoutineOutputs {
	types := make([]ids.OID, len(functions.PgStatStatementsColumns))
	names := make([]string, len(types)+1)
	names[0] = "showtext"
	for i, c := range functions.PgStatStatementsColumns {
		types[i] = ids.OID(c.Type)
		names[i+1] = c.Name
	}
	return catalog.NewRoutineOutputs(types, names...)
}()
var pgStatStatementsInfoOutputs = catalog.NewRoutineOutputs([]ids.OID{20, 1184}, "dealloc", "stats_reset")

func pgStatStatementsDeclaration(slot int) catalog.InstalledRoutineDeclaration {
	if slot == 0 {
		return catalog.InstalledRoutineDeclaration{Parallel: 's', ReturnsSet: true, Rows: 1000, Outputs: pgStatStatementsOutputs}
	}
	return catalog.InstalledRoutineDeclaration{Parallel: 's', Outputs: pgStatStatementsInfoOutputs}
}
