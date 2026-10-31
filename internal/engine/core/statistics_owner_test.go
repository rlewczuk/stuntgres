package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestStatisticsAlterDescriptorPreservation(t *testing.T) {
	for _, expression := range []bool{false, true} {
		t.Run(map[bool]string{false: "keys", true: "expression"}[expression], func(t *testing.T) {
			e, rel := statisticsUnitEngine(t)
			aclUnitRoles(e, "statistics_recipient")
			recipient := e.Roles().LookupRef("statistics_recipient")
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			syntax := statisticsUnitCreate()
			if expression {
				parsed, err := parser.Parse("CREATE STATISTICS statistics_definition ON (a+1),c FROM statistics_input")
				if err != nil {
					t.Fatal(err)
				}
				syntax = parsed[0].(*parser.CreateStatistics)
			}
			stage, err := statisticsUnitStage(t, e, op, syntax)
			if err != nil {
				t.Fatal(err)
			}
			old := statisticsUnitDescriptor(t, stage)
			enginetest.PublishOperationStage(t, e, op, stage)
			e.DiscardCatalogStage(stage)
			captured := e.CatalogRead()
			key := storage.ExtendedStatisticsKey{Definition: e.Catalog.Domain.Ref(captured.Epoch(), old.Address())}
			types := make(map[sqltype.ID]*sqltype.Descriptor)
			for _, id := range []sqltype.ID{sqltype.Int4, sqltype.PgNDistinct, sqltype.PgDependencies, sqltype.PgMCVList} {
				typ, ok := captured.LookupType(id)
				if !ok {
					t.Fatal("missing canonical collector type", id)
				}
				types[id] = typ
			}
			columns := make([]storage.StatisticsColumn, old.Selection.DimensionLen())
			for i := range columns {
				usage := types[sqltype.Int4].Usage()
				if i < old.Selection.Len() {
					usage = rel.Columns[rel.IndexForAttribute(old.Selection.Key(i))].Usage
				} else {
					usage = old.Expressions.At(i - old.Selection.Len()).Usage()
				}
				columns[i] = storage.StatisticsColumn{Key: storage.StatisticsKey{Attribute: old.Selection.Dimension(i)}, Usage: usage, Slot: i}
			}
			collector, err := storage.NewExtendedCollector(storage.ExtendedInput{Key: key, Definition: old, Columns: columns, Type: types[sqltype.PgNDistinct], DependenciesType: types[sqltype.PgDependencies], MCVType: types[sqltype.PgMCVList], MCVTarget: 100})
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range []int64{1, 1, 2, 2} {
				values := make([]sqltype.Value, len(columns))
				for i := range values {
					values[i], err = sqltype.Integer(columns[i].Usage.Type, n)
					if err != nil {
						t.Fatal(err)
					}
				}
				if err = collector.Add(t.Context(), values); err != nil {
					t.Fatal(err)
				}
			}
			fact, err := collector.Finish(t.Context())
			if err != nil || fact == nil {
				t.Fatal(err)
			}
			facts := &storage.RelationStatistics{Extended: map[storage.ExtendedStatisticsKey]*storage.ExtendedStatistics{key: fact}}
			ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 50000}, ObjectName: "statistics_destination", ObjectRevision: 1, Owner: old.Owner}
			e.Mu.Lock()
			state := catalog.CloneState(e.Catalog.State)
			state.SetDescriptor(ns.Address(), ns)
			state.BindName(catalog.DescriptorNameKey(ns), ns.Address())
			e.Catalog.PublishLocked(state)
			e.Mu.Unlock()
			name := configName(old.Name())
			for _, action := range []string{"rename", "schema", "owner", "target", "default"} {
				before := e.CatalogRead()
				previous, _ := before.Statistics(old.Address())
				e.Mu.Lock()
				auth, err := e.AuthorizationLocked(op)
				if err != nil {
					e.Mu.Unlock()
					t.Fatal(err)
				}
				next := e.BeginCatalogStage(auth.Catalog)
				next.AuthorizeLocked(auth, op.Session)
				host := core.DDLHost{E: e, Op: op, CatalogStage: next}
				switch action {
				case "rename":
					err = ddl.StageRenameStatistics(host, auth, &parser.AlterStatisticsRename{Name: name, NewName: parser.Identifier{Text: "statistics_renamed"}})
					name = configName("statistics_renamed")
				case "schema":
					err = ddl.StageStatisticsNamespace(host, auth, &parser.AlterStatisticsSchema{Name: name, Schema: parser.Identifier{Text: ns.Name()}})
					name = parser.ObjectName{Parts: []parser.Identifier{{Text: ns.Name()}, {Text: "statistics_renamed"}}}
				case "owner":
					err = role.StageOwnership(next, previous, recipient)
				case "target":
					err = ddl.StageAlterStatisticsTarget(host, auth, &parser.AlterStatisticsTarget{Name: name, Target: 10000})
				case "default":
					err = ddl.StageAlterStatisticsTarget(host, auth, &parser.AlterStatisticsTarget{Name: name, Target: -1})
				}
				e.Mu.Unlock()
				if err != nil {
					t.Fatal(action, err)
				}
				changed := statisticsUnitDescriptor(t, next)
				if changed.Address() != old.Address() || changed.Relation != old.Relation || changed.Selection != old.Selection || changed.Expressions != old.Expressions || changed.Revision() <= previous.Revision() {
					t.Fatal("metadata COW replaced definition identity", action)
				}
				expected := *previous
				expected.ObjectRevision = changed.ObjectRevision
				switch action {
				case "rename":
					expected.ObjectName = "statistics_renamed"
				case "schema":
					expected.ObjectNamespace = ns.Address()
				case "owner":
					expected.Owner = recipient
				case "target":
					expected.TargetSet = true
					expected.Target = 10000
				case "default":
					expected.TargetSet = false
					expected.Target = 0
				}
				if *changed != expected {
					t.Fatal("metadata alteration changed unrelated fields", action)
				}
				enginetest.PublishOperationStage(t, e, op, next)
				e.DiscardCatalogStage(next)
				after := e.CatalogRead()
				if err = (catalog.StatisticsProvider{}).Validate(after, changed); err != nil {
					t.Fatal(err)
				}
				if after.NameAddress(catalog.DescriptorNameKey(changed)) != old.Address() {
					t.Fatal("new index missing")
				}
				if catalog.DescriptorNameKey(changed) != catalog.DescriptorNameKey(previous) && after.NameAddress(catalog.DescriptorNameKey(previous)) != (ids.ObjectAddress{}) {
					t.Fatal("old index survived")
				}
				if storage.CompatibleExtendedStatistics(facts, rel, after) != facts || facts.Extended[key] != fact {
					t.Fatal("compatible collected root replaced")
				}
				if captured.Get(old.Address()) != old || old.Expressions != changed.Expressions {
					t.Fatal("old capture mutated")
				}
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			noop := e.BeginCatalogStage(auth.Catalog)
			noop.AuthorizeLocked(auth, op.Session)
			current, _ := auth.Catalog.Statistics(old.Address())
			err = ddl.StageStatisticsNamespace(core.DDLHost{E: e, Op: op, CatalogStage: noop}, auth, &parser.AlterStatisticsSchema{Name: name, Schema: parser.Identifier{Text: ns.Name()}})
			if err == nil {
				err = role.StageOwnership(noop, current, current.Owner)
			}
			e.Mu.Unlock()
			if err != nil || noop.Get(current.Address()) != nil || noop.DescriptorCount() != 0 ||
				noop.RemovalCount() != 0 || noop.StageRead().Get(current.Address()) != current ||
				e.CatalogRead().Get(current.Address()) != current {
				t.Fatal("no-op replaced descriptor", err)
			}
			e.DiscardCatalogStage(noop)
		})
	}
}
