package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func regtsPersistentCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	oid := regtsDictionaryOID(t, p, c)
	regtsSelect(t, p, b, c, "SELECT id,d,c,d::oid::bigint AS identity FROM reg_ts_data ORDER BY id", []string{"id", "d", "c", "identity"}, []string{"INT4", "REGDICTIONARY", "REGCONFIG", "INT8"}, [][]any{{int64(1), `"Reg.TS".unaccent`, "-", oid}, {int64(2), "-", "4294967295", int64(0)}, {int64(3), "4294967295", "8", int64(4294967295)}, {int64(4), nil, nil, nil}})
	for _, q := range []string{"SELECT d::regdictionary AS d FROM reg_ts_defaults ORDER BY id", "SELECT d FROM reg_ts_view"} {
		regtsSelect(t, p, b, c, q, []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{`"Reg.TS".unaccent`}})
	}
	regtsSelect(t, p, b, c, "SELECT d::oid::bigint AS identity FROM reg_ts_defaults", []string{"identity"}, []string{"INT8"}, [][]any{{oid}})
	regtsSelect(t, p, b, c, "SELECT d::oid::bigint AS identity FROM reg_ts_view", []string{"identity"}, []string{"INT8"}, [][]any{{oid}})
	regtsSelect(t, p, b, c, "SELECT g,g::oid::bigint AS identity FROM reg_ts_defaults", []string{"g", "identity"}, []string{"REGDICTIONARY", "INT8"}, [][]any{{`"Reg.TS".unaccent`, oid}})
	// Consume restored definition literals, not merely already-stored values.
	regtsExec(t, p, b, c, "INSERT INTO reg_ts_defaults(id) VALUES(2)", 1)
	regtsSelect(t, p, b, c, "SELECT d::regdictionary AS d,g,d::oid::bigint AS di,g::oid::bigint AS gi FROM reg_ts_defaults WHERE id=2", []string{"d", "g", "di", "gi"}, []string{"REGDICTIONARY", "REGDICTIONARY", "INT8", "INT8"}, [][]any{{`"Reg.TS".unaccent`, `"Reg.TS".unaccent`, oid, oid}})
	regtsExec(t, p, b, c, "DELETE FROM reg_ts_defaults WHERE id=2", 1)
	q := "SELECT $1::regdictionary AS d"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	regtsSelect(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, []string{"d"}, []string{"REGDICTIONARY"}, [][]any{{`"Reg.TS".unaccent`}}, `"Reg.TS".unaccent`)
}
func TestReferenceRegTSPersistence(t *testing.T) {
	var emitted []byte
	regtsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsInstall(t, p, b, c)
		for _, q := range []string{
			"CREATE DOMAIN reg_ts_stored AS regdictionary",
			"CREATE TABLE reg_ts_data(id integer,d regdictionary,c regconfig)",
			`CREATE TABLE reg_ts_defaults(id integer,d reg_ts_stored DEFAULT '"Reg.TS".unaccent'::regdictionary CHECK(d='"Reg.TS".unaccent'::regdictionary),g regdictionary GENERATED ALWAYS AS ('"Reg.TS".unaccent'::regdictionary) STORED)`,
			`CREATE VIEW reg_ts_view AS SELECT '"Reg.TS".unaccent'::regdictionary AS d`,
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, `INSERT INTO reg_ts_data VALUES(1,'"Reg.TS".unaccent','-'),(2,'-','4294967295'),(3,'4294967295','010'),(4,NULL,NULL)`, 4)
		regtsExec(t, p, b, c, "INSERT INTO reg_ts_defaults(id) VALUES(1)", 1)
		regtsPersistentCheck(t, p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("actual native dump bytes required for PostgreSQL replay")
			}
			for _, q := range []string{"DROP VIEW reg_ts_view", "DROP TABLE reg_ts_defaults", "DROP TABLE reg_ts_data", "DROP DOMAIN reg_ts_stored", `DROP EXTENSION unaccent`, `DROP SCHEMA "Reg.TS"`} {
				regtsExec(t, p, b, c, q, 0)
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual native dump replay", err)
			}
			regtsPersistentCheck(t, p, b, c)
			return
		}
		oldOID := regtsDictionaryOID(t, p, c)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		if !bytes.Contains(emitted, []byte(`"Reg.TS".unaccent`)) {
			t.Fatal("dump lacks qualified dictionary reference")
		}
		if bytes.Contains(emitted, []byte(fmt.Sprintf("'%d'", oldOID))) {
			t.Fatal("dump pins allocated live dictionary OID")
		}
		retained, err := c.QueryContext(p.Ctx, "SELECT d,c FROM reg_ts_data ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		metadata, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		regtsPersistentCheck(t, p, b, c)
		regtsAssertAliasRows(t, retained, [][]any{{`"Reg.TS".unaccent`, "-"}, {"-", "4294967295"}, {"4294967295", "8"}, {nil, nil}}, false)
		if metadata[0].DatabaseTypeName() != "REGDICTIONARY" || metadata[1].DatabaseTypeName() != "REGCONFIG" {
			t.Fatal("retained metadata changed")
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(emitted, redump.Bytes()) {
			t.Fatal("logical redump changed")
		}
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		// Ordinary objects are replaced, not reserved. Keep an actual TEMP
		// sequence whose observed OID collides with the imported dictionary;
		// replacement must preserve that owner and remap the dictionary class.
		var collider string
		for i := 0; collider == ""; i++ {
			name := fmt.Sprintf("reg_ts_displace_%d", i)
			regtsExec(t, fresh, fb, fc, "CREATE TEMP SEQUENCE "+name, 0)
			var id int64
			if err := fc.QueryRowContext(fresh.Ctx, "SELECT 'pg_temp."+name+"'::regclass::oid::bigint").Scan(&id); err != nil {
				t.Fatal(err)
			}
			if id == oldOID {
				collider = name
			}
			if id > oldOID {
				t.Fatalf("retained allocation passed source dictionary OID without collision: retained=%d source=%d", id, oldOID)
			}
		}
		regtsSelect(t, fresh, fb, fc, "SELECT setval('pg_temp."+collider+"',41,true) AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(41)}})
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		if newOID := regtsDictionaryOID(t, fresh, fc); newOID == oldOID {
			t.Fatalf("allocation witness did not displace OID %d", oldOID)
		}
		regtsSelect(t, fresh, fb, fc, "SELECT 'pg_temp."+collider+"'::regclass::oid::bigint AS id,nextval('pg_temp."+collider+"') AS v", []string{"id", "v"}, []string{"INT8", "INT8"}, [][]any{{oldOID, int64(42)}})
		regtsPersistentCheck(t, fresh, fb, fc)
		// The public native binary COPY API has no pq streaming counterpart.
		// SQL rows on both modes above independently specify its logical data.
		raw, err := p.Native.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		regtsExec(t, p, b, c, "CREATE TABLE reg_ts_binary(d regdictionary,c regconfig)", 0)
		var binary bytes.Buffer
		if _, err := stuntgres.CopyTo(p.Ctx, raw, "COPY reg_ts_data(d,c) TO STDOUT (FORMAT binary)", &binary); err != nil {
			t.Fatal(err)
		}
		regtsBinaryPayload(t, binary.Bytes(), uint32(regtsDictionaryOID(t, p, c)))
		if _, err := stuntgres.CopyFrom(p.Ctx, raw, "COPY reg_ts_binary(d,c) FROM STDIN (FORMAT binary)", bytes.NewReader(binary.Bytes())); err != nil {
			t.Fatal(err)
		}
		regtsSelect(t, p, b, c, "SELECT count(*)::bigint AS n FROM reg_ts_binary", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}})
		regtsSelect(t, p, b, c, `SELECT count(*)::bigint AS n FROM reg_ts_binary WHERE d='"Reg.TS".unaccent'::regdictionary`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
		regtsSelect(t, p, b, c, "SELECT d,c FROM reg_ts_binary ORDER BY d::oid", []string{"d", "c"}, []string{"REGDICTIONARY", "REGCONFIG"}, [][]any{{"-", "4294967295"}, {`"Reg.TS".unaccent`, "-"}, {"4294967295", "8"}, {nil, nil}})
		regtsExec(t, p, b, c, "CREATE TABLE reg_ts_sentinel(v integer)", 0)
		regtsExec(t, p, b, c, "INSERT INTO reg_ts_sentinel VALUES(42)", 1)
		for _, bad := range []string{string(emitted) + "INSERT INTO reg_ts_data VALUES(9,'reg_ts_missing','-');", "CREATE TABLE reg_ts_broken(d regdictionary); INSERT INTO reg_ts_broken VALUES('08');"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
				t.Fatal("invalid Restore succeeded")
			}
			regtsPersistentCheck(t, p, b, c)
			regtsSelect(t, p, b, c, "SELECT v FROM reg_ts_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore: %v", err)
		}
		regtsPersistentCheck(t, p, b, c)
		regtsSelect(t, p, b, c, "SELECT v FROM reg_ts_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}})
	})
}
func TestReferenceRegTSFeatureSmoke(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{{"values", TestReferenceRegTSValuesMetadata}, {"diagnostics", TestReferenceRegTSDiagnostics}, {"namespace", TestReferenceRegTSNamespace}, {"authorization", TestReferenceRegTSNamespaceAuthorization}, {"prepared", TestReferenceRegTSPreparedLifetime}, {"retained", TestReferenceRegTSRetainedRows}, {"persistence", TestReferenceRegTSPersistence}, {"mixed-persistence", TestReferenceRegTSMixedRoutinePersistence}, {"partition-persistence", TestReferenceRegTSPartitionPersistence}, {"partition-diagnostics", TestReferenceRegTSPartitionDiagnostics}} {
		t.Run(tc.name, tc.run)
	}
}
