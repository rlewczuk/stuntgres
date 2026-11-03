package core_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func TestPgstatginindexCapture(t *testing.T) {
	for _, placement := range []string{"main", "pending"} {
		t.Run(placement, func(t *testing.T) {
			pgstatginindexCapturePlacement(t, placement)
		})
	}
}

func pgstatginindexCapturePlacement(t *testing.T, placement string) {
	e := engine.New()
	options := " WITH(fastupdate=off)"
	if placement == "pending" {
		options = "" // Exercise genuine default-fastupdate deferred DML.
	}
	for _, q := range []string{"CREATE TABLE gin_stats_heap(id integer,a integer[],v tsvector)", "CREATE INDEX gin_stats_array ON gin_stats_heap USING gin(a)" + options, "CREATE INDEX gin_stats_vector ON gin_stats_heap USING gin(v)" + options} {
		enginetest.Run(t, e, q)
	}
	check := func(name string) index.PgstattupleStatsCapture {
		t.Helper()
		c := pgstatindexCaptureFixture(t, e, nil, name)
		root, versions := c.Root, c.Store.Versions
		if root.Gin == nil || len(root.Gin.Columns) != 1 {
			t.Fatal("fixture lacks genuine GIN payload")
		}
		payload, original := root.Gin, *root.Gin
		type columnBacking struct {
			column storage.GinColumn
			lists  []storage.GinPostingList
		}
		var backing [2][]columnBacking
		snapshot := original
		for part, columns := range []*[]storage.GinColumn{&snapshot.Columns, &snapshot.Pending} {
			if *columns == nil {
				continue
			}
			copied := make([]storage.GinColumn, len(*columns))
			copy(copied, *columns)
			backing[part] = make([]columnBacking, len(copied))
			for i, column := range copied {
				backing[part][i] = columnBacking{column, append([]storage.GinPostingList(nil), column.Tokens...)}
				if column.Tokens != nil {
					copied[i].Tokens = make([]storage.GinPostingList, len(column.Tokens))
					copy(copied[i].Tokens, column.Tokens)
					for j, list := range column.Tokens {
						if list.Postings != nil {
							copied[i].Tokens[j].Postings = make([]storage.GinPosting, len(list.Postings))
							copy(copied[i].Tokens[j].Postings, list.Postings)
						}
					}
				}
				if column.NonNull != nil {
					copied[i].NonNull = make([]storage.GinPosting, len(column.NonNull))
					copy(copied[i].NonNull, column.NonNull)
				}
			}
			*columns = copied
		}
		entries := append([]storage.IndexEntry(nil), root.Entries...)
		// No heap projection is needed for metapage-model statistics.
		c.Store = storage.Relation{}
		got, err := index.CapturePgstattupleStats(context.Background(), c)
		if err != nil || got.Version != 2 || got.PendingPages != 0 || got.PendingTuples != 0 {
			t.Fatalf("genuine GIN capture %+v: %v", got, err)
		}
		if c.Root != root || !storage.SameVersionRoot(versions, e.Storage.Relations[c.Parent.Address()].Versions) || len(entries) != len(root.Entries) || len(entries) > 0 && !reflect.DeepEqual(entries, root.Entries) {
			t.Fatal("capture mutated root/heap authority")
		}
		if root.Gin != payload || !reflect.DeepEqual(snapshot, *root.Gin) {
			t.Fatal("capture mutated main/pending payload contents")
		}
		for part, columns := range [][]storage.GinColumn{original.Columns, original.Pending} {
			current := root.Gin.Columns
			if part == 1 {
				current = root.Gin.Pending
			}
			if len(columns) > 0 && &columns[0] != &current[0] {
				t.Fatal("capture replaced main/pending columns backing", part)
			}
			for i, saved := range backing[part] {
				column := saved.column
				if len(column.Tokens) > 0 && &column.Tokens[0] != &current[i].Tokens[0] {
					t.Fatal("capture copied token backing", part, i)
				}
				if len(column.NonNull) > 0 && &column.NonNull[0] != &current[i].NonNull[0] {
					t.Fatal("capture copied nonnull posting backing", part, i)
				}
				for j, list := range saved.lists {
					if len(list.Postings) > 0 && &list.Postings[0] != &current[i].Tokens[j].Postings[0] {
						t.Fatal("capture replaced posting-list backing", part, i, j)
					}
				}
			}
		}
		return c
	}
	check("gin_stats_array")
	enginetest.Run(t, e, "INSERT INTO gin_stats_heap VALUES(1,'{1,2,2}','cat:1 rat:2'),(2,'{2,3}','dog:1'),(3,'{}',''),(4,NULL,NULL)")
	array, vector := check("gin_stats_array"), check("gin_stats_vector")
	if array.Root == vector.Root || array.Index.Address() == vector.Index.Address() || len(array.Root.Entries) != 4 || len(vector.Root.Entries) != 4 {
		t.Fatal("fixture lacks distinct populated roots")
	}
	for _, c := range []index.PgstattupleStatsCapture{array, vector} {
		if placement == "pending" {
			if len(c.Root.Gin.Columns[0].Tokens) != 0 || len(c.Root.Gin.Pending) != 1 || len(c.Root.Gin.Pending[0].Tokens) == 0 || storage.GinPendingIncidences(c.Root.Gin) == 0 {
				t.Fatal("fixture lacks genuine pending inversion")
			}
		} else if len(c.Root.Gin.Columns[0].Tokens) == 0 || len(c.Root.Gin.Pending) != 0 {
			t.Fatal("fixture lacks incumbent main inversion")
		}
	}
	for _, damage := range []string{"missing_root", "missing_payload", "crossed_root", "crossed_binding", "crossed_parent", "wrong_method", "incompatible_payload", "column_count"} {
		t.Run(damage, func(t *testing.T) {
			bad := array
			root := *array.Root
			bad.Root = &root
			switch damage {
			case "missing_root":
				bad.Root = nil
			case "missing_payload":
				root.Gin = nil
			case "crossed_root":
				bad.Root = vector.Root
			case "crossed_binding":
				gin := *root.Gin
				gin.Columns = append([]storage.GinColumn(nil), gin.Columns...)
				gin.Columns[0].Binding = vector.Root.Gin.Columns[0].Binding
				root.Gin = &gin
			case "crossed_parent":
				parent := *array.Parent
				parent.ObjectAddress.ObjectID++
				bad.Parent = &parent
			case "wrong_method":
				target := *array.Index
				definition := *target.SQLIndex
				definition.MethodOID = catalog.HashMethod.OID()
				target.SQLIndex = &definition
				bad.Index = &target
			case "incompatible_payload":
				root.Hash = &storage.HashRoot{}
			case "column_count":
				gin := *root.Gin
				gin.Columns = nil
				root.Gin = &gin
			}
			if _, err := index.CapturePgstattupleStats(context.Background(), bad); err == nil {
				t.Fatal("incoherent capture accepted", damage)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := index.CapturePgstattupleStats(ctx, array); !errors.Is(err, context.Canceled) {
		t.Fatal("precanceled capture", err)
	}
	enginetest.Run(t, e, "ALTER TABLE gin_stats_heap RENAME COLUMN a TO a_renamed")
	enginetest.Run(t, e, "ALTER INDEX gin_stats_array RENAME TO gin_stats_renamed")
	presented := check("gin_stats_renamed")
	if presented.Index == array.Index || presented.Parent == array.Parent || presented.Index.Address() != array.Index.Address() || presented.Parent.Address() != array.Parent.Address() || &presented.Root.Entries[0] != &array.Root.Entries[0] {
		t.Fatal("presentation did not preserve actual root membership authority")
	}
}

func TestPgstatginindexCaptureControls(t *testing.T) {
	ctx := context.Background()
	source := engine.New()
	for _, q := range []string{
		"CREATE SCHEMA gin_control_padding", "CREATE SCHEMA gin_control_toast_padding",
		"CREATE TABLE gin_control_heap(id integer,a integer[],v tsvector)",
		"INSERT INTO gin_control_heap VALUES(1,'{1,2,2}','cat:1 rat:2'),(2,'{2,3}','dog:1')",
		"CREATE INDEX gin_control_i ON gin_control_heap USING gin(a,v) WITH(fastupdate=off)",
		"CREATE TABLE gin_control_empty(a integer[],v tsvector)",
		"CREATE INDEX gin_control_empty_i ON gin_control_empty USING gin(a,v)",
		"CREATE ROLE gin_control_owner", "ALTER TABLE gin_control_heap OWNER TO gin_control_owner",
	} {
		enginetest.Run(t, source, q)
	}
	pgstatindexUnitInstalled(t, source)
	check := func(t *testing.T, e *core.Engine, name string) index.PgstattupleStatsCapture {
		t.Helper()
		capture := pgstatindexCaptureFixture(t, e, nil, name)
		bindings, err := index.BindGinIndex(e.CatalogRead(), capture.Parent, capture.Index.SQLIndex)
		if err != nil || capture.Root.Gin == nil || len(bindings) != len(capture.Root.Gin.Columns) {
			t.Fatal("destination binding", name, err)
		}
		for ordinal, binding := range bindings {
			key := capture.Index.SQLIndex.Keys[ordinal]
			// array_ops has no support slot 1; both selected classes have
			// support slot 2 and search strategy 1. Full class/binding equality
			// below still checks every applicable destination capture.
			if key.Class != binding.Class || !capture.Root.Gin.Columns[ordinal].Binding.Same(binding) || binding.Class.Support(2).Routine == 0 || binding.Class.Search(1).Routine == 0 {
				t.Fatal("descriptor/root omitted destination support or search authority", name, ordinal)
			}
		}
		capture.Store = storage.Relation{}
		stats, err := index.CapturePgstattupleStats(ctx, capture)
		if err != nil || stats.Version != 2 || stats.PendingPages != 0 || stats.PendingTuples != 0 {
			t.Fatal("destination capture", name, stats, err)
		}
		got := enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM pgstatginindex('"+name+"'::regclass)"))
		if !reflect.DeepEqual(got, [][]driver.Value{{int64(2), int64(0), int64(0)}}) {
			t.Fatal("destination public statistics", name, got)
		}
		return capture
	}
	original := check(t, source, "gin_control_i")
	check(t, source, "gin_control_empty_i")
	savedRoot := ginAmcheckClone(original.Root)
	savedKeys := append([]catalog.IndexKey(nil), original.Index.SQLIndex.Keys...)
	var dump bytes.Buffer
	if err := engine.Dump(ctx, source, &dump); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"reset_replace", "collision"} {
		t.Run(mode, func(t *testing.T) {
			target := source
			var retained index.PgstattupleStatsCapture
			var retainedCopy *storage.IndexRoot
			if mode == "reset_replace" {
				if err := engine.Reset(ctx, target); err != nil {
					t.Fatal(err)
				}
			} else {
				target = engine.New()
				keeper := enginetest.StartSession(t, target, nil)
				enginetest.SessionSQL(t, keeper, "CREATE TEMP TABLE gin_control_keep(id integer,v tsvector)")
				enginetest.SessionSQL(t, keeper, "CREATE INDEX gin_control_keep_i ON gin_control_keep USING gin(v)")
				enginetest.SessionSQL(t, keeper, "INSERT INTO gin_control_keep VALUES(73,'retained:1')")
				retained = pgstatindexCaptureFixture(t, target, nil, "gin_control_keep_i")
				retainedCopy = ginAmcheckClone(retained.Root)
				if retained.Parent.Address() != original.Parent.Address() {
					t.Fatal("fixture lacks actual destination TEMP identity collision")
				}
				// Dump references existing cluster roles rather than creating them.
				enginetest.Run(t, target, "CREATE ROLE gin_control_owner")
			}
			for range 2 {
				if err := engine.Restore(ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal("replacement", err)
				}
				current := check(t, target, "gin_control_i")
				check(t, target, "gin_control_empty_i")
				if current.Index == original.Index || current.Parent == original.Parent || current.Root == original.Root || current.Root.Gin == original.Root.Gin {
					t.Fatal("replacement retained source descriptor/root authority")
				}
				if mode == "collision" && current.Parent.Address() == original.Parent.Address() {
					t.Fatal("import did not remap the actual TEMP collision")
				}
				for _, entry := range current.Root.Entries {
					row := target.Storage.Relations[current.Parent.Address()].Versions[entry.Position]
					if entry.Row != row.Row || entry.Version != row.Version {
						t.Fatal("restored index lost destination row/version ownership")
					}
				}
				enginetest.Run(t, target, "INSERT INTO gin_control_heap VALUES(3,'{2}','cat:1 rat:2')")
				enginetest.Run(t, target, "UPDATE gin_control_heap SET a='{9}',v='dog:1' WHERE id=1")
				for query, want := range map[string][][]driver.Value{
					"SELECT id FROM gin_control_heap WHERE a @> '{2}'::integer[] ORDER BY id":       {{int64(2)}, {int64(3)}},
					"SELECT id FROM gin_control_heap WHERE v @@ 'cat <-> rat'::tsquery ORDER BY id": {{int64(3)}},
				} {
					if got := enginetest.Tuples(enginetest.Run(t, target, query)); !reflect.DeepEqual(got, want) {
						t.Fatal("postrestore DML", query, got, want)
					}
				}
				maintained := check(t, target, "gin_control_i")
				if maintained.Root == current.Root || len(maintained.Root.Entries) != 3 {
					t.Fatal("postrestore DML did not maintain actual destination root")
				}
				if retained.Root != nil && (target.CatalogRead().Get(retained.Index.Address()) != retained.Index || target.CatalogRead().Get(retained.Parent.Address()) != retained.Parent || target.Storage.IndexRoots[retained.Index.Address()] != retained.Root || !reflect.DeepEqual(retained.Root, retainedCopy)) {
					t.Fatal("replacement or DML rebound certified retained TEMP consumers")
				}
				if !reflect.DeepEqual(original.Index.SQLIndex.Keys, savedKeys) || !reflect.DeepEqual(original.Root, savedRoot) {
					t.Fatal("replacement or DML mutated immutable source authority")
				}
			}
		})
	}
}

func TestPgstatginindexCaptureCost(t *testing.T) {
	for _, placement := range []string{"main", "pending"} {
		t.Run(placement, func(t *testing.T) {
			pgstatginindexCaptureCostPlacement(t, placement)
		})
	}
}

func pgstatginindexCaptureCostPlacement(t *testing.T, placement string) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE gin_cost(id integer,a integer[])")
	options := " WITH(fastupdate=off)"
	if placement == "pending" {
		options = "" // Exercise genuine default-fastupdate deferred DML.
	}
	enginetest.Run(t, e, "CREATE INDEX gin_cost_i ON gin_cost USING gin(a)"+options)
	capture := func() float64 {
		t.Helper()
		c := pgstatindexCaptureFixture(t, e, nil, "gin_cost_i")
		c.Store = storage.Relation{}
		// Deliberately unusable private heap outputs prove that the fixed
		// physical model does not demand or evaluate parent projections.
		c.Writes = []storage.RowVersion{{}}
		return testing.AllocsPerRun(10, func() {
			got, err := index.CapturePgstattupleStats(context.Background(), c)
			if err != nil || got.Version != 2 || got.PendingPages != 0 || got.PendingTuples != 0 {
				t.Fatalf("capture %+v: %v", got, err)
			}
		})
	}
	empty := capture()
	enginetest.Run(t, e, "INSERT INTO gin_cost SELECT n,ARRAY[n,n+1,n+2] FROM generate_series(1,2048) AS s(n)")
	large := pgstatindexCaptureFixture(t, e, nil, "gin_cost_i")
	if len(large.Root.Entries) != 2048 {
		t.Fatal("cost fixture lacks incumbent row-version population")
	}
	if placement == "pending" {
		if len(large.Root.Gin.Columns[0].Tokens) != 0 || len(large.Root.Gin.Pending) != 1 || len(large.Root.Gin.Pending[0].Tokens) < 2048 || storage.GinPendingIncidences(large.Root.Gin) != 3*2048 {
			t.Fatal("cost fixture lacks genuine pending posting population")
		}
	} else if len(large.Root.Gin.Columns[0].Tokens) < 2048 || len(large.Root.Gin.Pending) != 0 {
		t.Fatal("cost fixture lacks incumbent main posting population")
	}
	if populated := capture(); empty != 0 || populated != 0 {
		t.Fatalf("capture allocations empty=%g populated=%g, want zero", empty, populated)
	}
}

func TestPgstatginindexCaptureTargetChecks(t *testing.T) {
	e := engine.New()
	pgstatindexUnitInstalled(t, e)
	for _, q := range []string{"CREATE TABLE gin_target(a integer[])", "CREATE INDEX gin_target_i ON gin_target USING gin(a)", "CREATE ACCESS METHOD gin_target_alias TYPE INDEX HANDLER ginhandler", "CREATE TABLE gin_partitioned(id integer,a integer[]) PARTITION BY RANGE(id)"} {
		enginetest.Run(t, e, q)
	}
	target := pgstatindexCaptureFixture(t, e, nil, "gin_target_i").Index
	parent := pgstatindexCaptureFixture(t, e, nil, "gin_target_i").Parent
	alias := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.AccessMethodClassID, Name: "gin_target_alias"})
	method, ok := e.CatalogRead().AccessMethod(alias.ObjectID)
	if !ok || method.Native != catalog.GinAccessMethod || method.Handler != 333 || method.Address().ObjectID == catalog.GinMethod.OID() {
		t.Fatal("fixture lacks genuine different-OID GIN implementation", method)
	}
	partitioned := e.CatalogRead().MustRelation(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: "gin_partitioned"}))
	if alias.ObjectID == 0 || partitioned.Partition == nil {
		t.Fatal("fixture lacks actual custom AM or partition descriptor")
	}
	before := e.Catalog.State
	defer func() { e.Mu.Lock(); e.Catalog.PublishLocked(before); e.Mu.Unlock() }()
	for _, tc := range []struct {
		name          string
		method        ids.OID
		kind          catalog.RelationKind
		code, message string
	}{
		{"invalid", catalog.GinMethod.OID(), catalog.IndexRelation, "55000", "index \"gin_target_i\" is not valid"},
		{"btree", catalog.BtreeMethod.OID(), catalog.IndexRelation, "42809", "relation \"gin_target_i\" is not a GIN index"},
		{"hash", catalog.HashMethod.OID(), catalog.IndexRelation, "42809", "relation \"gin_target_i\" is not a GIN index"},
		{"custom_am", alias.ObjectID, catalog.IndexRelation, "42809", "relation \"gin_target_i\" is not a GIN index"},
		{"table", catalog.GinMethod.OID(), catalog.TableRelation, "42809", "relation \"gin_target_i\" is not a GIN index"},
		{"partitioned_index", catalog.GinMethod.OID(), catalog.IndexRelation, "42809", "relation \"gin_target_i\" is not a GIN index"},
	} {
		state := catalog.CloneState(before)
		bad := *target
		definition := *target.SQLIndex
		definition.Valid = false
		// Keep the genuine GIN implementation/root, but vary the declared AM.
		// No custom opclass or partitioned GIN creation prerequisite is needed.
		definition.MethodOID = tc.method
		if tc.name == "partitioned_index" {
			p := *parent
			p.RelationKind, p.Partition = catalog.PartitionedRelation, partitioned.Partition
			state.SetDescriptor(p.Address(), &p)
		}
		bad.SQLIndex, bad.RelationKind = &definition, tc.kind
		bad.ObjectRevision++
		state.SetDescriptor(bad.Address(), &bad)
		if tc.name == "partitioned_index" && pgcatalog.ClassKind(e.Catalog.Read(state), &bad) != "I" {
			t.Fatal("descriptor is not genuine partitioned-index relkind")
		}
		e.Mu.Lock()
		e.Catalog.PublishLocked(state)
		e.Mu.Unlock()
		amcheckUnitDiagnostic(t, amcheckUnitCall(t, e, "SELECT pgstatginindex('gin_target_i'::regclass)"), sqlstate.Error{Code: tc.code, Message: tc.message})
	}
}
