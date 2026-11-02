package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoPolicyNativeControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"policy_control_reader"})
	pgcryptoPolicyCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE ROLE policy_control_reader", 0))
		pgcryptoPolicyRoleGrantCleanup(t, c, "policy_control_reader", "")
		for _, q := range []string{
			"GRANT USAGE ON SCHEMA pgcrypto_part TO policy_control_reader",
			"GRANT SET ON PARAMETER pgcrypto.builtin_crypto_enabled TO policy_control_reader",
			"CREATE TABLE policy_control(id integer,p text DEFAULT 'foox',s text DEFAULT pgcrypto_part.gen_salt('des'),h text GENERATED ALWAYS AS(pgcrypto_part.crypt(p,s)) STORED,d text DEFAULT pgcrypto_part.crypt('foox','NB'),sha text DEFAULT pgcrypto_part.crypt('Hello world!','$5$saltstring'),CONSTRAINT policy_control_valid CHECK(p IS NULL OR pgcrypto_part.crypt(p,'NB')='NB53EGGqrrb5E'))",
			"CREATE INDEX policy_control_idx ON policy_control((pgcrypto_part.crypt(p,'NB')))",
			"CREATE VIEW policy_control_dynamic AS SELECT id,pgcrypto_part.crypt(p,s) AS value FROM policy_control",
			"CREATE VIEW policy_control_nested AS SELECT id,value FROM policy_control_dynamic",
			"CREATE VIEW policy_control_folded AS SELECT pgcrypto_part.crypt('foox','NB') AS value",
		} { run(fixture.Exec(q, 0)) }
		run(fixture.Exec("INSERT INTO policy_control(id) VALUES(1),(2)", 2))
		stored := pgcryptoPolicyStored(t, p, c)
		run(stored)
		run(fixture.Prepared(stored))
		run(fixture.Select("SELECT id,h=pgcrypto_part.crypt(p,s) AS correct,pgcrypto_part.crypt('wrong',h)=h AS wrong,d,sha FROM policy_control ORDER BY id", []string{"id", "correct", "wrong", "d", "sha"}, []string{"INT4", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{{int64(1), true, false, "NB53EGGqrrb5E", pgcryptoSHA256Hello}, {int64(2), true, false, "NB53EGGqrrb5E", pgcryptoSHA256Hello}}))
		owner := "postgres"
		if pg { owner = "stuntgres" }
		ids := pgcryptoRandomInventory(t, p, c, pg, owner)
		pgcryptoPolicyDependencies(t, p, c, r, ids)
		run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
		pgcryptoPolicyStoredOff(t, p, c, r, stored)
		if !reflect.DeepEqual(ids, pgcryptoRandomInventory(t, p, c, pg, owner)) {
			t.Fatal("policy mutation replaced installed member identities")
		}
		pgcryptoPolicyControlACL(t, p, c, r)
		pgcryptoPolicyNotices(t, notices)
		if pg { return }
		q := "SELECT pgcrypto_part.crypt($1,$2) AS value"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyFailure(q, pgcryptoPolicyOff, "foox", "NB"))
		old, err := c.QueryContext(p.Ctx, stored.Query)
		if err != nil { t.Fatal(err) }
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil { t.Fatal(err) }
		script := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		pgcryptoPolicyShow(t, p, c, r, "off")
		// The physical descriptor/prefix remains active with no installed objects.
		run(pgcryptoPolicyInvalid("SET "+pgcryptoPolicyName+"='bogus'", "bogus"))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil { t.Fatal(err) }
		pgcryptoPolicyConsumeStored(t, old, metadata, stored)
		post := func(conn *sql.Conn, record *fixture.Recorder) {
			pgcryptoPolicyStoredOff(t, p, conn, record, stored)
			replayedIDs := pgcryptoRandomInventory(t, p, conn, false, owner)
			pgcryptoPolicyDependencies(t, p, conn, record, replayedIDs)
			pgcryptoPolicyControlACL(t, p, conn, record)
		}
		post(c, r)
		fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyFailure(q, pgcryptoPolicyOff, "foox", "NB"))
		retained, err := c.QueryContext(p.Ctx, stored.Query)
		if err != nil { t.Fatal(err) }
		defer retained.Close()
		retainedMetadata, err := retained.ColumnTypes()
		if err != nil { t.Fatal(err) }
		beforeIDs := pgcryptoRandomInventory(t, p, c, false, owner)
		pgcryptoRandomRejectReplay(t, p, p.Native, script, func() {
			post(c, r)
			if !reflect.DeepEqual(beforeIDs, pgcryptoRandomInventory(t, p, c, false, owner)) { t.Fatal("failed Restore changed member identities") }
		})
		pgcryptoPolicyConsumeStored(t, retained, retainedMetadata, stored)
		candidate := pgcryptoProcedureIDs(t, p, c)
		other, target, record := pgcryptoControlDestination(t, p, "pgcrypto_policy_destination")
		fixture.RunStep(t, p.Ctx, target, record, fixture.Exec("CREATE ROLE policy_control_reader", 0))
		pgcryptoPolicyRoleGrantCleanup(t, target, "policy_control_reader", "")
		var maximum int64
		for _, id := range candidate {
			if id > maximum { maximum = id }
		}
		// Reuse the relocation family's actual TEMP-catalog collision technique;
		// no source allocator sequence is a golden or a presumed collision.
		collisions := make(map[int64]string)
		for i := range 128 {
			name := fmt.Sprintf("policy_collision_%d", i)
			fixture.RunStep(t, p.Ctx, target, record, fixture.Exec("CREATE TEMP SEQUENCE "+name+" START 73", 0))
			var id int64
			if err := target.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1", name).Scan(&id); err != nil || id <= 0 { t.Fatal("collision sequence", id, err) }
			collisions[id] = name
			if id >= maximum { break }
		}
		for signature, id := range candidate {
			if collisions[id] == "" { t.Fatal("missing actual destination collision", signature, id) }
		}
		for _, setup := range []string{
			"CREATE SCHEMA pgcrypto_part", "CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part",
			"SET pgcrypto.builtin_crypto_enabled=off",
		} { fixture.RunStep(t, p.Ctx, target, record, fixture.Exec(setup, 0)) }
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(script)); err != nil { t.Fatal(err) }
		post(target, record)
		newIDs := pgcryptoRandomInventory(t, p, target, false, owner)
		for signature, id := range candidate {
			if newIDs[signature] == id { t.Fatal("procedure failed destination remapping", signature, id) }
			name := collisions[id]
			fixture.RunStep(t, p.Ctx, target, record, fixture.Select("SELECT oid::bigint AS oid FROM pg_class WHERE relname=$1", []string{"oid"}, []string{"INT8"}, [][]any{{id}}, name))
			fixture.RunStep(t, p.Ctx, target, record, fixture.Select("SELECT count(*) FROM pg_proc WHERE oid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, id))
			fixture.RunStep(t, p.Ctx, target, record, fixture.Select("SELECT nextval('"+name+"') AS value", []string{"value"}, []string{"INT8"}, [][]any{{int64(73)}}))
		}
		connector, err := other.Driver().(*stuntgres.Driver).OpenConnector("pgcrypto_policy_destination")
		if err != nil { t.Fatal(err) }
		freshPool, freshRecord := fixture.RecordedNativePool(t, connector)
		fresh, err := freshPool.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer fresh.Close()
		pgcryptoPolicyUnseen(t, p, fresh, freshRecord)
		pgcryptoPolicyActivate(t, p, fresh, freshRecord)
		pgcryptoPolicyShow(t, p, fresh, freshRecord, "on")
		fixture.RunStep(t, p.Ctx, fresh, freshRecord, fixture.Select("SELECT pgcrypto_part.fips_mode() AS value", []string{"value"}, []string{"BOOL"}, [][]any{{pgcryptoRandomFIPSExpected(false)}}))
		fixture.RunStep(t, p.Ctx, target, record, fixture.Exec("SET "+pgcryptoPolicyName+"=on", 0))
		fixture.RunStep(t, p.Ctx, target, record, fixture.Exec("INSERT INTO policy_control(id) VALUES(3)", 1))
		fixture.RunStep(t, p.Ctx, target, record, fixture.Select("SELECT h=pgcrypto_part.crypt(p,s) AS valid,d,sha FROM policy_control WHERE id=3", []string{"valid", "d", "sha"}, []string{"BOOL", "TEXT", "TEXT"}, [][]any{{true, "NB53EGGqrrb5E", pgcryptoSHA256Hello}}))
		fixture.RunStep(t, p.Ctx, target, record, fixture.Select("SELECT n.id,n.value=c.h AS valid FROM policy_control_nested n JOIN policy_control c ON c.id=n.id ORDER BY n.id", []string{"id", "valid"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), true}, {int64(3), true}}))
		fixture.RunStep(t, p.Ctx, target, record, fixture.Select("SELECT id FROM policy_control WHERE pgcrypto_part.crypt(p,'NB')='NB53EGGqrrb5E' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		run(fixture.Exec("SET "+pgcryptoPolicyName+"=on", 0))
		fixture.RunStep(t, p.Ctx, held, r, pgcryptoPolicyText(r, q, "NB53EGGqrrb5E", "foox", "NB"))
	})
}

func pgcryptoPolicyStored(t *testing.T, p *fixture.Pair, c *sql.Conn) fixture.Step {
	t.Helper()
	q := "SELECT id,p,s,h,d,sha FROM policy_control ORDER BY id"
	rows, err := c.QueryContext(p.Ctx, q)
	if err != nil { t.Fatal(err) }
	defer rows.Close()
	var values [][]any
	for rows.Next() {
		var id int64
		var password, salt, hash, des, sha string
		if err := rows.Scan(&id, &password, &salt, &hash, &des, &sha); err != nil { t.Fatal(err) }
		if id != int64(len(values)+1) || password != "foox" || des != "NB53EGGqrrb5E" || sha != pgcryptoSHA256Hello || len(hash) != 13 { t.Fatal("independent stored witnesses", id, password, salt, hash, des, sha) }
		pgcryptoPasswordAlgorithmSaltShape(t, salt, "des", 25)
		values = append(values, []any{id, password, salt, hash, des, sha})
	}
	if err := rows.Err(); err != nil || len(values) != 2 { t.Fatal("stored cardinality", len(values), err) }
	return fixture.Select(q, []string{"id", "p", "s", "h", "d", "sha"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, values)
}

func pgcryptoPolicyStoredOff(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, stored fixture.Step) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	pgcryptoPolicyShow(t, p, c, r, "off")
	run(stored)
	run(fixture.Prepared(stored))
	for _, q := range []string{"SELECT value FROM policy_control_dynamic ORDER BY id", "SELECT value FROM policy_control_nested ORDER BY id"} { run(pgcryptoPolicyFailure(q, pgcryptoPolicyOff)) }
	s := pgcryptoPolicyFailure("INSERT INTO policy_control(id) VALUES(9)", pgcryptoPolicyOff)
	s.SelectRows = false
	run(s)
	// First row can be evaluated without a body call; the later non-NULL
	// generated expression fails. No successfully evaluated row may publish.
	s = pgcryptoPolicyFailure("INSERT INTO policy_control(id,p,s,d,sha) VALUES(10,NULL,'NB','kept','kept'),(11,'foox','NB','kept','kept')", pgcryptoPolicyOff)
	s.SelectRows = false
	run(s)
	run(stored)
}

func pgcryptoPolicyControlACL(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	run(fixture.Select("SELECT count(*) FROM pg_parameter_acl WHERE parname='pgcrypto.builtin_crypto_enabled'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
	run(fixture.Exec("SET ROLE policy_control_reader", 0))
	run(fixture.Exec("SET "+pgcryptoPolicyName+"=on", 0))
	run(pgcryptoPolicyText(r, "SELECT pgcrypto_part.crypt($1,$2) AS value", "NB53EGGqrrb5E", "foox", "NB"))
	run(fixture.Exec("SET "+pgcryptoPolicyName+"=off", 0))
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("REVOKE SET ON PARAMETER pgcrypto.builtin_crypto_enabled FROM policy_control_reader", 0))
	run(fixture.Exec("SET ROLE policy_control_reader", 0))
	run(pgcryptoPolicyPermissionError("SET "+pgcryptoPolicyName+"=on"))
	pgcryptoPolicyShow(t, p, c, r, "off")
	run(fixture.Exec("RESET ROLE", 0))
	run(fixture.Exec("GRANT SET ON PARAMETER pgcrypto.builtin_crypto_enabled TO policy_control_reader", 0))
}

func pgcryptoPolicyConsumeStored(t *testing.T, rows *sql.Rows, metadata []*sql.ColumnType, stored fixture.Step) {
	t.Helper()
	if len(metadata) != len(stored.Types) { t.Fatal("retained metadata cardinality", metadata) }
	for i, typ := range metadata {
		if typ.Name() != stored.Columns[i] || typ.DatabaseTypeName() != stored.Types[i] { t.Fatal("retained metadata", i, typ) }
	}
	for _, want := range stored.Rows {
		if !rows.Next() { t.Fatal("retained row missing", rows.Err()) }
		var id int64
		var password, salt, hash, des, sha string
		if err := rows.Scan(&id, &password, &salt, &hash, &des, &sha); err != nil || !reflect.DeepEqual([]any{id, password, salt, hash, des, sha}, want) { t.Fatal("retained data", err) }
	}
	if rows.Next() || rows.Err() != nil { t.Fatal("retained EOF", rows.Err()) }
	rows.Close()
}

func pgcryptoPolicyDependencies(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, members map[string]int64) {
	t.Helper()
	for _, consumer := range []struct{ query string; class int64; signature string }{
		{"SELECT oid FROM pg_attrdef WHERE adrelid='policy_control'::regclass AND adnum=3", 2604, "pgcrypto_part.gen_salt(text)"},
		{"SELECT oid FROM pg_attrdef WHERE adrelid='policy_control'::regclass AND adnum=4", 2604, "pgcrypto_part.crypt(text,text)"},
		{"SELECT oid FROM pg_attrdef WHERE adrelid='policy_control'::regclass AND adnum=5", 2604, "pgcrypto_part.crypt(text,text)"},
		{"SELECT oid FROM pg_attrdef WHERE adrelid='policy_control'::regclass AND adnum=6", 2604, "pgcrypto_part.crypt(text,text)"},
		{"SELECT oid FROM pg_constraint WHERE conrelid='policy_control'::regclass AND conname='policy_control_valid'", 2606, "pgcrypto_part.crypt(text,text)"},
		{"SELECT 'policy_control_idx'::regclass::oid", 1259, "pgcrypto_part.crypt(text,text)"},
		{"SELECT oid FROM pg_rewrite WHERE ev_class='policy_control_dynamic'::regclass AND rulename='_RETURN'", 2618, "pgcrypto_part.crypt(text,text)"},
		{"SELECT oid FROM pg_rewrite WHERE ev_class='policy_control_folded'::regclass AND rulename='_RETURN'", 2618, "pgcrypto_part.crypt(text,text)"},
	} {
		var id int64
		if err := c.QueryRowContext(p.Ctx, consumer.query).Scan(&id); err != nil || id <= 0 { t.Fatal("stored consumer address", consumer.query, id, err) }
		member := members[consumer.signature]
		if member <= 0 { t.Fatal("missing installed dependency target", consumer.signature) }
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_depend WHERE classid=$1 AND objid=$2 AND objsubid=0 AND refclassid=1255 AND refobjid=$3 AND refobjsubid=0 AND deptype='n'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, consumer.class, id, member))
	}
}
