package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/host"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

// Empty transform inventories never construct callable identity values. Actual
// supplied runtimes distinguish the three OID fields from two REGPROC fields,
// including a genuine absent direction represented by a nonnull REGPROC zero.
func TestHostTransformCatalogCarriers(t *testing.T) {
	for _, mode := range []string{"both", "from-only", "to-only"} {
		t.Run(mode, func(t *testing.T) {
			definition := hostOwnerDefinition(nil)
			if mode == "from-only" {
				definition.Conversions[0].ToSQL = nil
				definition.Conversions[0].ToName = host.Name{}
			}
			if mode == "to-only" {
				definition.Conversions[0].FromSQL = nil
				definition.Conversions[0].FromName = host.Name{}
			}
			runtime, err := host.New(definition)
			if err != nil {
				t.Fatal(err)
			}
			e := engine.New()
			if err = e.InstallHostDefinitions([]*host.Runtime{runtime}); err != nil {
				t.Fatal(err)
			}
			enginetest.Run(t, e, "CREATE EXTENSION hstore")
			view := e.CatalogRead()
			var pair *catalog.Transform
			for _, descriptor := range view.Descriptors() {
				if candidate, ok := descriptor.(*catalog.Transform); ok {
					if pair != nil {
						t.Fatal("unexpected extra bootstrap pair")
					}
					pair = candidate
				}
			}
			if pair == nil {
				t.Fatal("genuine materialization produced no pair")
			}
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.TransformOID}
			source := view.MustRelation(address)
			produce, ok := pgcatalog.ProducerFor(pgcatalog.TransformOID)
			if !ok {
				t.Fatal("missing transform catalog producer")
			}
			input := pgcatalog.Input{Context: context.Background(), Catalog: view, Source: source, Owner: view.Owner(), Runtime: core.EngineCatalogRuntime{}}
			rows, err := produce(input)
			if err != nil || len(rows) != 1 || len(rows[0]) != 5 {
				t.Fatal("nonempty transform capture", rows, err)
			}
			want := []uint32{uint32(pair.Address().ObjectID), uint32(pair.Type.ObjectID), uint32(pair.Language.ObjectID), uint32(pair.From.ObjectID), uint32(pair.To.ObjectID)}
			for i, value := range rows[0] {
				if value.Type() != source.Columns[i].Usage.Type || value.IsNull() {
					t.Fatal("capture lost owned nonnull field type", i, value)
				}
				var actual uint32
				if i < 3 {
					actual, err = value.OID()
					if _, aliasErr := value.ProcedureOID(); aliasErr == nil {
						t.Fatal("OID field became a routine alias", i)
					}
				} else {
					actual, err = value.ProcedureOID()
					if _, oidErr := value.OID(); oidErr == nil {
						t.Fatal("REGPROC field became a plain OID", i)
					}
				}
				if err != nil || actual != want[i] {
					t.Fatal("typed catalog identity mismatch", i, actual, want[i], err)
				}
			}
			second, err := produce(input)
			if err != nil || len(second) != 1 {
				t.Fatal(second, err)
			}
			second[0][3] = sqltype.Null(source.Columns[3].Usage.Type)
			if rows[0][3].IsNull() {
				t.Fatal("captures share mutable row slots")
			}
			if mode == "both" {
				// Preserve the real public join and native oidvector signatures;
				// do not cast catalog columns to text to hide a carrier mismatch.
				result := enginetest.Run(t, e, "SELECT f.proargtypes='2281'::oidvector AS v FROM pg_transform t JOIN pg_proc f ON f.oid=t.trffromsql JOIN pg_proc r ON r.oid=t.trftosql WHERE f.prorettype=2281 AND r.proargtypes='2281'::oidvector AND r.prorettype=t.trftype")
				var row [1]sqltype.Value
				present, err := result.Rows.Next(context.Background(), row[:])
				if err != nil || !present {
					t.Fatal("genuine internal-signature join", present, err)
				}
				matched, err := row[0].Boolean()
				if err != nil || !matched {
					t.Fatal("internal oidvector signature mismatch", matched, err)
				}
				if present, err = result.Rows.Next(context.Background(), row[:]); err != nil || present {
					t.Fatal("unexpected extra signature row", present, err)
				}
			}
		})
	}
}

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
