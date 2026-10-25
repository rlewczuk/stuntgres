package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/bits"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceHashPartitionControls(t *testing.T) {
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := referenceHashRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE hp_control(k INT,payload TEXT) PARTITION BY HASH(k)", 0))
		referenceHashChildren(run, "hp_control", 8)
		run(fixture.Exec("INSERT INTO hp_control VALUES(NULL,'null'),(1,'one'),(17,'seventeen'),(42,'answer')", 4))
		check := func(db fixture.Executor, record *fixture.Recorder) {
			apply := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, db, record, s) }
			apply(fixture.Select("SELECT k,payload FROM hp_control ORDER BY k NULLS FIRST", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{nil, "null"}, {int64(1), "one"}, {int64(17), "seventeen"}, {int64(42), "answer"}}))
			apply(referenceRangeRows("SELECT k FROM hp_control_4", 17))
			apply(referenceRangeRows("SELECT k FROM hp_control_2", 42))
			apply(referenceRangeRows("SELECT k FROM ONLY hp_control"))
		}
		check(b.DB, b.Recorder)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		q := "INSERT INTO hp_control VALUES($1,$2)"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		retained, err := b.DB.QueryContext(p.Ctx, "SELECT k FROM hp_control_4")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		for range 2 {
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(b.DB, b.Recorder)
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("hash-control-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		pool, record := fixture.InsertSelectRecordedPool(t, connector)
		for range 2 {
			if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(pool, record)
		}
		var settled, redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, destination, &settled); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(settled.Bytes())); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Dump(p.Ctx, destination, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(settled.Bytes(), redump.Bytes()) {
			t.Fatal("integer replay not deterministic")
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		reader := &referenceHashCancelReader{reader: bytes.NewReader(dump.Bytes()), cancel: cancel}
		restoreErr := stuntgres.Restore(canceled, p.Native, reader)
		cancel()
		if !reader.read || !errors.Is(restoreErr, context.Canceled) {
			t.Fatal("Restore input cancellation", reader.read, restoreErr)
		}
		check(b.DB, b.Recorder)
		for _, suffix := range []string{"CREATE TABLE hp_broken(k INT,k INT);", "INSERT INTO hp_control_0 VALUES(17,'wrong');"} {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+suffix)); err == nil {
				t.Fatal("invalid restore succeeded")
			}
			check(b.DB, b.Recorder)
		}
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 17, "future"))
		run(fixture.Select("SELECT payload FROM hp_control_4 ORDER BY payload", []string{"payload"}, []string{"TEXT"}, [][]any{{"future"}, {"seventeen"}}))
		if !retained.Next() {
			t.Fatal("retained cursor lost row", retained.Err())
		}
		var k int64
		if err := retained.Scan(&k); err != nil || k != 17 {
			t.Fatal(k, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained cursor changed", retained.Err())
		}
	})
}

// Independent REL_18_0 uint32 support2 arithmetic; no native hash provider is imported.
// Its raw scalar outputs are pinned separately before using catalog-owned identities.
func referenceHashOID(k uint32) uint64 {
	a := uint32(0x9e3779b9 + 4 + 3923095)
	b, c := a, a
	a += 0x7a5b2236
	b += 0x7996dcfd
	a -= c
	a ^= bits.RotateLeft32(c, 4)
	c += b
	b -= a
	b ^= bits.RotateLeft32(a, 6)
	a += c
	c -= b
	c ^= bits.RotateLeft32(b, 8)
	b += a
	a -= c
	a ^= bits.RotateLeft32(c, 16)
	c += b
	b -= a
	b ^= bits.RotateLeft32(a, 19)
	a += c
	c -= b
	c ^= bits.RotateLeft32(b, 4)
	b += a
	a += k
	c ^= b
	c -= bits.RotateLeft32(b, 14)
	a ^= c
	a -= bits.RotateLeft32(c, 11)
	b ^= a
	b -= bits.RotateLeft32(a, 25)
	c ^= b
	c -= bits.RotateLeft32(b, 16)
	a ^= c
	a -= bits.RotateLeft32(c, 4)
	b ^= a
	b -= bits.RotateLeft32(a, 14)
	c ^= b
	c -= bits.RotateLeft32(b, 24)
	return uint64(b)<<32 | uint64(c)
}

func TestReferenceHashPartitionIdentityReplay(t *testing.T) {
	for _, v := range []struct {
		k    uint32
		want int64
	}{{0, -4403592609991167795}, {1, 5968994663651403477}, {2, -6784076462669891433}, {17, 1629907112932383753}, {42, 7363975540656877951}} {
		if got := int64(referenceHashOID(v.k)); got != v.want {
			t.Fatalf("independent OID arithmetic %d: %d != %d", v.k, got, v.want)
		}
	}
	referenceHashScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := referenceHashRun(t, p, b, prepared)
		var retainedTemporary *sql.Conn
		if !b.PG {
			var err error
			retainedTemporary, err = b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer retainedTemporary.Close()
			fixture.RunStep(t, p.Ctx, retainedTemporary, b.Recorder, fixture.Exec("CREATE TEMP TABLE hp_retained(k INT)", 0))
			fixture.RunStep(t, p.Ctx, retainedTemporary, b.Recorder, fixture.Exec("INSERT INTO hp_retained VALUES(71)", 1))
		}
		run(fixture.Exec("CREATE TYPE hp_mood AS ENUM('calm','busy','quiet')", 0))
		run(fixture.Exec("CREATE DOMAIN hp_mood_domain AS hp_mood", 0))
		run(fixture.Exec("CREATE TABLE hp_reference(k INT)", 0))
		for _, c := range []struct{ name, typ, values string }{{"hp_enum", "hp_mood", "'calm','busy','quiet'"}, {"hp_domain", "hp_mood_domain", "'calm','busy','quiet'"}, {"hp_alias", "REGCLASS", "'hp_reference','hp_enum','hp_domain'"}} {
			run(fixture.Exec("CREATE TABLE "+c.name+"(k "+c.typ+",id INT) PARTITION BY HASH(k)", 0))
			referenceHashChildren(run, c.name, 8)
			for i, value := range strings.Split(c.values, ",") {
				run(fixture.Exec(fmt.Sprintf("INSERT INTO %s VALUES(%s,%d)", c.name, value, i), 1))
			}
		}
		identities := func(db *sql.DB) map[string]uint32 {
			result := map[string]uint32{}
			rows, err := db.QueryContext(p.Ctx, "SELECT enumlabel,oid::BIGINT FROM pg_enum WHERE enumtypid='hp_mood'::REGTYPE::OID ORDER BY enumlabel")
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var label string
				var oid int64
				if err := rows.Scan(&label, &oid); err != nil {
					t.Fatal(err)
				}
				result[label] = uint32(oid)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			for _, name := range []string{"hp_reference", "hp_enum", "hp_domain"} {
				var oid int64
				if err := db.QueryRowContext(p.Ctx, "SELECT '"+name+"'::REGCLASS::OID::BIGINT").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				result[name] = uint32(oid)
			}
			return result
		}
		check := func(db *sql.DB, record *fixture.Recorder) {
			ids := identities(db)
			for _, c := range []struct {
				name   string
				labels []string
			}{{"hp_enum", []string{"calm", "busy", "quiet"}}, {"hp_domain", []string{"calm", "busy", "quiet"}}, {"hp_alias", []string{"hp_reference", "hp_enum", "hp_domain"}}} {
				for r := range 8 {
					var want [][]any
					for i, label := range c.labels {
						if (referenceHashOID(ids[label])+0x49a0f4dd15e5a8e3)%8 == uint64(r) {
							want = append(want, []any{int64(i)})
						}
					}
					s := fixture.Select(fmt.Sprintf("SELECT id FROM %s_%d ORDER BY id", c.name, r), []string{"id"}, []string{"INT4"}, want)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, db, record, s)
				}
			}
		}
		check(b.DB, b.Recorder)
		if b.PG {
			return
		}
		before := identities(b.DB)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		// Allocating real members before replacement forces an advanced remint frontier.
		run(fixture.Exec("CREATE TYPE hp_identity_padding AS ENUM('a','b','c','d','e','f','g','h','i')", 0))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		after := identities(b.DB)
		if before["calm"] == after["calm"] {
			t.Fatal("enum member identity did not change")
		}
		if before["hp_reference"] == after["hp_reference"] {
			t.Fatal("regclass identity did not change")
		}
		check(b.DB, b.Recorder)
		fixture.RunStep(t, p.Ctx, retainedTemporary, b.Recorder, referenceRangeRows("SELECT k FROM hp_retained", 71))
		for _, name := range []string{"hp_enum", "hp_domain"} {
			run(fixture.Exec("INSERT INTO "+name+" VALUES('calm',3)", 1))
			r := (referenceHashOID(after["calm"]) + 0x49a0f4dd15e5a8e3) % 8
			run(fixture.Select(fmt.Sprintf("SELECT id FROM %s_%d WHERE id=3", name, r), []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("hash-identity-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		pool, record := fixture.InsertSelectRecordedPool(t, connector)
		if _, err := destination.ExecContext(p.Ctx, "CREATE TYPE hp_dest_padding AS ENUM('a','b','c','d','e','f','g')"); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(pool, record)
		}
	})
	t.Run("atomic_identity_conflicts", func(t *testing.T) { referenceHashScenario(t, referenceHashIdentityConflicts) })
}

type referenceHashCancelReader struct {
	reader *bytes.Reader
	cancel context.CancelFunc
	read   bool
}

func (r *referenceHashCancelReader) Read(p []byte) (int, error) {
	if len(p) > 32 {
		p = p[:32]
	}
	n, err := r.reader.Read(p)
	r.read = true
	r.cancel()
	return n, err
}
