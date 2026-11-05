package extension_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestPgBuffercacheSummaryProviderIdentity(t *testing.T) {
	e := engine.New()
	ext := buffercacheInstalled(t, e)
	view := e.CatalogRead()
	if len(ext.Members) != 13 || len(ext.Installation.Members) != 7 { t.Fatal("procedure recipe and ordinary view closures", ext) }
	monitor := view.Owner().Principals.Principal(3373)
	seen := [7]bool{}
	for _, address := range ext.Installation.Members {
		p := view.MustProcedure(address)
		slot := p.Origin.Declaration
		if slot >= 7 || seen[slot] { t.Fatal("invalid/duplicate original slot", slot) }
		seen[slot] = true
		if slot < extension.PgBuffercacheAggregateStart {
			if slot == 0 && p.Display.Source != "pg_buffercache_pages" { t.Fatal("pages identity moved") }
			if slot > 0 && p.Installed != extension.PgBuffercacheEvictionMembers[slot-1].Declaration { t.Fatal("prior eviction slot changed") }
			continue
		}
		if slot == 6 {
			if p.Name() != "pg_buffercache_numa_pages" || p.OutputDeclaration() != nil || !p.Installed.ReturnsSet { t.Fatal("appended NUMA slot changed", p) }
			continue
		}
		member := extension.PgBuffercacheAggregateMembers[slot-extension.PgBuffercacheAggregateStart]
		if p.Name() != member.Name || p.Signature != "" || p.Result != 2249 || p.Strict || p.SecurityDefiner || p.Volatility != expr.Volatile || p.Installed != member.Declaration || p.Installed.ReturnsSet != member.Kind.Set() { t.Fatal("released aggregate declaration", p) }
		granted := false
		if !p.ACL.Explicit { t.Fatal("implicit PUBLIC execute", p) }
		for _, acl := range p.ACL.Entries {
			if acl.Grantee.Public && acl.Privileges != 0 { t.Fatal("PUBLIC revoke absent") }
			granted = granted || acl.Grantee.Role == monitor && acl.Privileges&authorization.ExecutePrivilege != 0
		}
		if !granted || view.State().InitialPrivilege(p.Address()) == nil { t.Fatal("missing genuine monitor grant/initial ACL", p) }
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Result = 16 },
			func(p *catalog.Procedure) { p.Signature = "23" },
			func(p *catalog.Procedure) { p.Strict = true },
			func(p *catalog.Procedure) { p.Installed.ReturnsSet = !p.Installed.ReturnsSet },
			func(p *catalog.Procedure) { p.Volatility = expr.Immutable },
			func(p *catalog.Procedure) { p.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{23}, "fake") },
			func(p *catalog.Procedure) { p.Implementation = new(extension.PgBuffercacheProvider) },
			func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 1; p.Origin = &origin },
			func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 5-(slot-4); p.Origin = &origin },
		} {
			bad := *p
			mutate(&bad)
			state := catalog.CloneState(view.State())
			state.SetDescriptor(p.Address(), &bad)
			if extension.StockPgBuffercache.ValidateProcedure(e.Catalog.Read(state), &bad) == nil { t.Fatal("forged aggregate procedure accepted") }
		}
	}
	for _, present := range seen { if !present { t.Fatal("incomplete original slot inventory") } }
}
