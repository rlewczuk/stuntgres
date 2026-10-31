package core_test

import (
	"context"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
)

func statsPrivateFunctionKey(t *testing.T, e *core.Engine, actor *session.Session, name string, journals ...*session.Journal) statistics.Key {
	t.Helper()
	for _, journal := range journals {
		if journal == nil {
			continue
		}
		for address, descriptor := range journal.Descriptors {
			if d, ok := descriptor.(*catalog.Procedure); ok && d.Name() == name {
				return statistics.Key{Object: e.Catalog.Domain.Ref(actor.Transactions.Ref.Epoch(), address), Category: statistics.Function}
			}
		}
	}
	t.Fatal("missing actual private function", name)
	return statistics.Key{}
}

func statsLifetimeFact(t *testing.T, e *core.Engine, key statistics.Key, calls, inserts int64) {
	t.Helper()
	got := e.Statistics.Read(key)
	if got.Calls != calls || got.Inserts != inserts {
		t.Fatalf("key %+v calls/inserts=%d/%d, want %d/%d", key, got.Calls, got.Inserts, calls, inserts)
	}
	if calls == 0 && inserts == 0 {
		got.ResetAt, got.Generation = time.Time{}, 0
		if got != (statistics.Fact{}) { t.Fatal("retired identity retained cumulative fields", key, got) }
		var fresh statistics.Reader
		fresh.Configure("snapshot", 1)
		captured := fresh.Read(&e.Statistics, key, time.Now())
		captured.ResetAt, captured.Generation = time.Time{}, 0
		if captured != (statistics.Fact{}) { t.Fatal("fresh capture retained orphan statistics", key, captured) }
		if statsLifetimeSnapshotContains(&fresh, key) {
			t.Fatal("fresh full snapshot retained a zero-valued orphan entry", key)
		}
	}
}

// Test-only ownership inspection distinguishes a missing captured key from a
// zero-valued map entry, without adding a production inspection API.
func statsLifetimeSnapshotContains(reader *statistics.Reader, key statistics.Key) bool {
	return reflect.ValueOf(reader).Elem().FieldByName("cache").MapIndex(reflect.ValueOf(key)).IsValid()
}

func statsLifetimeFinishPrepared(t *testing.T, e *core.Engine, actor *session.Session, commit bool, beforeFinish func()) {
	t.Helper()
	statsFunctionExec(t, actor, "PREPARE TRANSACTION 'stats_lifetime_owner'")
	// Keep the preparing writer alive across completion by another session.
	// Its later terminal drainage must not resurrect a retired identity.
	beforeFinish()
	finisher := enginetest.StartSession(t, e, nil)
	t.Cleanup(func() {
		if core.EnginePreparedRef(e).Lookup("stats_lifetime_owner") != nil {
			statsFunctionExec(t, finisher, "ROLLBACK PREPARED 'stats_lifetime_owner'")
		}
	})
	end := "ROLLBACK PREPARED 'stats_lifetime_owner'"
	if commit {
		end = "COMMIT PREPARED 'stats_lifetime_owner'"
	}
	statsFunctionExec(t, finisher, end)
	statsFunctionRetire(t, finisher)
}

func TestStatsSnapshotObjectLifetime(t *testing.T) {
	t.Run("actual_session_late_pending_writer", statsLifetimeActualPendingWriter)
	for _, kind := range []string{"abort", "commit", "savepoint_discarded", "savepoint_retained", "released_child_abort", "prepared_abort", "prepared_commit", "create_drop_abort", "create_drop_commit"} {
		t.Run("function_creation/"+kind, func(t *testing.T) {
			e := engine.New()
			actor := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, actor, "SET track_functions=pl", "BEGIN")
			if kind == "savepoint_discarded" || kind == "released_child_abort" {
				statsFunctionExec(t, actor, "SAVEPOINT creation")
			}
			statsFunctionExec(t, actor, "CREATE FUNCTION stats_created_function() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$")
			key := statsPrivateFunctionKey(t, e, actor, "stats_created_function", actor.Transactions.Catalog)
			statsFunctionExec(t, actor, "SELECT stats_created_function()")
			var late statistics.Writer
			late.Add(&e.Statistics, key, statistics.Fact{Calls: 17})
			keep := kind == "commit" || kind == "savepoint_retained" || kind == "prepared_commit"
			switch kind {
			case "prepared_abort", "prepared_commit":
				statsLifetimeFinishPrepared(t, e, actor, keep, func() { statsLifetimeFact(t,e,key,1,0) })
			case "savepoint_discarded":
				statsFunctionExec(t, actor, "ROLLBACK TO creation", "ROLLBACK TO creation", "COMMIT")
			case "savepoint_retained":
				statsFunctionExec(t, actor, "SAVEPOINT creation",
					"CREATE OR REPLACE FUNCTION stats_created_function() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 8; END$$",
					"DROP FUNCTION stats_created_function()", "ROLLBACK TO creation", "ROLLBACK TO creation",
					"SELECT stats_created_function()", "COMMIT")
			case "released_child_abort":
				statsFunctionExec(t, actor, "RELEASE creation", "ROLLBACK")
			case "create_drop_abort", "create_drop_commit":
				statsFunctionExec(t, actor, "DROP FUNCTION stats_created_function()")
				end := "ROLLBACK"
				if kind == "create_drop_commit" { end = "COMMIT" }
				statsFunctionExec(t, actor, end)
			default:
				end := "ROLLBACK"
				if keep { end = "COMMIT" }
				statsFunctionExec(t, actor, end)
			}
			statsFunctionRetire(t, actor)
			if present:=e.CatalogRead().Get(key.Object.Address)!=nil; present!=keep {
				t.Fatal("creation outcome disagrees with actual catalog lifetime", key, present, keep)
			}
			if keep {
				calls := int64(1)
				if kind=="savepoint_retained" { calls=2 }
				statsLifetimeFact(t, e, key, calls, 0)
			} else {
				late.Flush(&e.Statistics, time.Now(), true)
				late.Add(&e.Statistics, key, statistics.Fact{Calls: 19})
				late.Flush(&e.Statistics, time.Now(), true)
				statsLifetimeFact(t, e, key, 0, 0)
			}
		})
	}
	for _, kind := range []string{"direct", "direct_no_operation", "commit", "abort", "savepoint_abort", "released_child_abort", "released_child_commit", "prepared_commit", "prepared_abort", "recreate", "compatible_replacement"} {
		t.Run("existing_drop/"+kind, func(t *testing.T) {
			e := engine.New()
			seed := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, seed,
				"SET track_functions=pl",
				"CREATE TABLE stats_lifetime_table(id integer)",
				"INSERT INTO stats_lifetime_table VALUES(1),(2)",
				"CREATE FUNCTION stats_lifetime_function() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$",
				"CREATE FUNCTION stats_lifetime_survivor() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 11; END$$",
				"SELECT stats_lifetime_function(), stats_lifetime_survivor()",
			)
			tableKey := statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_lifetime_table"), Category: statistics.Relation}
			functionKey := statsFunctionKey(t, e, "stats_lifetime_function")
			survivor := statsFunctionKey(t, e, "stats_lifetime_survivor")
			statsFunctionRetire(t, seed)
			statsLifetimeFact(t, e, tableKey, 0, 2)
			statsLifetimeFact(t, e, functionKey, 1, 0)
			actor := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, actor, "SET track_functions=pl")
			if kind != "direct" && kind != "direct_no_operation" { statsFunctionExec(t, actor, "BEGIN") }
			statsFunctionExec(t, actor, "SELECT stats_lifetime_survivor()")
			if kind == "savepoint_abort" || kind == "released_child_abort" || kind == "released_child_commit" { statsFunctionExec(t, actor, "SAVEPOINT dropped") }
			if kind == "compatible_replacement" {
				statsFunctionExec(t, actor,
					"CREATE OR REPLACE FUNCTION stats_lifetime_function() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 8; END$$",
					"ALTER TABLE stats_lifetime_table ADD COLUMN extra integer",
					"SELECT stats_lifetime_function()",
					"COMMIT",
				)
				statsFunctionRetire(t, actor)
				if statsFunctionKey(t,e,"stats_lifetime_function") != functionKey { t.Fatal("compatible function replacement reminted identity") }
				statsLifetimeFact(t,e,functionKey,2,0)
				statsLifetimeFact(t,e,tableKey,0,2)
				statsLifetimeFact(t,e,survivor,2,0)
				return
			}
			var late statistics.Writer
			late.Add(&e.Statistics, tableKey, statistics.Fact{Inserts: 17})
			late.Add(&e.Statistics, functionKey, statistics.Fact{Calls: 19})
			if kind == "direct_no_operation" {
				base := e.CatalogRead()
				stage := e.BeginCatalogStage(base)
				defer e.DiscardCatalogStage(stage)
				if err:=stage.Remove(tableKey.Object);err!=nil {t.Fatal(err)}
				if err:=stage.Remove(functionKey.Object);err!=nil {t.Fatal(err)}
				prepared,err:=e.ValidateCatalogStage(context.Background(),stage)
				if err!=nil {t.Fatal(err)}
				statsLifetimeFact(t,e,tableKey,0,2)
				statsLifetimeFact(t,e,functionKey,1,0)
				if _,err=enginetest.PublishCatalog(e,prepared,core.PublicationToken{Epoch:base.Epoch()});err!=nil {t.Fatal(err)}
			} else {
				statsFunctionExec(t, actor, "DROP TABLE stats_lifetime_table", "DROP FUNCTION stats_lifetime_function()")
			}
			retired := kind == "direct" || kind == "direct_no_operation" || kind == "commit" || kind == "released_child_commit" || kind == "prepared_commit" || kind == "recreate"
			if kind != "direct" && kind != "direct_no_operation" {
				// Dropping is not retirement before the outer outcome is known.
				statsLifetimeFact(t,e,tableKey,0,2)
				statsLifetimeFact(t,e,functionKey,1,0)
			}
			var newTable,newFunction statistics.Key
			if kind == "recreate" {
				statsFunctionExec(t,actor,
					"CREATE TABLE stats_lifetime_table(id integer)",
					"INSERT INTO stats_lifetime_table VALUES(7)",
					"CREATE FUNCTION stats_lifetime_function() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 9; END$$",
					"SELECT stats_lifetime_function()",
				)
				newTable=statsPrivateMaintenanceKey(t,e,actor,actor.Transactions.Catalog,"stats_lifetime_table")
				newFunction=statsPrivateFunctionKey(t,e,actor,"stats_lifetime_function",actor.Transactions.Catalog)
				if newTable==tableKey || newFunction==functionKey {t.Fatal("drop/recreate reused a retired identity")}
			}
			switch kind {
			case "prepared_abort","prepared_commit":
				statsLifetimeFinishPrepared(t,e,actor,retired,func(){
					statsLifetimeFact(t,e,tableKey,0,2)
					statsLifetimeFact(t,e,functionKey,1,0)
				})
			case "savepoint_abort": statsFunctionExec(t,actor,"ROLLBACK TO dropped","COMMIT")
			case "released_child_abort","released_child_commit":
				statsFunctionExec(t,actor,"RELEASE dropped")
				statsLifetimeFact(t,e,tableKey,0,2)
				statsLifetimeFact(t,e,functionKey,1,0)
				end:="ROLLBACK"
				if kind=="released_child_commit" {end="COMMIT"}
				statsFunctionExec(t,actor,end)
			case "abort": statsFunctionExec(t,actor,"ROLLBACK")
			case "commit","recreate": statsFunctionExec(t,actor,"COMMIT")
			}
			statsFunctionRetire(t,actor)
			for _, key:=range []statistics.Key{tableKey,functionKey} {
				if present:=e.CatalogRead().Get(key.Object.Address)!=nil; present==retired {
					t.Fatal("drop outcome disagrees with actual catalog lifetime", key, present, retired)
				}
			}
			if retired {
				late.Flush(&e.Statistics,time.Now(),true)
				late.Add(&e.Statistics,tableKey,statistics.Fact{Inserts:23})
				late.Function(&e.Statistics,functionKey,statistics.Policy{Functions:"pl"},"pl",false,time.Millisecond,time.Millisecond)
				late.Flush(&e.Statistics,time.Now(),true)
				statsLifetimeFact(t,e,tableKey,0,0)
				statsLifetimeFact(t,e,functionKey,0,0)
			} else {
				statsLifetimeFact(t,e,tableKey,0,2)
				statsLifetimeFact(t,e,functionKey,1,0)
			}
			statsLifetimeFact(t,e,survivor,2,0)
			if kind=="recreate" {
				statsLifetimeFact(t,e,newTable,0,1)
				statsLifetimeFact(t,e,newFunction,1,0)
			}
			if !retired {
				later:=enginetest.StartSession(t,e,nil)
				statsFunctionExec(t,later,"SET track_functions=pl",
					"SELECT stats_lifetime_function(), stats_lifetime_survivor()",
					"INSERT INTO stats_lifetime_table VALUES(3)")
				statsFunctionRetire(t,later)
				statsLifetimeFact(t,e,functionKey,2,0)
				statsLifetimeFact(t,e,tableKey,0,3)
				statsLifetimeFact(t,e,survivor,3,0)
			}
		})
	}
	for _, kind := range []string{"discarded","retained_owner","retained_statement","drop_rollback"} {
		t.Run("pl_exception/"+kind,func(t *testing.T){ statsLifetimePL(t,kind) })
	}
}

func statsLifetimePL(t *testing.T, kind string) {
	e := engine.New()
	actor := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, actor, "SET track_functions=pl", "BEGIN")
	create := "CREATE FUNCTION stats_pl_function() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$"
	var key, tableKey statistics.Key
	if kind == "retained_owner" || kind == "drop_rollback" {
		statsFunctionExec(t, actor, create, "SELECT stats_pl_function()")
	}
	if kind == "drop_rollback" {
		statsFunctionExec(t, actor,
			"CREATE TABLE stats_pl_table(id integer)",
			"INSERT INTO stats_pl_table VALUES(1),(2)", "COMMIT")
		key = statsFunctionKey(t, e, "stats_pl_function")
		tableKey = statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_pl_table"), Category: statistics.Relation}
		statsLifetimeFact(t, e, key, 1, 0)
		statsLifetimeFact(t, e, tableKey, 0, 2)
		statsFunctionExec(t, actor, "BEGIN")
	}
	scope, _ := truncateUnitScope(t, e, actor, context.Background())
	if err := scope.Gate(&parser.Do{}); err != nil {
		t.Fatal(err)
	}
	execute := func(q string) {
		t.Helper()
		restore := scope.ActivateSQLRoutine(nil, e.CatalogRead())
		result, err := scope.Execute(enginetest.Statement(t, q), 0, nil)
		if result.Rows != nil {
			enginetest.Tuples(result)
			if closeErr := result.Rows.Close(); err == nil {
				err = closeErr
			}
		}
		if err = restore(err); err != nil {
			t.Fatal(q, err)
		}
	}
	if kind == "retained_statement" {
		execute(create)
		execute("SELECT stats_pl_function()")
	}
	mark, err := scope.BeginPLException()
	if err != nil {
		t.Fatal(err)
	}
	if kind == "discarded" {
		execute(create)
	}
	execute("SELECT stats_pl_function()")
	if kind != "drop_rollback" {
		key = statsPrivateFunctionKey(t, e, actor, "stats_pl_function", scope.Catalog, actor.Transactions.Catalog)
	}
	if kind == "drop_rollback" {
		execute("DROP FUNCTION stats_pl_function()")
		execute("DROP TABLE stats_pl_table")
	}
	if err := scope.EndPLException(mark, true, nil); err != nil {
		t.Fatal(err)
	}
	if kind == "drop_rollback" {
		execute("SELECT stats_pl_function()")
		execute("INSERT INTO stats_pl_table VALUES(3)")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	scope.Op.Done()
	statsFunctionExec(t, actor, "COMMIT")
	statsFunctionRetire(t, actor)
	want := int64(2)
	if kind == "discarded" {
		want = 0
	} else if kind == "drop_rollback" {
		want = 3
		statsLifetimeFact(t, e, tableKey, 0, 3)
	}
	statsLifetimeFact(t, e, key, want, 0)
}

func statsLifetimeActualPendingWriter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := engine.New()
		setup := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, setup,
			"CREATE TABLE stats_late_control(id integer)",
			"INSERT INTO stats_late_control VALUES(1),(2)",
			"CREATE FUNCTION stats_late_function() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$",
			"CREATE FUNCTION stats_late_survivor() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 11; END$$",
		)
		key := statsFunctionKey(t, e, "stats_late_function")
		survivor := statsFunctionKey(t, e, "stats_late_survivor")
		tableKey := statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_late_control"), Category: statistics.Relation}
		statsFunctionRetire(t, setup)
		statsLifetimeFact(t, e, tableKey, 0, 2)
		actor := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, actor, "SET track_functions=pl")
		call := func() {
			t.Helper()
			op, err := actor.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			result, err := e.Execute(op, enginetest.Statement(t, "SELECT stats_late_function()"), 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.Rows == nil {
				t.Fatal("real function invocation returned no result")
			}
			rows := enginetest.Tuples(result)
			if err := result.Rows.Close(); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != int64(7) {
				t.Fatal("real function invocation result", rows)
			}
		}
		at := time.Now()
		call()
		statsLifetimeFact(t, e, key, 1, 0)
		var retained statistics.Reader
		retained.Configure("snapshot", 1)
		if retained.Read(&e.Statistics, key, at).Calls != 1 || !statsLifetimeSnapshotContains(&retained, key) {
			t.Fatal("initial full snapshot lacks the positively published key")
		}
		call()
		if time.Now() != at {
			t.Fatal("virtual time advanced between the real calls")
		}
		statsLifetimeFact(t, e, key, 1, 0)
		// Inspect the actual session-owned batch after two completed SQL calls.
		// No standalone Writer or injected event stands in for this authority.
		pending := reflect.ValueOf(core.PayloadOf(actor)).Elem().FieldByName("statsWriter").FieldByName("pending").MapIndex(reflect.ValueOf(key))
		if !pending.IsValid() || pending.FieldByName("Calls").Int() != 1 {
			t.Fatal("second real call is not retained in the throttled session batch")
		}
		statsFunctionExec(t, actor, "SELECT stats_late_survivor()")
		statsLifetimeFact(t, e, survivor, 0, 0)
		dropper := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, dropper, "DROP FUNCTION stats_late_function()")
		statsLifetimeFact(t, e, key, 0, 0)
		if e.CatalogRead().Get(key.Object.Address) != nil {
			t.Fatal("other session did not commit the actual function drop")
		}
		// Ordinary physical-session retirement drains the real pending batch.
		// The unrelated pending call must publish, proving drainage occurred.
		statsFunctionRetire(t, actor)
		statsLifetimeFact(t, e, key, 0, 0)
		statsLifetimeFact(t, e, survivor, 1, 0)
		statsLifetimeFact(t, e, tableKey, 0, 2)
		if retained.Read(&e.Statistics, key, at).Calls != 1 || !statsLifetimeSnapshotContains(&retained, key) {
			t.Fatal("retirement mutated a preexisting full snapshot")
		}
		statsFunctionRetire(t, dropper)
	})
}
