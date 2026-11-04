package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestAnalyzeInheritedForeignConversionFailure(t *testing.T) {
	for _, conversion := range []struct {
		name, declaration, literal, code string
		valid, invalid                   any
	}{
		{"integer", "integer", "10", "22P02", int64(20), "not_an_integer"},
		{"domain", "mixed_positive", "10", "23514", int64(20), int64(-1)},
		{"typmod", "varchar(2)", "'ok'", "22001", "no", "too_long"},
	} {
		for _, partition := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				t.Run(conversion.name+map[bool]string{false: "/ordinary", true: "/partition"}[partition]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					f := newInheritedForeignFixture(t, 0, false)
					if conversion.name == "domain" {
						enginetest.SessionSQL(t, f.s, "CREATE DOMAIN mixed_positive AS integer CHECK(VALUE>0)")
					}
					definition := "CREATE TABLE mixed_conversion(a integer,b " + conversion.declaration + ")"
					if partition {
						definition += " PARTITION BY RANGE(a)"
					}
					enginetest.SessionSQL(t, f.s, definition)
					if partition {
						enginetest.SessionSQL(t, f.s, "CREATE TABLE mixed_conversion_native PARTITION OF mixed_conversion FOR VALUES FROM(0) TO(2)")
					}
					enginetest.SessionSQL(t, f.s, "CREATE FOREIGN TABLE mixed_conversion_foreign(b "+conversion.declaration+",a integer) SERVER mixed_owner_server")
					if partition {
						enginetest.SessionSQL(t, f.s, "ALTER TABLE mixed_conversion ATTACH PARTITION mixed_conversion_foreign FOR VALUES FROM(2) TO(4)")
					} else {
						enginetest.SessionSQL(t, f.s, "ALTER FOREIGN TABLE mixed_conversion_foreign INHERIT mixed_conversion")
					}
					for _, query := range []string{
						"INSERT INTO mixed_conversion VALUES(1," + conversion.literal + ")",
						"CREATE STATISTICS mixed_conversion_joint(ndistinct,dependencies,mcv) ON a,b FROM mixed_conversion",
						"CREATE STATISTICS mixed_conversion_expression ON (b::text) FROM mixed_conversion",
					} {
						enginetest.SessionSQL(t, f.s, query)
					}
					source := &inheritedForeignSource{pages: 1, sample: catalog.ForeignSample{Rows: []catalog.ForeignRow{{conversion.valid, int64(2)}, {conversion.valid, int64(2)}}, TotalRows: 2, TotalPages: 1}}
					f.wrapper.sources["mixed_conversion_foreign"] = source
					f.parent = enginetest.CatalogRef(t, f.e, f.e.CatalogRead(), "mixed_conversion")
					f.foreign = enginetest.CatalogRef(t, f.e, f.e.CatalogRead(), "mixed_conversion_foreign")
					if err := f.execute(t, context.Background(), "ANALYZE ONLY mixed_conversion", prepared); err != nil {
						t.Fatal(err)
					}
					before := f.e.Storage.RelationMetadata[f.parent.Address]
					retained := f.retained(t)
					source.sample.Rows[0][0] = conversion.invalid
					err := f.execute(t, context.Background(), "ANALYZE ONLY mixed_conversion", prepared)
					var diagnostic *sqlstate.Error
					if !errors.As(err, &diagnostic) || diagnostic.Code != conversion.code {
						t.Fatal("genuine child conversion failure missing or altered", err)
					}
					after := f.e.Storage.RelationMetadata[f.parent.Address]
					if after.Statistics != before.Statistics || after.InheritedTuples != 3 {
						t.Fatal("rejected typed import changed immutable scalar/extended population")
					}
					if partition {
						if after != before || f.e.Storage.Relations[f.parent.Address] != nil {
							t.Fatal("partition import rejection fabricated local completion")
						}
					} else if after.AnalyzeCount != before.AnalyzeCount+1 || !after.LastAnalyze.After(before.LastAnalyze) || f.e.Storage.Relations[f.parent.Address].Maintenance.Tuples != 1 {
						t.Fatal("import failure retracted completed local pass")
					}
					if f.wrapper.active.Load() != 0 || f.e.Storage.Relations[f.foreign.Address] != nil || f.e.Storage.RelationMetadata[f.foreign.Address] != nil {
						t.Fatal("failed inherited conversion leaked resources/foreign publication")
					}
					retained()
					enginetest.LockEmpty(t, f.e)
				})
			}
		}
	}
}
