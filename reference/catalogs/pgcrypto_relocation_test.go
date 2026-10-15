package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgcryptoReplayOverloads(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	pgcryptoResult(t, p, c, r, "digest", "SELECT pgcrypto_part.digest('abc','sha256')", pgcryptoDigestVectors[3].abc)
	pgcryptoResult(t, p, c, r, "digest", "SELECT pgcrypto_part.digest($1::bytea,'sha256')", "5240672d7b51756b829ad0ef8d9468b7a078afa2f410484fd3892dab47becb72", []byte{0, 128, 255})
	pgcryptoResult(t, p, c, r, "hmac", "SELECT pgcrypto_part.hmac('what do ya want for nothing?','Jefe','sha256')", "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843")
	pgcryptoResult(t, p, c, r, "hmac", "SELECT pgcrypto_part.hmac($1::bytea,$2::bytea,'sha256')", "f085c3bd7fc8d8f90f1a475a943a7f03a97c496a81013f8d54d2e2d229ab562c", []byte{0, 128, 255}, []byte{255, 0, 128})
	pgcryptoPasswordText(t, p, c, r, "SELECT pgcrypto_part.crypt('foox',$1) AS value", pgcryptoPasswordFoox, pgcryptoPasswordSalt)
	pgcryptoPasswordGenerated(t, p, c, r, "SELECT pgcrypto_part.gen_salt('bf')", "pgcrypto_part.crypt", 6)
	pgcryptoPasswordGenerated(t, p, c, r, "SELECT pgcrypto_part.gen_salt('bf',4)", "pgcrypto_part.crypt", 4)
	pgcryptoResult(t, p, c, r, "encrypt", "SELECT pgcrypto_part.encrypt('foo','0123456789','aes')", "f397f03d2819b7172b68d0706fda4693")
	pgcryptoResult(t, p, c, r, "decrypt", "SELECT pgcrypto_part.decrypt('\\xf397f03d2819b7172b68d0706fda4693'::bytea,'0123456789','aes')", "666f6f")
	pgcryptoResult(t, p, c, r, "encrypt_iv", "SELECT pgcrypto_part.encrypt_iv('foo','0123456','abcd','aes')", "2c24cb7da91d6d5699801268b0f5adad")
	pgcryptoResult(t, p, c, r, "decrypt_iv", "SELECT pgcrypto_part.decrypt_iv('\\x2c24cb7da91d6d5699801268b0f5adad'::bytea,'0123456','abcd','aes')", "666f6f")
}

func pgcryptoProcedureIDs(t *testing.T, p *fixture.Pair, c *sql.Conn) map[string]int64 {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT p.oid::regprocedure::text,p.oid FROM pg_proc p JOIN pg_extension e ON e.extnamespace=p.pronamespace WHERE e.extname='pgcrypto' AND proname IN ('digest','hmac','crypt','gen_salt','encrypt','decrypt','encrypt_iv','decrypt_iv')")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := make(map[string]int64)
	for rows.Next() {
		var signature string
		var id int64
		if err := rows.Scan(&signature, &id); err != nil {
			t.Fatal(err)
		}
		if id <= 0 || ids[signature] != 0 {
			t.Fatal("invalid procedure identity", signature, id)
		}
		ids[signature] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 11 {
		t.Fatal("procedure inventory", ids)
	}
	return ids
}

func pgcryptoConsumerDependencies(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, ids map[string]int64) map[string]int64 {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT c.relname,r.rolname=current_user AS owned FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.relname IN ('pgcrypto_atomic','pgcrypto_hash_idx','pgcrypto_nested','pgcrypto_stored','pgcrypto_view') ORDER BY c.relname", []string{"relname", "owned"}, []string{"NAME", "BOOL"}, [][]any{{"pgcrypto_atomic", true}, {"pgcrypto_hash_idx", true}, {"pgcrypto_nested", true}, {"pgcrypto_stored", true}, {"pgcrypto_view", true}}))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname='pgcrypto_part'", []string{"rolname"}, []string{"NAME"}, [][]any{{"pgcrypto_owner"}}))
	// Resolve named consumers independently of pg_depend. Generated expressions
	// have pg_attrdef addresses too; their dependent subobject ID is zero.
	type consumer struct {
		name, query string
		class       int64
	}
	consumers := []consumer{
		{"default", "SELECT oid FROM pg_attrdef WHERE adrelid='pgcrypto_stored'::regclass::oid AND adnum=3", 2604},
		{"generated", "SELECT oid FROM pg_attrdef WHERE adrelid='pgcrypto_stored'::regclass::oid AND adnum=4", 2604},
		{"check", "SELECT oid FROM pg_constraint WHERE conrelid='pgcrypto_stored'::regclass::oid AND conname='pgcrypto_valid'", 2606},
		{"atomic_check", "SELECT oid FROM pg_constraint WHERE conrelid='pgcrypto_atomic'::regclass::oid AND conname='pgcrypto_atomic_valid'", 2606},
		{"index", "SELECT oid FROM pg_class WHERE oid='pgcrypto_hash_idx'::regclass::oid", 1259},
		{"view", "SELECT oid FROM pg_rewrite WHERE ev_class='pgcrypto_view'::regclass::oid AND rulename='_RETURN'", 2618},
		{"nested", "SELECT oid FROM pg_rewrite WHERE ev_class='pgcrypto_nested'::regclass::oid AND rulename='_RETURN'", 2618},
	}
	addresses := make(map[string]int64)
	for _, consumer := range consumers {
		var id int64
		if err := c.QueryRowContext(p.Ctx, consumer.query).Scan(&id); err != nil || id <= 0 {
			t.Fatal("named consumer identity", consumer.name, id, err)
		}
		addresses[consumer.name] = id
	}
	var stored int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE oid='pgcrypto_stored'::regclass::oid").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	addresses["stored"] = stored
	for name, query := range map[string]string{
		"extension":       "SELECT oid FROM pg_extension WHERE extname='pgcrypto'",
		"extension_owner": "SELECT extowner FROM pg_extension WHERE extname='pgcrypto'",
		"schema":          "SELECT oid FROM pg_namespace WHERE nspname='pgcrypto_part'",
		"schema_owner":    "SELECT nspowner FROM pg_namespace WHERE nspname='pgcrypto_part'",
		"view_relation":   "SELECT oid FROM pg_class WHERE oid='pgcrypto_view'::regclass::oid",
		"nested_relation": "SELECT oid FROM pg_class WHERE oid='pgcrypto_nested'::regclass::oid",
	} {
		var id int64
		if err := c.QueryRowContext(p.Ctx, query).Scan(&id); err != nil || id <= 0 {
			t.Fatal("owned identity", name, id, err)
		}
		addresses[name] = id
	}
	for _, definition := range []struct {
		name, kind string
		sub        int64
	}{{"default", "a", 3}, {"generated", "i", 4}} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT classid::bigint AS classid,objid::bigint AS objid,objsubid,refclassid::bigint AS refclassid,refobjid::bigint AS refobjid,refobjsubid,deptype FROM pg_depend WHERE classid=2604 AND objid=$1 AND refclassid=1259 AND refobjid=$2 AND deptype=$3", []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, [][]any{fixture.PgDependEdge(2604, addresses[definition.name], 0, 1259, stored, definition.sub, definition.kind)}, addresses[definition.name], stored, definition.kind))
	}
	for _, name := range []string{"view", "nested"} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT classid::bigint AS classid,objid::bigint AS objid,objsubid,refclassid::bigint AS refclassid,refobjid::bigint AS refobjid,refobjsubid,deptype FROM pg_depend WHERE classid=2618 AND objid=$1 AND deptype='i'", []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, [][]any{fixture.PgDependEdge(2618, addresses[name], 0, 1259, addresses[name+"_relation"], 0, "i")}, addresses[name]))
	}
	for signature, id := range ids {
		want := [][]any{}
		for _, consumer := range consumers {
			if signature == "pgcrypto_part.digest(text,text)" || signature == "pgcrypto_part.hmac(text,text,text)" && consumer.name == "view" {
				want = append(want, fixture.PgDependEdge(consumer.class, addresses[consumer.name], 0, 1255, id, 0, "n"))
			}
		}
		sort.Slice(want, func(i, j int) bool {
			if want[i][0] != want[j][0] {
				return want[i][0].(int64) < want[j][0].(int64)
			}
			return want[i][1].(int64) < want[j][1].(int64)
		})
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT classid::bigint AS classid,objid::bigint AS objid,objsubid,refclassid::bigint AS refclassid,refobjid::bigint AS refobjid,refobjsubid,deptype FROM pg_depend WHERE refclassid=1255 AND refobjid=$1 AND deptype='n' ORDER BY classid,objid,objsubid", []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, want, id))
	}
	return addresses
}

func pgcryptoForcedRelocation(t *testing.T, p *fixture.Pair, source, baseline *sql.Conn, sourceRecord, baselineRecord *fixture.Recorder, script string, check, view, nested, failure fixture.Step) {
	t.Helper()
	// The ordinary replay supplies actual candidate identities, independently
	// of allocator assumptions about the original SQL creation sequence.
	candidate := pgcryptoProcedureIDs(t, p, baseline)
	original := pgcryptoProcedureIDs(t, p, source)
	pgcryptoConsumerDependencies(t, p, baseline, baselineRecord, candidate)
	other, c, r := pgcryptoControlDestination(t, p, "pgcrypto_collision")
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE USER pgcrypto_owner NOSUPERUSER", 0))
	var max int64
	for _, id := range candidate {
		if id > max {
			max = id
		}
	}
	// Every sequence consumes a real catalog OID. Stop at the observed upper
	// bound, and require actual collisions for all eleven procedures below.
	collisions := make(map[int64]string)
	sequenceNamespaces := make(map[int64]int64)
	for i := range 128 {
		name := fmt.Sprintf("pgcrypto_collision_%d", i)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TEMP SEQUENCE "+name+" START 73", 0))
		var id, namespace int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid,relnamespace FROM pg_class WHERE relname=$1", name).Scan(&id, &namespace); err != nil || id <= 0 || namespace <= 0 {
			t.Fatal("TEMP sequence identity", id, namespace, err)
		}
		collisions[id] = name
		sequenceNamespaces[id] = namespace
		if id >= max {
			break
		}
	}
	for signature, id := range candidate {
		if collisions[id] == "" {
			t.Fatal("did not force admitted procedure collision", signature, id, collisions)
		}
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TEMP TABLE pgcrypto_retained(v integer,b bytea)", 0))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_retained VALUES(73,'\\x0080ff'::bytea)", 1))
	temp := pgcryptoCaptureTemp(t, p, c)
	tempSQL := "SELECT v,b FROM pgcrypto_retained"
	tempHeld, err := c.PrepareContext(p.Ctx, tempSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer tempHeld.Close()
	captured, err := baseline.QueryContext(p.Ctx, view.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	metadata, err := captured.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	held, err := c.PrepareContext(p.Ctx, "SELECT nextval('"+collisions[candidate["pgcrypto_part.digest(text,text)"]]+"')")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(script)); err != nil {
		t.Fatal("forced relocation Restore", err)
	}
	pgcryptoInventory(t, p, c, "pgcrypto_owner", "pgcrypto_owner")
	destination := pgcryptoProcedureIDs(t, p, c)
	temp.unchanged(t, p, c, r)
	for id, name := range collisions {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT oid::bigint AS oid,relnamespace::bigint AS namespace FROM pg_class WHERE relname=$1", []string{"oid", "namespace"}, []string{"INT8", "INT8"}, [][]any{{id, sequenceNamespaces[id]}}, name))
	}
	for signature, id := range candidate {
		if destination[signature] == id || destination[signature] == original[signature] {
			t.Fatal("procedure not relocated", signature, original[signature], id, destination[signature])
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT oid::bigint AS oid,relnamespace::bigint AS namespace FROM pg_class WHERE relname=$1", []string{"oid", "namespace"}, []string{"INT8", "INT8"}, [][]any{{id, sequenceNamespaces[id]}}, collisions[id]))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_proc WHERE oid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, id))
	}
	pgcryptoReplayOverloads(t, p, c, r)
	consumerIDs := pgcryptoConsumerDependencies(t, p, c, r, destination)
	cryptoHeld := pgcryptoHoldMembers(t, p, c)
	for _, call := range cryptoHeld {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: call.statement, SQL: call.step.Query}, r, call.step)
	}
	for _, step := range []fixture.Step{check, view, nested, fixture.Prepared(nested), failure} {
		fixture.RunStep(t, p.Ctx, c, r, step)
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT v FROM pgcrypto_retained", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pgcrypto_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_stored(id,v) VALUES(3,'abc')", 1))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT d,g FROM pgcrypto_stored WHERE id=3", []string{"d", "g"}, []string{"BYTEA", "BYTEA"}, [][]any{{pgcryptoBytes(t, pgcryptoDigestVectors[3].abc), pgcryptoBytes(t, pgcryptoDigestVectors[3].abc)}}))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id FROM pgcrypto_stored WHERE pgcrypto_part.digest(v,'sha256')='\\x"+pgcryptoDigestVectors[3].abc+"'::bytea ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
	var next int64
	if err := held.QueryRowContext(p.Ctx).Scan(&next); err != nil || next != 73 {
		t.Fatal("retained TEMP held capture", next, err)
	}
	if !reflect.DeepEqual(candidate, pgcryptoProcedureIDs(t, p, baseline)) || !reflect.DeepEqual(original, pgcryptoProcedureIDs(t, p, source)) {
		t.Fatal("destination replacement mutated source identities")
	}
	for i, expected := range [][]any{{int64(1), []byte(pgcryptoBytes(t, pgcryptoDigestVectors[3].abc)), []byte(pgcryptoBytes(t, "7cf4ec4f741f51cb0d887013c46251d6f4175643c4f422906a1aaec688cc13e8"))}, {int64(2), nil, nil}} {
		if !captured.Next() {
			t.Fatal("lost source capture", i, captured.Err())
		}
		var id int64
		var digest, mac []byte
		if err := captured.Scan(&id, &digest, &mac); err != nil || id != expected[0] || (i == 0 && (!bytes.Equal(digest, expected[1].([]byte)) || !bytes.Equal(mac, expected[2].([]byte)))) || (i == 1 && (digest != nil || mac != nil)) {
			t.Fatal("source capture changed", i, id, digest, mac, err)
		}
	}
	if captured.Next() || captured.Err() != nil || metadata[1].DatabaseTypeName() != "BYTEA" || metadata[2].DatabaseTypeName() != "BYTEA" {
		t.Fatal("source capture tail/metadata", captured.Err())
	}
	// Each refusal is followed by the same complete destination witness.
	// Fresh writes roll back so the persistent snapshot remains comparable.
	var blockingView *pgcryptoTempViewCapture
	postcondition := func(before string) {
		t.Helper()
		if extensionDump(t, p.Ctx, other) != before || !reflect.DeepEqual(destination, pgcryptoProcedureIDs(t, p, c)) {
			t.Fatal("refused replay changed persistent destination")
		}
		pgcryptoInventory(t, p, c, "pgcrypto_owner", "pgcrypto_owner")
		if !reflect.DeepEqual(consumerIDs, pgcryptoConsumerDependencies(t, p, c, r, destination)) {
			t.Fatal("refused replay changed named consumer identities")
		}
		temp.unchanged(t, p, c, r)
		if blockingView != nil {
			blockingView.unchanged(t, p, c, r)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: tempHeld, SQL: tempSQL}, r, fixture.Select(tempSQL, []string{"v", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(73), pgcryptoBytes(t, "0080ff")}}))
		for id, name := range collisions {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT oid::bigint AS oid,relnamespace::bigint AS namespace FROM pg_class WHERE relname=$1", []string{"oid", "namespace"}, []string{"INT8", "INT8"}, [][]any{{id, sequenceNamespaces[id]}}, name))
		}
		for _, call := range cryptoHeld {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: call.statement, SQL: call.step.Query}, r, call.step)
		}
		pgcryptoReplayOverloads(t, p, c, r)
		pgcryptoFreshConsumers(t, p, c, r, failure)
		if extensionDump(t, p.Ctx, other) != before {
			t.Fatal("consumer witness changed persistent data")
		}
	}
	badReplays := []string{
		strings.Replace(script, "VERSION '1.4'", "VERSION 'missing'", 1),
		strings.Replace(script, "stuntgres-extension-owners-v1", "stuntgres-extension-owners-v2", 1),
		script + "\nCREATE EXTENSION nonexistent_provider;",
		script + "\nINVALID SQL;",
		// A staging engine may not borrow the live destination's private TEMP
		// namespace even when its concrete name is known.
		"CREATE EXTENSION pgcrypto SCHEMA \"" + strings.ReplaceAll(temp.schema, "\"", "\"\"") + "\" VERSION '1.4';",
	}
	badReplays = append(badReplays, pgcryptoInvalidOwnerDumps(t, script)...)
	for i, bad := range badReplays {
		before := extensionDump(t, p.Ctx, other)
		unread := pgcryptoCaptureTemp(t, p, c)
		err := stuntgres.Restore(p.Ctx, other, strings.NewReader(bad))
		if err == nil {
			t.Fatal("invalid destination replay accepted", i)
		}
		if i == 4 {
			var diagnostic *stuntgres.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "3F000" {
				t.Fatal("private TEMP placement diagnostic", err)
			}
		}
		postcondition(before)
		unread.unchanged(t, p, c, r)
		unread.consume(t)
	}
	before := extensionDump(t, p.Ctx, other)
	unread := pgcryptoCaptureTemp(t, p, c)
	ctx, cancel := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(ctx, other, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled destination replay", err)
	}
	postcondition(before)
	unread.unchanged(t, p, c, r)
	unread.consume(t)
	if err := held.QueryRowContext(p.Ctx).Scan(&next); err != nil || next != 74 {
		t.Fatal("failed replay changed retained generator", next, err)
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TEMP VIEW pgcrypto_stale_capture AS SELECT id,digest FROM pgcrypto_view", 0))
	stale := pgcryptoCaptureTempView(t, p, c)
	if stale.namespace != temp.namespace || stale.target != consumerIDs["view_relation"] {
		t.Fatal("blocking TEMP view namespace/target", stale)
	}
	blockingView = &stale
	blockingView.unchanged(t, p, c, r)
	before = extensionDump(t, p.Ctx, other)
	err = stuntgres.Restore(p.Ctx, other, strings.NewReader(script))
	if err == nil || err.Error() != "stuntgres: catalog replacement with live temporary view persistent dependencies is unsupported" {
		t.Fatal("stale persistent consumer identity not refused", err)
	}
	postcondition(before)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT digest FROM pgcrypto_stale_capture ORDER BY id", []string{"digest"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, pgcryptoDigestVectors[3].abc)}, {nil}, {pgcryptoBytes(t, pgcryptoDigestVectors[3].abc)}}))
	temp.consume(t)
	fixture.RunStep(t, p.Ctx, source, sourceRecord, fixture.Select("SELECT count(*) FROM pgcrypto_stored", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
	fixture.RunStep(t, p.Ctx, baseline, baselineRecord, check)
}
