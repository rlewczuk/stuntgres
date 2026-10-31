package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
)

func TestHostMaterializationDetachedType(t *testing.T) {
	t.Run("genuine-detached-generation", func(t *testing.T) {
		e, procedure := hostOwnerEngine(t, nil)
		retained := e.CatalogRead()
		originalPair := retained.Get(procedure.Host.Pairs[0])
		originalTo := retained.Get(procedure.Host.Converters[1])
		enginetest.Run(t, e, "DROP FUNCTION owner_host_from(internal) CASCADE")
		// Installation replay uses this real membership transition before it
		// creates aliases, restoring membership only after dependent DDL.
		enginetest.Run(t, e, "ALTER EXTENSION hstore DROP TYPE hstore")
		enginetest.Run(t, e, "CREATE LANGUAGE detached_host HANDLER owner_host_handler")
		read := e.CatalogRead()
		alias := read.NameAddress(ids.NameKey{ClassID: ids.LanguageClassID, Name: "detached_host"})
		var pair *catalog.Transform
		for _, descriptor := range read.Descriptors() {
			if candidate, ok := descriptor.(*catalog.Transform); ok {
				if candidate.Language != alias || pair != nil {
					t.Fatal("retired baseline pair resurrected or unexpected association")
				}
				pair = candidate
			}
			if candidate, ok := descriptor.(*catalog.Procedure); ok && candidate.HostConversion != nil && candidate.HostConversion.From {
				t.Fatal("alias creation resurrected dropped FROM procedure")
			}
		}
		if pair == nil || pair.From.ObjectID != 0 || pair.To != originalTo.Address() || read.Get(pair.To) != originalTo || pair.SQLType != procedure.Host.Layout.Result.Type {
			t.Fatal("detached authentic type did not retain its genuine TO-only association")
		}
		typ, ok := read.Type(pair.Type)
		if !ok || typ.Origin == nil || typ.Extension.ObjectID != 0 {
			t.Fatal("detached type lost installation provenance or regained membership")
		}
		if err := (catalog.TransformProvider{}).Validate(read, pair); err != nil {
			t.Fatal(err)
		}
		if retained.Get(originalPair.Address()) != originalPair || retained.Get(originalTo.Address()) != originalTo {
			t.Fatal("materialization mutated retained source descriptors")
		}
	})
	t.Run("unrelated-same-name", func(t *testing.T) {
		runtime, err := host.New(hostOwnerDefinition(nil))
		if err != nil {
			t.Fatal(err)
		}
		e := engine.New()
		if err = e.InstallHostDefinitions([]*host.Runtime{runtime}); err != nil {
			t.Fatal(err)
		}
		enginetest.Run(t, e, "CREATE TYPE hstore AS ENUM ('unrelated')")
		enginetest.Run(t, e, "CREATE LANGUAGE detached_host HANDLER owner_host_handler")
		for _, descriptor := range e.CatalogRead().Descriptors() {
			if _, ok := descriptor.(*catalog.Transform); ok {
				t.Fatal("SQL spelling manufactured a host conversion association")
			}
			if p, ok := descriptor.(*catalog.Procedure); ok && p.HostConversion != nil {
				t.Fatal("SQL spelling manufactured a directional procedure")
			}
		}
	})
	t.Run("missing-provenance", func(t *testing.T) {
		e, procedure := hostOwnerEngine(t, nil)
		e.Mu.Lock()
		defer e.Mu.Unlock()
		read := e.Catalog.Current()
		stage := e.BeginCatalogStage(read)
		defer e.DiscardCatalogStage(stage)
		typ, _ := read.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(procedure.Host.Layout.Result.Type.ID())})
		forged := *typ
		forged.Origin = nil
		if err := stage.Replace(&forged); err != nil {
			t.Fatal(err)
		}
		language, _ := read.Get(procedure.Language).(*catalog.Language)
		alias := *language
		oid, err := e.Catalog.AllocateOIDLocked()
		if err != nil {
			t.Fatal(err)
		}
		alias.ObjectAddress.ObjectID, alias.ObjectName = oid, "forged_host"
		alias.HostRegistration = nil
		if err = stage.Create(&alias); err != nil {
			t.Fatal(err)
		}
		if err = (core.DDLHost{E: e, CatalogStage: stage}).MaterializeHostConversions(); err == nil {
			t.Fatal("unauthenticated hstore descriptor admitted materialization")
		}
		if e.Catalog.Current().State() != read.State() {
			t.Fatal("rejected materialization published a catalog change")
		}
	})
}
