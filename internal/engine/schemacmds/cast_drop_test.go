package schemacmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCastDefinitionDropPublicationTransition(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE CAST (xid AS real) WITHOUT FUNCTION"))
	base := e.CatalogRead()
	original, ok := base.Cast(sqltype.Xid, sqltype.Float4)
	if !ok {
		t.Fatal("CREATE did not publish cast")
	}
	key := catalog.DescriptorNameKey(original)
	drop := domainUnitStage(t, e, op, "DROP CAST (xid AS real) RESTRICT")
	defer e.DiscardCatalogStage(drop)
	if len(drop.DomainTargets) != 1 || drop.DomainTargets[0] != original.Address() {
		t.Fatal("DROP did not select directed edge", drop.DomainTargets)
	}
	if err := core.StageExpressionDependencies(context.Background(), drop, drop.DomainTargets, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{}); err != nil {
		t.Fatal(err)
	}
	if removal, ok := drop.Removal(original.Address()); !ok || removal.Descriptor != original {
		t.Fatal("DROP did not record authentic predecessor", removal)
	}
	if _, ok := drop.Cast(original.Source, original.Target); ok || drop.LookupName(key) != (ids.ObjectAddress{}) {
		t.Fatal("candidate retained directed pair name")
	}
	candidate := e.StageRead(drop)
	if _, ok := candidate.Cast(original.Source, original.Target); ok || candidate.Get(original.Address()) != nil || candidate.NameAddress(key) != (ids.ObjectAddress{}) {
		t.Fatal("candidate retained descriptor, pair index or name")
	}
	if current, ok := e.CatalogRead().Cast(original.Source, original.Target); !ok || current != original {
		t.Fatal("private DROP mutated publication")
	}
	domainUnitPublish(t, e, drop)
	retired := e.CatalogRead()
	if _, ok := retired.Cast(original.Source, original.Target); ok || retired.Get(original.Address()) != nil || retired.NameAddress(key) != (ids.ObjectAddress{}) {
		t.Fatal("published DROP retained descriptor, pair index or name")
	}
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE CAST (xid AS real) WITH INOUT AS ASSIGNMENT"))
	current, ok := e.CatalogRead().Cast(original.Source, original.Target)
	if !ok || current.Address() == original.Address() || current.Method != "i" || current.Context != "a" {
		t.Fatal("recreation did not publish fresh definition", current)
	}
	if retained, ok := base.Cast(original.Source, original.Target); !ok || retained != original || base.Get(original.Address()) != original || base.NameAddress(key) != original.Address() {
		t.Fatal("DROP/recreation mutated retained immutable predecessor")
	}
	if _, ok := retired.Cast(original.Source, original.Target); ok {
		t.Fatal("recreation mutated retained retired publication")
	}
}

func TestCastDefinitionDropDependencyTransitions(t *testing.T) {
	for _, root := range []string{"function", "source", "target", "adapter"} {
		t.Run(root, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			var query string
			var source, target sqltype.ID
			if root == "adapter" {
				for _, q := range []string{
					"CREATE CAST (text AS bytea) WITHOUT FUNCTION AS IMPLICIT",
					"CREATE FUNCTION cast_drop_bytes(xid) RETURNS text LANGUAGE SQL AS $$ SELECT 'abc'::text $$",
					"CREATE CAST (xid AS bytea) WITH FUNCTION cast_drop_bytes(xid)",
				} {
					enginetest.Run(t, e, q)
				}
				source, target = sqltype.Xid, sqltype.Bytea
				query = "DROP CAST (text AS bytea)"
			} else if root == "function" {
				for _, q := range []string{
					"CREATE TYPE cast_drop_source AS ENUM ('red')",
					"CREATE FUNCTION cast_drop_convert(cast_drop_source) RETURNS integer LANGUAGE SQL AS $$ SELECT 41 $$",
					"CREATE CAST (cast_drop_source AS integer) WITH FUNCTION cast_drop_convert(cast_drop_source)",
				} {
					enginetest.Run(t, e, q)
				}
				source = domainUnitType(t, e.CatalogRead(), "cast_drop_source").SQLType.ID()
				target = sqltype.Int4
				query = "DROP FUNCTION cast_drop_convert(cast_drop_source)"
			} else {
				for _, q := range []string{
					"CREATE TYPE cast_drop_source AS (v text)",
					"CREATE TYPE cast_drop_target AS (v text)",
					"CREATE CAST (cast_drop_source AS cast_drop_target) WITH INOUT",
				} {
					enginetest.Run(t, e, q)
				}
				source = domainUnitType(t, e.CatalogRead(), "cast_drop_source").SQLType.ID()
				target = domainUnitType(t, e.CatalogRead(), "cast_drop_target").SQLType.ID()
				switch root {
				case "source":
					query = "DROP TYPE cast_drop_source"
				case "target":
					query = "DROP TYPE cast_drop_target"
				}
			}
			base := e.CatalogRead()
			cast, ok := base.Cast(source, target)
			if !ok {
				t.Fatal("missing dependent cast")
			}
			restrict := domainUnitStage(t, e, op, query+" RESTRICT")
			defer e.DiscardCatalogStage(restrict)
			err := core.StageExpressionDependencies(context.Background(), restrict, restrict.DomainTargets, parser.DropRestrict, nil, nil, nil, ids.ObjectAddress{})
			enginetest.State(t, err, "2BP01")
			if _, removed := restrict.Removal(cast.Address()); removed || e.CatalogRead().State() != base.State() {
				t.Fatal("RESTRICT retired cast or published")
			}
			cascade := domainUnitStage(t, e, op, query+" CASCADE")
			defer e.DiscardCatalogStage(cascade)
			if err := core.StageExpressionDependencies(context.Background(), cascade, cascade.DomainTargets, parser.DropCascade, nil, nil, nil, ids.ObjectAddress{}); err != nil {
				t.Fatal(err)
			}
			if _, removed := cascade.Removal(cast.Address()); !removed {
				t.Fatal("CASCADE discovered but did not retire dependent cast")
			}
			if _, ok := e.StageRead(cascade).Cast(source, target); ok {
				t.Fatal("CASCADE candidate retained pair")
			}
			domainUnitPublish(t, e, cascade)
			current := e.CatalogRead()
			if _, ok := current.Cast(source, target); ok || current.Get(cast.Address()) != nil || current.NameAddress(catalog.DescriptorNameKey(cast)) != (ids.ObjectAddress{}) {
				t.Fatal("CASCADE publication retained cast")
			}
			if root == "adapter" {
				if _, ok := current.Cast(sqltype.Text, sqltype.Bytea); ok || current.Get(cast.Function) == nil {
					t.Fatal("adapter CASCADE retained adapter or removed independent function")
				}
			}
			if old, ok := base.Cast(source, target); !ok || old != cast {
				t.Fatal("CASCADE mutated retained predecessor")
			}
		})
	}
}

func TestCastDefinitionDropBuiltinProtection(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	base := e.CatalogRead()
	e.Mu.Lock()
	stage, err := schemacmds.StageSchemaLocked(e, op, enginetest.Statement(t, "DROP CAST (integer AS bigint) CASCADE"), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	e.Mu.Unlock()
	if stage != nil {
		defer e.DiscardCatalogStage(stage)
	}
	enginetest.State(t, err, "2BP01")
	if e.CatalogRead().State() != base.State() {
		t.Fatal("protected builtin DROP changed publication")
	}
}
