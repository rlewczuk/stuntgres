package core_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqlstate"
	"testing"
	"testing/synctest"
	"time"
)

func TestPGStatAllIndexesNativeReplacement(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source, sourceSession, _ := indexStatsSetup(t)
		statsFunctionExec(t, sourceSession, "CREATE TEMP TABLE stat_foreign_temp(n integer)", "CREATE INDEX stat_foreign_temp_idx ON stat_foreign_temp(n)")
		var dump bytes.Buffer
		if err := engine.Dump(context.Background(), source, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump.String(), "idx_scan") || strings.Contains(dump.String(), "last_idx_scan") {
			t.Fatal("runtime statistics serialized")
		}
		target, setup, persistent := indexStatsSetup(t)
		statsFunctionExec(t, setup, "SELECT payload FROM stat_index_rows WHERE n=2")
		statsFunctionRetire(t, setup)
		keeper := enginetest.StartSession(t, target, nil)
		statsFunctionExec(t, keeper, "CREATE TEMP TABLE stat_keep(n integer)", "INSERT INTO stat_keep VALUES(2),(2)", "CREATE INDEX stat_keep_idx ON stat_keep USING hash(n)", "SET enable_seqscan=off", "SELECT n FROM stat_keep WHERE n=2")
		var temp statistics.Key
		for _, d := range target.CatalogRead().Relations() {
			if d.Name() == "stat_keep_idx" {
				temp = statistics.Key{Object: target.Catalog.Domain.Ref(target.CatalogRead().Epoch(), d.Address())}
			}
		}
		// The existing one-shot idle deadline publishes without retiring the live
		// destination TEMP owner. All elapsed time here is synctest virtual time.
		time.Sleep(10 * time.Second)
		synctest.Wait()
		indexStatsWant(t, target, temp, 1, 2, 2)
		beforeFact := target.Statistics.Read(temp)
		retained := indexStatsRows(t, keeper, "SELECT indexrelid,idx_scan FROM pg_stat_all_indexes WHERE indexrelname='stat_keep_idx'")
		oldRow := append([]any(nil), retained[0]...)
		var late statistics.Writer
		late.IndexAccess(&target.Statistics, temp, statistics.Policy{Counts: true}, 17, 19, 23)
		base := target.CatalogRead().State()
		bad := dump.String() + "\nCREATE TABLE broken(n nonexistent_type);"
		if err := engine.Restore(context.Background(), target, strings.NewReader(bad)); err == nil || target.CatalogRead().State() != base {
			t.Fatal("late failure changed target", err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		if err := engine.Restore(canceled, target, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) || target.CatalogRead().State() != base {
			t.Fatal("canceled restore changed target", err)
		}
		if target.Statistics.Read(temp) != beforeFact {
			t.Fatal("failed restore changed history")
		}
		statsFunctionExec(t, keeper, "SELECT n FROM stat_keep WHERE n=2")
		if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		nextTemp := statistics.Key{Object: target.Catalog.Domain.Ref(target.CatalogRead().Epoch(), temp.Object.Address)}
		indexStatsWant(t, target, nextTemp, 1, 2, 2)
		restored := statistics.Key{Object: enginetest.RoleTableRef(t, target, "stat_index_hash")}
		if restored.Object == persistent.Object {
			t.Fatal("restore kept old epoch")
		}
		indexStatsWant(t, target, restored, 0, 0, 0)
		late.Flush(&target.Statistics, time.Now(), true)
		indexStatsWant(t, target, nextTemp, 1, 2, 2)
		// Drain the actual old-incarnation session batch while retaining its TEMP
		// owner; the replacement must reject it, not replay it under the new key.
		time.Sleep(10 * time.Second)
		synctest.Wait()
		indexStatsWant(t, target, nextTemp, 1, 2, 2)
		if !reflect.DeepEqual(retained[0], oldRow) {
			t.Fatal("retained SQL row mutated")
		}
		if rows := indexStatsRows(t, keeper, "SELECT * FROM pg_stat_all_indexes WHERE indexrelname='stat_foreign_temp_idx'"); len(rows) != 0 {
			t.Fatal("source TEMP imported", rows)
		}
		if rows := indexStatsRows(t, keeper, "SELECT n FROM stat_keep WHERE n=2"); len(rows) != 2 {
			t.Fatal("retained TEMP path broken", rows)
		}
		if err := engine.Reset(context.Background(), target); err != nil {
			t.Fatal(err)
		}
		newestTemp := statistics.Key{Object: target.Catalog.Domain.Ref(target.CatalogRead().Epoch(), temp.Object.Address)}
		indexStatsWant(t, target, newestTemp, 1, 2, 2)
		time.Sleep(10 * time.Second)
		synctest.Wait()
		indexStatsWant(t, target, newestTemp, 1, 2, 2)
		if rows := indexStatsRows(t, keeper, "SELECT * FROM pg_stat_all_indexes WHERE indexrelname='stat_index_hash'"); len(rows) != 0 {
			t.Fatal("Reset retained persistent index")
		}
		if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
			t.Fatal("direct catalog replacement", err)
		}
		directTemp := statistics.Key{Object: target.Catalog.Domain.Ref(target.CatalogRead().Epoch(), temp.Object.Address)}
		indexStatsWant(t, target, directTemp, 1, 2, 2)
		indexStatsWant(t, target, statistics.Key{Object: enginetest.RoleTableRef(t, target, "stat_index_hash")}, 0, 0, 0)
	})
}

func TestPGStatAllIndexesReadOnly(t *testing.T) {
	e, s, key := indexStatsSetup(t)
	before := e.Statistics.Read(key)
	for _, tc := range []struct{ q, op, code, message, detail, hint string }{
		{"INSERT INTO pg_stat_all_indexes(relname) VALUES('x')", "INSERT", "55000", `cannot insert into view "pg_stat_all_indexes"`, "Views that do not select from a single table or view are not automatically updatable.", "To enable inserting into the view, provide an INSTEAD OF INSERT trigger or an unconditional ON INSERT DO INSTEAD rule."},
		{"UPDATE pg_stat_all_indexes SET idx_scan=9 WHERE FALSE", "UPDATE", "55000", `cannot update view "pg_stat_all_indexes"`, "Views that do not select from a single table or view are not automatically updatable.", "To enable updating the view, provide an INSTEAD OF UPDATE trigger or an unconditional ON UPDATE DO INSTEAD rule."},
		{"DELETE FROM pg_stat_all_indexes WHERE FALSE", "DELETE", "55000", `cannot delete from view "pg_stat_all_indexes"`, "Views that do not select from a single table or view are not automatically updatable.", "To enable deleting from the view, provide an INSTEAD OF DELETE trigger or an unconditional ON DELETE DO INSTEAD rule."},
		{"TRUNCATE pg_stat_all_indexes", "TRUNCATE", "42809", `"pg_stat_all_indexes" is not a table`, "", ""},
	} {
		err := statsFunctionSQL(t, s, tc.q)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Detail != tc.detail || diagnostic.Hint != tc.hint {
			t.Fatal(tc.op, "diagnostic", err)
		}
		if e.Statistics.Read(key) != before {
			t.Fatal("mutation changed index facts")
		}
		if rows := indexStatsRows(t, s, "SELECT payload FROM stat_index_rows ORDER BY payload"); len(rows) != 5 {
			t.Fatal("sentinel changed", rows)
		}
	}
}

func TestPGStatAllIndexesPrivileges(t *testing.T) {
	e, s, key := indexStatsSetup(t)
	statsFunctionExec(t, s, "CREATE ROLE index_private_reader LOGIN", "CREATE SCHEMA index_private", "ALTER TABLE stat_index_rows SET SCHEMA index_private")
	// Default private schema/table ACLs deny this reader. The index view must
	// nevertheless expose the real parent, namespace, names and zero counters.
	reader := enginetest.StartSession(t, e, nil)
	statsFunctionExec(t, reader, "SET SESSION AUTHORIZATION index_private_reader")
	err := statsFunctionSQL(t, reader, "SELECT n FROM index_private.stat_index_rows")
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
		t.Fatal("parent privilege independently denied", err)
	}
	rows := indexStatsRows(t, reader, "SELECT indexrelid,schemaname,relname,indexrelname,idx_scan,last_idx_scan FROM pg_stat_all_indexes WHERE indexrelname='stat_index_hash'")
	want := [][]any{{int64(key.Object.Address.ObjectID), "index_private", "stat_index_rows", "stat_index_hash", int64(0), nil}}
	if !reflect.DeepEqual(rows, want) {
		t.Fatal("parent ACL filtered/redacted view", rows)
	}
	statsFunctionExec(t, s, "SELECT payload FROM index_private.stat_index_rows WHERE n=2")
	statsFunctionRetire(t, s)
	if rows := indexStatsRows(t, reader, "SELECT idx_scan,idx_tup_read,idx_tup_fetch FROM pg_stat_all_indexes WHERE indexrelname='stat_index_hash'"); !reflect.DeepEqual(rows, [][]any{{int64(1), int64(2), int64(2)}}) {
		t.Fatal("positive facts redacted", rows)
	}
	ref := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatAllIndexesOID})
	original := e.CatalogRead().MustRelation(ref.Address).Security.ACL
	statsFunctionExec(t, reader, "PREPARE index_acl AS SELECT * FROM pg_stat_all_indexes WHERE FALSE", "EXECUTE index_acl")
	held := core.PayloadOf(reader).PreparedSQL.Lookup("index_acl")
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	err = statsFunctionSQL(t, reader, "SELECT * FROM pg_stat_all_indexes WHERE FALSE")
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for view pg_stat_all_indexes" {
		t.Fatal("view ACL bypassed", err)
	}
	err = statsFunctionSQL(t, reader, "EXECUTE index_acl")
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for view pg_stat_all_indexes" {
		t.Fatal("held WHERE FALSE cached former view ACL", err)
	}
	if err := enginetest.SetObjectACL(e, context.Background(), ref, original); err != nil {
		t.Fatal(err)
	}
	if rows := indexStatsRows(t, reader, "SELECT indexrelid FROM pg_stat_all_indexes WHERE indexrelname='stat_index_hash'"); len(rows) != 1 {
		t.Fatal("view grant did not restore access", rows)
	}
	statsFunctionExec(t, reader, "EXECUTE index_acl")
	if held == nil || core.PayloadOf(reader).PreparedSQL.Lookup("index_acl") != held {
		t.Fatal("ACL change replaced the prepared handle")
	}
}
