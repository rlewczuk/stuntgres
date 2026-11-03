package core_test

import (
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
)

func statioPrivateKey(t *testing.T, e *core.Engine, s *session.Session, name string) statistics.Key {
	t.Helper()
	for address, descriptor := range s.Transactions.Catalog.Descriptors {
		if d, ok := descriptor.(*catalog.Relation); ok && d.Name() == name && d.SQLIndex != nil {
			return statistics.Key{Object: e.Catalog.Domain.Ref(s.Transactions.Ref.Epoch(), address), Category: statistics.Relation}
		}
	}
	t.Fatal("missing real private index", name)
	return statistics.Key{}
}
func statioNamedKey(t *testing.T, e *core.Engine, name string) statistics.Key {
	t.Helper()
	read := e.CatalogRead()
	for _, descriptor := range read.Descriptors() {
		if d, ok := descriptor.(*catalog.Relation); ok && d.SQLIndex != nil && d.Name() == name {
			return statistics.Key{Object: e.Catalog.Domain.Ref(read.Epoch(), d.Address()), Category: statistics.Relation}
		}
	}
	t.Fatal("missing real represented index", name)
	return statistics.Key{}
}

func statioPrivateLifetime(t *testing.T) {
	for _, finish := range []string{"commit", "abort", "savepoint"} {
		t.Run(finish, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s, "SET enable_seqscan=off", "BEGIN")
			if finish == "savepoint" {
				statsFunctionExec(t, s, "SAVEPOINT created")
			}
			statsFunctionExec(t, s, "CREATE TABLE statio_private(k integer)", "INSERT INTO statio_private VALUES(1)", "CREATE INDEX statio_private_i ON statio_private USING hash(k)")
			key := statioPrivateKey(t, e, s, "statio_private_i")
			statsFunctionExec(t, s, "SELECT k FROM statio_private WHERE k=1")
			statioFact(t, e, key, 0, 0)
			var late statistics.Writer
			late.IndexBlock(&e.Statistics, key, statistics.Policy{Counts: true}, false)
			switch finish {
			case "commit":
				statsFunctionExec(t, s, "COMMIT")
			case "savepoint":
				statsFunctionExec(t, s, "ROLLBACK TO created", "COMMIT")
			default:
				statsFunctionExec(t, s, "ROLLBACK")
			}
			if finish == "commit" {
				statsFunctionExec(t, s, "SELECT k FROM statio_private WHERE k=1")
				statsFunctionRetire(t, s)
				statioFact(t, e, key, 1, 0)
			} else {
				late.Flush(&e.Statistics, time.Now(), true)
				statsFunctionRetire(t, s)
				statioFact(t, e, key, 0, 0)
				if e.CatalogRead().Get(key.Object.Address) != nil {
					t.Fatal("discarded identity remained")
				}
			}
		})
	}
	t.Run("excluded_sources_keep_generic_requests", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, s,
				"CREATE TEMP TABLE statio_excluded_temp(k integer)",
				"INSERT INTO statio_excluded_temp VALUES(1)",
				"CREATE INDEX statio_excluded_temp_i ON statio_excluded_temp USING hash(k)",
				"BEGIN",
				"CREATE TABLE statio_excluded_private(k integer)",
				"INSERT INTO statio_excluded_private VALUES(1)",
				"CREATE INDEX statio_excluded_private_i ON statio_excluded_private USING hash(k)",
				"SELECT k FROM statio_excluded_temp",
				"SELECT k FROM statio_excluded_private")
			temp := statioNamedKey(t, e, "statio_excluded_temp_i")
			private := statioPrivateKey(t, e, s, "statio_excluded_private_i")
			e.Mu.Lock()
			read := e.PrivateCatalogLocked(s.Transactions.Catalog)
			e.Mu.Unlock()
			tempTable := read.MustRelation(read.MustRelation(temp.Object.Address).SQLIndex.Parent)
			namespace, exists := read.Namespace(tempTable.Namespace())
			if !exists || namespace.Temporary != s.SessionToken {
				t.Fatal("TEMP parent does not belong to the captured session", tempTable)
			}
			tempParent := e.Catalog.Domain.Ref(read.Epoch(), tempTable.Address())
			privateParent := enginetest.CatalogRef(t, e, read, "statio_excluded_private")
			blocksUnitPending(t, e, s, tempParent, 1)
			blocksUnitPending(t, e, s, privateParent, 1)
			statioFact(t, e, temp, 0, 0)
			statioFact(t, e, private, 0, 0)
			statsFunctionExec(t, s, "COMMIT")
			// Advance virtual time through the ordinary idle publication deadline
			// while the physical owner and its TEMP descriptors remain live.
			time.Sleep(10 * time.Second)
			synctest.Wait()
			blocksUnitPublished(t, e, tempParent, 1)
			blocksUnitPublished(t, e, privateParent, 1)
			statioFact(t, e, temp, 0, 0)
			statioFact(t, e, private, 0, 0)
			statsFunctionRetire(t, s)
			blocksUnitPublished(t, e, tempParent, 0)
			blocksUnitPublished(t, e, privateParent, 1)
			if e.CatalogRead().Get(tempParent.Address) != nil || e.CatalogRead().Get(privateParent.Address) == nil {
				t.Fatal("physical retirement did not distinguish TEMP and committed parents")
			}
			statioFact(t, e, temp, 0, 0)
			statioFact(t, e, private, 0, 0)
		})
	})
	t.Run("temp_and_committed_private_namespace", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "SET enable_seqscan=off", "CREATE TEMP TABLE statio_temp(k integer)", "INSERT INTO statio_temp VALUES(1)", "CREATE INDEX statio_temp_i ON statio_temp USING hash(k)")
		temp := statioNamedKey(t, e, "statio_temp_i")
		statsFunctionExec(t, s, "SELECT k FROM statio_temp WHERE k=1", "CREATE SCHEMA statio_private_schema", "REVOKE ALL ON SCHEMA statio_private_schema FROM PUBLIC", "CREATE TABLE statio_private_schema.statio_real(k integer)", "INSERT INTO statio_private_schema.statio_real VALUES(1)", "CREATE INDEX statio_real_i ON statio_private_schema.statio_real USING hash(k)", "SELECT k FROM statio_private_schema.statio_real WHERE k=1")
		real := statioNamedKey(t, e, "statio_real_i")
		statsFunctionRetire(t, s)
		statioFact(t, e, temp, 0, 0)
		statioFact(t, e, real, 1, 0)
	})
	t.Run("genuine_late_session_batch", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			e := engine.New()
			statioSeed(t, e)
			key, control := statioKey(t, e, "statio_events_i"), statioKey(t, e, "statio_events_unused")
			actor := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, actor, "SET enable_seqscan=off")
			// Establish the positive prefix at the ordinary shared boundary.
			time.Sleep(time.Second)
			statsFunctionExec(t, actor, "SELECT k FROM statio_events WHERE k=1")
			statioFact(t, e, key, 1, 0)
			statsFunctionExec(t, actor, "SELECT k FROM statio_events WHERE k=1", "SELECT v FROM statio_events WHERE v=10")
			pending := reflect.ValueOf(core.PayloadOf(actor)).Elem().FieldByName("statsWriter").FieldByName("pending").MapIndex(reflect.ValueOf(key))
			if !pending.IsValid() || pending.FieldByName("IndexBlocksHit").Int() != 1 {
				t.Fatal("actual read is not pending under throttle")
			}
			dropper := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, dropper, "DROP INDEX statio_events_i")
			statioFact(t, e, key, 0, 0)
			statsFunctionRetire(t, actor)
			statioFact(t, e, key, 0, 0)
			statioFact(t, e, control, 1, 0)
		})
	})
	t.Run("effective_proconfig", func(t *testing.T) {
		e := engine.New()
		statioSeed(t, e)
		s := enginetest.StartSession(t, e, nil)
		statsFunctionExec(t, s, "SET enable_seqscan=off", "CREATE FUNCTION statio_off() RETURNS integer LANGUAGE sql VOLATILE SET track_counts=off AS 'SELECT k FROM statio_events WHERE k=1'", "SELECT statio_off()")
		statioFact(t, e, statioKey(t, e, "statio_events_i"), 0, 0)
		statsFunctionExec(t, s, "SELECT k FROM statio_events WHERE k=1")
		statsFunctionRetire(t, s)
		statioFact(t, e, statioKey(t, e, "statio_events_i"), 0, 1)
	})
}
