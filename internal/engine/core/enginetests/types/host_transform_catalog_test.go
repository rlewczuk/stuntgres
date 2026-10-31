package types_test

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
			definition := enginetest.HostOwnerDefinition(nil)
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
			input := pgcatalog.Input{Context: context.Background(), Catalog: view, Source: source, Owner: view.Owner(), Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{})}
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
