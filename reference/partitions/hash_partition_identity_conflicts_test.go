package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Each twin owns a genuinely independent allocator. Public preconditioning is
// identical, so the successful preview measures the failed candidate's identity.
func referenceHashTwin(t *testing.T, p *fixture.Pair, padding int) (*sql.DB, *sql.DB, *fixture.Recorder) {
	t.Helper()
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("hash-conflict-twin")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() { db.Close() })
	pool, record := fixture.InsertSelectRecordedPool(t, connector)
	fixture.RunStep(t, p.Ctx, pool, record, fixture.Exec("CREATE TABLE hp_sentinel(k INT,payload TEXT)", 0))
	fixture.RunStep(t, p.Ctx, pool, record, fixture.Exec("INSERT INTO hp_sentinel VALUES(91,'preserved')", 1))
	for i := range padding {
		fixture.RunStep(t, p.Ctx, pool, record, fixture.Exec(fmt.Sprintf("CREATE TYPE hp_padding_%d AS ENUM('x','y','z')", i), 0))
	}
	return db, pool, record
}

func referenceHashMember(t *testing.T, p *fixture.Pair, db *sql.DB) uint32 {
	t.Helper()
	var oid int64
	if err := db.QueryRowContext(p.Ctx, "SELECT oid::BIGINT FROM pg_enum WHERE enumtypid='hp_conflict_mood'::REGTYPE::OID AND enumlabel='calm'").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	return uint32(oid)
}

func referenceHashConflictPreserved(t *testing.T, p *fixture.Pair, db, pool *sql.DB, record *fixture.Recorder, payload string) {
	t.Helper()
	var before bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, db, &before); err != nil {
		t.Fatal(err)
	}
	q := "INSERT INTO hp_sentinel VALUES($1,$2)"
	stmt, err := pool.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err := pool.QueryContext(p.Ctx, "SELECT k,payload FROM hp_sentinel")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, db, strings.NewReader(payload)); err == nil {
		t.Fatal("identity-conflicting Restore succeeded")
	} else {
		fixture.State(t, err, "23514")
	}
	var after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, db, &after); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("failed Restore changed persistent target")
	}
	fixture.TableDDLColumnTypes(t, false, metadata, []string{"k", "payload"}, []string{"INT4", "TEXT"}, []bool{true, true})
	if !rows.Next() {
		t.Fatal("retained row absent", rows.Err())
	}
	var k int64
	var payloadValue string
	if err := rows.Scan(&k, &payloadValue); err != nil || k != 91 || payloadValue != "preserved" {
		t.Fatal(k, payloadValue, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained rows changed", rows.Err())
	}
	held := fixture.HeldStatement{Executor: pool, Statement: stmt, SQL: q}
	fixture.RunStep(t, p.Ctx, held, record, fixture.Exec(q, 1, 92, "healthy"))
	fixture.RunStep(t, p.Ctx, pool, record, fixture.Select("SELECT k,payload FROM hp_sentinel ORDER BY k", []string{"k", "payload"}, []string{"INT4", "TEXT"}, [][]any{{int64(91), "preserved"}, {int64(92), "healthy"}}))
}

func referenceHashIdentityConflicts(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	run := referenceHashRun(t, p, b, prepared)
	run(fixture.Exec("CREATE TYPE hp_conflict_mood AS ENUM('calm')", 0))
	run(fixture.Exec("CREATE TABLE hp_sparse(k hp_conflict_mood,id INT) PARTITION BY HASH(k)", 0))
	run(fixture.Exec("CREATE TABLE hp_sparse_leaf PARTITION OF hp_sparse FOR VALUES WITH(MODULUS 1,REMAINDER 0)", 0))
	run(fixture.Exec("INSERT INTO hp_sparse VALUES('calm',0)", 1))
	run(fixture.Exec("CREATE TABLE hp_local(k hp_conflict_mood,id INT) PARTITION BY HASH(k)", 0))
	for r := range 8 {
		run(fixture.Exec(fmt.Sprintf("CREATE TABLE hp_local_%d PARTITION OF hp_local(CONSTRAINT hp_guard_%d CHECK(id>=0)) FOR VALUES WITH(MODULUS 8,REMAINDER %d)", r, r, r), 0))
	}
	run(fixture.Exec("INSERT INTO hp_local VALUES('calm',0)", 1))
	run(fixture.Select("SELECT id FROM hp_local", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}}))
	if b.PG {
		return
	}
	sourceOID := referenceHashMember(t, p, b.DB)
	sourceHash := referenceHashOID(sourceOID) + 0x49a0f4dd15e5a8e3
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for padding := range 16 {
		preview, _, _ := referenceHashTwin(t, p, padding)
		failed, pool, record := referenceHashTwin(t, p, padding)
		var a, z bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, preview, &a); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Dump(p.Ctx, failed, &z); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a.Bytes(), z.Bytes()) {
			t.Fatal("twin public prestates differ")
		}
		if err := stuntgres.Restore(p.Ctx, preview, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		destinationOID := referenceHashMember(t, p, preview)
		destinationHash := referenceHashOID(destinationOID) + 0x49a0f4dd15e5a8e3
		if destinationOID == sourceOID || destinationHash%8 == sourceHash%8 {
			continue
		}
		// Exact-topology preview proves the CHECK-rejecting destination differs from
		// the source; changing this integer expression changes no identity allocation.
		leaf := destinationHash % 8
		marker := fmt.Sprintf(`CONSTRAINT "hp_guard_%d" CHECK (`, leaf)
		start := strings.Index(dump.String(), marker)
		if start < 0 {
			t.Fatal("named CHECK absent from actual Dump", marker)
		}
		expressionStart := start + len(marker)
		end := expressionStart
		depth := 1
		text := dump.String()
		for ; end < len(text) && depth > 0; end++ {
			if text[end] == '(' {
				depth++
			} else if text[end] == ')' {
				depth--
			}
		}
		if depth != 0 {
			t.Fatal("unbalanced emitted CHECK")
		}
		expression := text[expressionStart : end-1]
		if strings.Count(expression, " >= ") != 1 {
			t.Fatal("unexpected emitted CHECK", expression)
		}
		conflict := text[:expressionStart] + strings.Replace(expression, " >= ", " <> ", 1) + text[end-1:]
		referenceHashConflictPreserved(t, p, failed, pool, record, conflict)
		// A distinct twin pair measures the same declarations for sparse coverage.
		// Only the hash-bound literal changes; total object/name/dependency order stays fixed.
		sparseTarget, sparsePool, sparseRecord := referenceHashTwin(t, p, padding)
		modulus := uint64(0)
		for _, prime := range []uint64{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47} {
			if sourceHash%prime != destinationHash%prime {
				modulus = prime
				break
			}
		}
		if modulus == 0 {
			t.Fatal("distinct measured hashes have no separating prime")
		}
		old := "FOR VALUES WITH (MODULUS 1, REMAINDER 0)"
		if strings.Count(text, old) != 1 {
			t.Fatal("unique sparse bound absent from Dump")
		}
		sparse := strings.Replace(text, old, fmt.Sprintf("FOR VALUES WITH (MODULUS %d, REMAINDER %d)", modulus, sourceHash%modulus), 1)
		referenceHashConflictPreserved(t, p, sparseTarget, sparsePool, sparseRecord, sparse)
		return
	}
	t.Fatal("no observed changed-residue identity fixture in sixteen deterministic public padding states")
}
