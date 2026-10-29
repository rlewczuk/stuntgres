package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func bfConsumerSetup(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) []fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, c, r, s) }
	run(fixture.Exec("CREATE USER pgcrypto_owner SUPERUSER", 0))
	run(fixture.Exec("CREATE SCHEMA pgcrypto_part AUTHORIZATION pgcrypto_owner", 0))
	run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_owner", 0))
	run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
	run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	pgcryptoInventory(t, p, c, "pgcrypto_owner", "pgcrypto_owner")
	// Stored type columns prevent all consumers becoming literal-folding-only
	// witnesses. Both default and generated recipes must execute on fresh rows.
	run(fixture.Exec("CREATE TABLE pgcrypto_bf_stored(id integer,p bytea DEFAULT 'foo'::bytea,typ text DEFAULT 'bf',c bytea DEFAULT pgcrypto_part.encrypt('foo','0123456789','bf'),g bytea GENERATED ALWAYS AS(pgcrypto_part.encrypt_iv(p,'0123456','abcd',typ)) STORED,cfbt text DEFAULT 'bf-cfb',CONSTRAINT pgcrypto_bf_valid CHECK(pgcrypto_part.encrypt_iv(p,'0123456','abcd',typ)='\\x95c7e89322525d59'::bytea))", 0))
	run(fixture.Exec("CREATE INDEX pgcrypto_bf_idx ON pgcrypto_bf_stored ((pgcrypto_part.encrypt(p,'0123456789','bf-ecb')))", 0))
	run(fixture.Exec("CREATE VIEW pgcrypto_bf_view AS SELECT id,pgcrypto_part.decrypt(c,'0123456789',typ) AS p,pgcrypto_part.decrypt_iv(g,'0123456','abcd',typ) AS ivp FROM pgcrypto_bf_stored", 0))
	run(fixture.Exec("CREATE VIEW pgcrypto_bf_nested AS SELECT id,(SELECT pgcrypto_part.decrypt_iv('\\x"+bfCFBCipher+"'::bytea,'\\x"+bfCFBKey+"'::bytea,'\\x"+bfCFBIV+"'::bytea,cfbt)) AS p FROM pgcrypto_bf_stored", 0))
	run(fixture.Exec("INSERT INTO pgcrypto_bf_stored(id,p) VALUES(1,'foo'),(2,NULL)", 2))
	steps := []fixture.Step{
		fixture.Select("SELECT id,c,g FROM pgcrypto_bf_stored ORDER BY id", []string{"id", "c", "g"}, []string{"INT4", "BYTEA", "BYTEA"}, [][]any{{int64(1), pgcryptoBytes(t, "42f58af3b2c03f46"), pgcryptoBytes(t, "95c7e89322525d59")}, {int64(2), pgcryptoBytes(t, "42f58af3b2c03f46"), nil}}),
		fixture.Select("SELECT id,p,ivp FROM pgcrypto_bf_view ORDER BY id", []string{"id", "p", "ivp"}, []string{"INT4", "BYTEA", "BYTEA"}, [][]any{{int64(1), "foo", "foo"}, {int64(2), "foo", nil}}),
		fixture.Select("SELECT id,p FROM pgcrypto_bf_nested ORDER BY id", []string{"id", "p"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), pgcryptoBytes(t, bfCFBPlain)}, {int64(2), pgcryptoBytes(t, bfCFBPlain)}}),
	}
	for _, s := range steps {
		run(s)
		run(fixture.Prepared(s))
	}
	bfDependencies(t, p, c, r)
	return steps
}

func TestReferencePgcryptoBFConsumersAndControls(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		steps := bfConsumerSetup(t, p, c, r)
		q := "INSERT INTO pgcrypto_bf_stored(id,p) VALUES(4,'foo'),(5,'wrong') RETURNING g"
		failure := fixture.ImplicitError(q, fixture.Diagnostic{
			Code: "23514", Message: `new row for relation "pgcrypto_bf_stored" violates check constraint "pgcrypto_bf_valid"`,
			Detail: `Failing row contains (5, \x77726f6e67, bf, \x42f58af3b2c03f46, \x0e7f7d8c7abc9267, bf-cfb).`,
			Schema: "public", Table: "pgcrypto_bf_stored", Constraint: "pgcrypto_bf_valid",
		})
		failure.SelectRows, failure.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, c, r, failure)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(failure))
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: stmt, SQL: q}, r, failure)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pgcrypto_bf_stored", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
		if !pg {
			bfRetainedControls(t, p, c, r, steps)
		}
		// Native relation AND routine ACLs remain explicitly marked after
		// revoke. Finish every Dump/replay witness before exercising reader
		// authority; no native control serialization follows this phase.
		bfConsumerGrant(t, p, c, r)
	})
}

func bfConsumerGrant(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, c, r, s) }
	// Keep explicit SELECT ACLs on disposable forwarding views. The raw call
	// stays outside its source view, so function EXECUTE remains reader-owned;
	// the other view still traverses the actual stored decrypt consumers.
	run(fixture.Exec("CREATE VIEW pgcrypto_bf_reader_source AS SELECT id,p,typ FROM pgcrypto_bf_stored", 0))
	run(fixture.Exec("CREATE VIEW pgcrypto_bf_reader_result AS SELECT id,p,ivp FROM pgcrypto_bf_view", 0))
	run(fixture.Exec("CREATE USER pgcrypto_bf_reader", 0))
	defer func() {
		// Registered before the held statement's Close defer, so no role
		// handle survives into view/ACL/role teardown.
		for _, q := range []string{
			"RESET SESSION AUTHORIZATION",
			"DROP VIEW pgcrypto_bf_reader_result",
			"DROP VIEW pgcrypto_bf_reader_source",
			"GRANT EXECUTE ON FUNCTION pgcrypto_part.encrypt(bytea,bytea,text) TO PUBLIC",
			"REVOKE EXECUTE ON FUNCTION pgcrypto_part.encrypt(bytea,bytea,text) FROM pgcrypto_bf_reader",
			"REVOKE USAGE ON SCHEMA public,pgcrypto_part FROM pgcrypto_bf_reader",
			"DROP ROLE pgcrypto_bf_reader",
		} {
			if _, err := c.ExecContext(p.Ctx, q); err != nil {
				t.Error(q, err)
			}
		}
	}()
	run(fixture.Exec("GRANT USAGE ON SCHEMA public,pgcrypto_part TO pgcrypto_bf_reader", 0))
	run(fixture.Exec("GRANT SELECT ON pgcrypto_bf_reader_source,pgcrypto_bf_reader_result TO pgcrypto_bf_reader", 0))
	run(fixture.Exec("GRANT EXECUTE ON FUNCTION pgcrypto_part.encrypt(bytea,bytea,text) TO pgcrypto_bf_reader", 0))
	q := "SELECT pgcrypto_part.encrypt(p,'0123456789',typ) FROM pgcrypto_bf_reader_source WHERE id=$1"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Statement: stmt, SQL: q}
	good := fixture.Select(q, []string{"encrypt"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, "42f58af3b2c03f46")}}, 1)
	fixture.RoleGrantActor(run, "pgcrypto_bf_reader", func() {
		run(good)
		run(fixture.Prepared(good))
		fixture.RunStep(t, p.Ctx, held, r, good)
		s := fixture.Select("SELECT p,ivp FROM pgcrypto_bf_reader_result WHERE id=1", []string{"p", "ivp"}, []string{"BYTEA", "BYTEA"}, [][]any{{"foo", "foo"}})
		run(s)
		run(fixture.Prepared(s))
	})
	// Exactly one existing signature is revoked, not a second algorithm ACL.
	run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pgcrypto_part.encrypt(bytea,bytea,text) FROM PUBLIC,pgcrypto_bf_reader", 0))
	fixture.RoleGrantActor(run, "pgcrypto_bf_reader", func() {
		for _, id := range []int{1, 2} {
			d := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for function encrypt"}, id)
			run(d)
			run(fixture.Prepared(d))
			fixture.RunStep(t, p.Ctx, held, r, d)
		}
		// Row-dependent invalid mode must not leak the kernel diagnostic first.
		bad := "SELECT pgcrypto_part.encrypt(p,'k',typ||'/pad:bogus') FROM pgcrypto_bf_reader_source WHERE id=1"
		aesErrorRoutes(t, p, c, r, bad, fixture.Diagnostic{Code: "42501", Message: "permission denied for function encrypt"})
	})
	run(fixture.Exec("GRANT EXECUTE ON FUNCTION pgcrypto_part.encrypt(bytea,bytea,text) TO PUBLIC", 0))
	fixture.RoleGrantActor(run, "pgcrypto_bf_reader", func() { fixture.RunStep(t, p.Ctx, held, r, good) })
}

func bfRetainedControls(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, steps []fixture.Step) {
	t.Helper()
	q := "SELECT pgcrypto_part.encrypt_iv($1::bytea,$2::bytea,$3::bytea,$4)"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Statement: stmt, SQL: q}
	args := []any{[]byte("foo"), []byte("0123456"), []byte("abcd"), "bf"}
	good := fixture.Select(q, []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, "95c7e89322525d59")}}, args...)
	fixture.RunStep(t, p.Ctx, held, r, good)
	old, err := c.QueryContext(p.Ctx, "SELECT c FROM pgcrypto_bf_stored WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	var scanCopy []byte
	if err := c.QueryRowContext(p.Ctx, "SELECT c FROM pgcrypto_bf_stored WHERE id=1").Scan(&scanCopy); err != nil || !bytes.Equal(scanCopy, aesBytes(t, "42f58af3b2c03f46")) {
		t.Fatal("BF scan ownership", scanCopy, err)
	}
	scanCopy[0] ^= 255
	fixture.RunStep(t, p.Ctx, c, r, steps[0])
	script := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	absent := fixture.Diagnostic{Code: "3F000", Message: `schema "pgcrypto_part" does not exist`, Position: "8"}
	fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError("SELECT pgcrypto_part.encrypt('foo','key','bf')", absent))
	fixture.RunStep(t, p.Ctx, held, r, fixture.ImplicitError(q, absent, args...))
	if !old.Next() {
		t.Fatal("retained BF row missing", old.Err())
	}
	var exported []byte
	if err := old.Scan(&exported); err != nil || !bytes.Equal(exported, aesBytes(t, "42f58af3b2c03f46")) {
		t.Fatal(exported, err)
	}
	if old.Next() || old.Err() != nil {
		t.Fatal("retained BF tail", old.Err())
	}
	old.Close()
	if len(metadata) != 1 || metadata[0].Name() != "c" || metadata[0].DatabaseTypeName() != "BYTEA" {
		t.Fatal("retained BF metadata", metadata)
	}
	exported[0] ^= 255
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	pgcryptoInventory(t, p, c, "pgcrypto_owner", "pgcrypto_owner")
	bfDependencies(t, p, c, r)
	for _, s := range steps {
		fixture.RunStep(t, p.Ctx, c, r, s)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
	}
	fixture.RunStep(t, p.Ctx, held, r, good)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_bf_stored(id) VALUES(3)", 1))
	pgcryptoResult(t, p, c, r, "g", "SELECT g FROM pgcrypto_bf_stored WHERE id=3", "95c7e89322525d59")
	pgcryptoResult(t, p, c, r, "c", "SELECT c FROM pgcrypto_bf_stored WHERE id=3", "42f58af3b2c03f46")
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pgcrypto_bf_stored", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
	before := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(pgcryptoReplayFailure(t, script, "provider"))); err == nil {
		t.Fatal("corrupted BF provider replay accepted")
	}
	if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
		t.Fatal("failed BF replay published changes")
	}
	fixture.RunStep(t, p.Ctx, held, r, good)
	pgcryptoResult(t, p, c, r, "g", "SELECT g FROM pgcrypto_bf_stored WHERE id=3", "95c7e89322525d59")
}

func bfDependencies(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	ids := pgcryptoProcedureIDs(t, p, c)
	for _, consumer := range []struct {
		query      string
		class      int64
		signatures []string
	}{
		{"SELECT oid FROM pg_attrdef WHERE adrelid='pgcrypto_bf_stored'::regclass::oid AND adnum=4", 2604, []string{"encrypt(bytea,bytea,text)"}},
		{"SELECT oid FROM pg_attrdef WHERE adrelid='pgcrypto_bf_stored'::regclass::oid AND adnum=5", 2604, []string{"encrypt_iv(bytea,bytea,bytea,text)"}},
		{"SELECT oid FROM pg_constraint WHERE conrelid='pgcrypto_bf_stored'::regclass::oid AND conname='pgcrypto_bf_valid'", 2606, []string{"encrypt_iv(bytea,bytea,bytea,text)"}},
		{"SELECT oid FROM pg_class WHERE oid='pgcrypto_bf_idx'::regclass::oid", 1259, []string{"encrypt(bytea,bytea,text)"}},
		{"SELECT oid FROM pg_rewrite WHERE ev_class='pgcrypto_bf_view'::regclass::oid AND rulename='_RETURN'", 2618, []string{"decrypt(bytea,bytea,text)", "decrypt_iv(bytea,bytea,bytea,text)"}},
		{"SELECT oid FROM pg_rewrite WHERE ev_class='pgcrypto_bf_nested'::regclass::oid AND rulename='_RETURN'", 2618, []string{"decrypt_iv(bytea,bytea,bytea,text)"}},
	} {
		var id int64
		if err := c.QueryRowContext(p.Ctx, consumer.query).Scan(&id); err != nil || id <= 0 {
			t.Fatal("BF consumer identity", consumer.query, id, err)
		}
		want := [][]any{}
		for _, signature := range consumer.signatures {
			proc := ids["pgcrypto_part."+signature]
			if proc <= 0 {
				t.Fatal("missing actual BF procedure", signature)
			}
			want = append(want, fixture.PgDependEdge(consumer.class, id, 0, 1255, proc, 0, "n"))
		}
		s := fixture.Select("SELECT classid::bigint AS classid,objid::bigint AS objid,objsubid,refclassid::bigint AS refclassid,refobjid::bigint AS refobjid,refobjsubid,deptype FROM pg_depend WHERE classid=$1 AND objid=$2 AND refclassid=1255 AND deptype='n'", []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, want, consumer.class, id)
		s.Bag = true
		fixture.RunStep(t, p.Ctx, c, r, s)
	}
}
