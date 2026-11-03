package reference_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatioUserIndexesLifecycle(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		owner, peer := vacuumConn(t, p, b), vacuumConn(t, p, b)
		statioExec(t, p, b, owner, "BEGIN", "CREATE TABLE statio_user_private(k integer)", "CREATE INDEX statio_user_private_i ON statio_user_private USING hash(k)")
		vacuumRun(t, p, b, owner, fixture.Exec("INSERT INTO statio_user_private VALUES(1)", 1))
		statioUserNames(t, p, b, owner, "relname='statio_user_private'", "statio_user_private_i")
		statioUserNames(t, p, b, peer, "relname='statio_user_private'")
		if !b.PG && statioUserPair(t, p, owner, "statio_user_private_i") != ([2]int64{}) {
			t.Fatal("private index collected")
		}
		statioExec(t, p, b, owner, "ROLLBACK")
		statioUserNames(t, p, b, owner, "relname='statio_user_private'")
		statioExec(t, p, b, owner, "BEGIN", "CREATE TABLE statio_user_life(k integer)", "CREATE INDEX statio_user_life_i ON statio_user_life USING hash(k)", "COMMIT")
		vacuumRun(t, p, b, owner, fixture.Exec("INSERT INTO statio_user_life VALUES(1)", 1))
		statioUserNames(t, p, b, peer, "relname='statio_user_life'", "statio_user_life_i")
		statioTouch(t, p, b, "statio_user_life")
		parent := statioIdentity(t, p, owner, "statio_user_life")
		oldID := statioIdentity(t, p, owner, "statio_user_life_i")
		statioExec(t, p, b, owner, "SET stats_fetch_consistency='snapshot'", "BEGIN")
		pair := statioUserPair(t, p, owner, "statio_user_life_i")
		before := statioRows(t, p, b, peer, "SELECT * FROM pg_statio_user_indexes WHERE relname='statio_user_life'")
		held, err := peer.QueryContext(p.Ctx, "SELECT * FROM pg_statio_user_indexes WHERE relname='statio_user_life'")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		metadata := statioMetadata(t, held, b.PG)
		t.Cleanup(func() {
			copyProgressExec(t, p.Ctx, owner, "ROLLBACK")
			copyProgressExec(t, p.Ctx, owner, "DROP TABLE IF EXISTS information_schema.statio_user_parent")
		})
		statioExec(t, p, b, owner, "ALTER INDEX statio_user_life_i RENAME TO statio_user_renamed", "ALTER TABLE statio_user_life RENAME TO statio_user_parent", "ALTER TABLE statio_user_parent SET SCHEMA information_schema")
		if statioIdentity(t, p, owner, "information_schema.statio_user_renamed") != oldID || statioIdentity(t, p, owner, "information_schema.statio_user_parent") != parent {
			t.Fatal("move/rename changed real identities")
		}
		statioUserNames(t, p, b, owner, "relname='statio_user_parent'")
		statioNamesFor(t, p, b, owner, "relname='statio_user_parent'", "statio_user_renamed")
		if got := statioPair(t, p, owner, "statio_user_renamed"); got != pair {
			t.Fatal("move lost history", got, pair)
		}
		if retained := statioConsumeRows(t, held, b.PG); !reflect.DeepEqual(retained, before) {
			t.Fatal("held rows changed", retained, before)
		}
		if metadata[0].DatabaseTypeName() != "OID" || metadata[6].DatabaseTypeName() != "INT8" {
			t.Fatal("held metadata changed")
		}
		statioExec(t, p, b, owner, "ALTER TABLE information_schema.statio_user_parent SET SCHEMA public")
		statioUserNames(t, p, b, owner, "relname='statio_user_parent'", "statio_user_renamed")
		if got := statioUserPair(t, p, owner, "statio_user_renamed"); got != pair {
			t.Fatal("return lost history", got, pair)
		}
		statioExec(t, p, b, owner, "COMMIT", "SET stats_fetch_consistency='none'")
		statioExec(t, p, b, owner, "BEGIN", "DROP INDEX statio_user_renamed", "ROLLBACK")
		statioUserNames(t, p, b, peer, "relname='statio_user_parent'", "statio_user_renamed")
		statioExec(t, p, b, owner, "BEGIN", "SAVEPOINT statio_user_save", "CREATE INDEX statio_user_discard ON statio_user_parent(k)", "ROLLBACK TO statio_user_save", "COMMIT")
		statioUserNames(t, p, b, peer, "relname='statio_user_parent'", "statio_user_renamed")
		statioExec(t, p, b, owner, "CREATE INDEX statio_user_sibling ON statio_user_parent(k)")
		prepared, err := peer.PrepareContext(p.Ctx, "SELECT * FROM pg_statio_user_indexes WHERE relname='statio_user_parent' ORDER BY indexrelname")
		if err != nil {
			t.Fatal(err)
		}
		defer prepared.Close()
		statioExec(t, p, b, owner, "DROP INDEX statio_user_renamed")
		rows, err := prepared.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := statioConsumeRows(t, rows, b.PG); len(got) != 1 || got[0][4] != "statio_user_sibling" {
			t.Fatal("DROP INDEX removed wrong identities", got)
		}
		statioExec(t, p, b, owner, "CREATE INDEX statio_user_renamed ON statio_user_parent USING hash(k)")
		if statioIdentity(t, p, owner, "statio_user_renamed") == oldID {
			t.Fatal("successor reused old identity")
		}
		if got := statioUserPair(t, p, owner, "statio_user_renamed"); !b.PG && got != ([2]int64{}) {
			t.Fatal("successor inherited retired history", got)
		}
		statioExec(t, p, b, owner, "DROP TABLE statio_user_parent")
		rows, err = prepared.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := statioConsumeRows(t, rows, b.PG); len(got) != 0 {
			t.Fatal("held prepared retained dropped parent", got)
		}
		statioNamesFor(t, p, b, owner, "relname='statio_user_parent'")
		statioExec(t, p, b, owner, "CREATE TEMP TABLE statio_user_temp(k integer)", "CREATE INDEX statio_user_temp_i ON statio_user_temp USING hash(k)", "SET enable_seqscan=off")
		vacuumRun(t, p, b, owner, fixture.Exec("INSERT INTO statio_user_temp VALUES(1)", 1))
		vacuumRun(t, p, b, owner, fixture.Select("SELECT k FROM statio_user_temp WHERE k=1", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		for _, c := range []fixture.Executor{owner, peer} {
			statioUserNames(t, p, b, c, "relname='statio_user_temp'", "statio_user_temp_i")
		}
		if !b.PG && statioUserPair(t, p, owner, "statio_user_temp_i") != ([2]int64{}) {
			t.Fatal("TEMP shared cache collection")
		}
		statioExec(t, p, b, owner, "DROP TABLE statio_user_temp")
		if b.PG {
			return
		}
		// Public native replacement controls rebuild catalog definitions, but do
		// not serialize virtual views or ephemeral selected provider history.
		statioExec(t, p, b, owner, "CREATE TABLE statio_user_restore(k integer)", "CREATE INDEX statio_user_restore_i ON statio_user_restore USING hash(k)")
		vacuumRun(t, p, b, owner, fixture.Exec("INSERT INTO statio_user_restore VALUES(1)", 1))
		statioTouch(t, p, b, "statio_user_restore")
		previous := statioIdentity(t, p, owner, "statio_user_restore_i")
		oldRows, err := peer.QueryContext(p.Ctx, "SELECT * FROM pg_statio_user_indexes WHERE relname='statio_user_restore'")
		if err != nil {
			t.Fatal(err)
		}
		defer oldRows.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(dump.String(), "\n") {
			if strings.Contains(line, "CREATE VIEW") && strings.Contains(line, "pg_statio_user_indexes") {
				t.Fatal("virtual view serialized", line)
			}
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		statioUserNames(t, p, b, owner, "relname='statio_user_restore'")
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		statioUserNames(t, p, b, owner, "relname='statio_user_restore'", "statio_user_restore_i")
		if statioIdentity(t, p, owner, "statio_user_restore_i") == previous {
			t.Fatal("Restore reused old qualified identity")
		}
		if got := statioUserPair(t, p, owner, "statio_user_restore_i"); got != ([2]int64{}) {
			t.Fatal("Restore serialized provider history", got)
		}
		if retained := statioConsumeRows(t, oldRows, false); len(retained) != 1 || retained[0][1] != previous || retained[0][5] != int64(1) || retained[0][6] != int64(0) {
			t.Fatal("held pre-replacement values", retained)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString("CREATE TABLE bad(k missing_type);")); err == nil {
			t.Fatal("invalid Restore accepted")
		}
		vacuumRun(t, p, b, owner, fixture.Select("SELECT k FROM statio_user_restore", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		vacuumRun(t, p, b, owner, fixture.Select("SELECT c.reltype=t.oid AS row_link,a.typelem=t.oid AS array_link FROM pg_class c JOIN pg_type t ON t.typrelid=c.oid JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_statio_user_indexes'::regclass", []string{"row_link", "array_link"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	})
}
