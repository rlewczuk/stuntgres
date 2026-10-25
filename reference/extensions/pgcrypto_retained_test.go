package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type pgcryptoTempCapture struct {
	namespace, relation, rowtype int64
	schema                       string
	rows                         *sql.Rows
	metadata                     []*sql.ColumnType
	columns                      []string
	snapshot                     []pgcryptoTempColumn
}

type pgcryptoTempColumn struct {
	name, databaseType string
	scan               reflect.Type
	nullable, known    bool
}

func pgcryptoTempMetadata(t *testing.T, columns []string, metadata []*sql.ColumnType) []pgcryptoTempColumn {
	t.Helper()
	if !reflect.DeepEqual(columns, []string{"v", "b"}) || len(metadata) != 2 {
		t.Fatal("retained TEMP columns", columns, metadata)
	}
	snapshot := make([]pgcryptoTempColumn, 2)
	for i, typ := range metadata {
		nullable, known := typ.Nullable()
		snapshot[i] = pgcryptoTempColumn{typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), nullable, known}
	}
	want := []pgcryptoTempColumn{
		{"v", "INT4", reflect.TypeOf(int64(0)), true, true},
		{"b", "BYTEA", reflect.TypeOf([]byte{}), true, true},
	}
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatal("retained TEMP metadata", snapshot, want)
	}
	return snapshot
}

func pgcryptoTempLinks(t *testing.T, p *fixture.Pair, c *sql.Conn) {
	t.Helper()
	var rowtype, namespace, relation, element, arrayRelation bool
	if err := c.QueryRowContext(p.Ctx, "SELECT t.oid=c.reltype,t.typnamespace=c.relnamespace,t.typrelid=c.oid,a.typelem=t.oid,a.typrelid=0 FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pgcrypto_retained'::regclass::oid").Scan(&rowtype, &namespace, &relation, &element, &arrayRelation); err != nil || !rowtype || !namespace || !relation || !element || !arrayRelation {
		t.Fatal("TEMP inverse type links", rowtype, namespace, relation, element, arrayRelation, err)
	}
}

func pgcryptoCaptureTemp(t *testing.T, p *fixture.Pair, c *sql.Conn) pgcryptoTempCapture {
	t.Helper()
	var capture pgcryptoTempCapture
	if err := c.QueryRowContext(p.Ctx, "SELECT n.oid,c.oid,c.reltype,n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='pgcrypto_retained'::regclass::oid").Scan(&capture.namespace, &capture.relation, &capture.rowtype, &capture.schema); err != nil {
		t.Fatal(err)
	}
	if capture.namespace <= 0 || capture.relation <= 0 || capture.rowtype <= 0 {
		t.Fatal("invalid TEMP identities", capture)
	}
	// Qualified REGTYPE is an independent authority alongside inverse SQL links.
	pgcryptoTempLinks(t, p, c)
	typeName := `"` + strings.ReplaceAll(capture.schema, `"`, `""`) + `".pgcrypto_retained`
	var resolvedRelation, resolvedType int64
	if err := c.QueryRowContext(p.Ctx, "SELECT $1::text::regclass::oid,$1::text::regtype::oid", typeName).Scan(&resolvedRelation, &resolvedType); err != nil || resolvedRelation != capture.relation || resolvedType != capture.rowtype {
		t.Fatal("TEMP qualified relation/row type identities", resolvedRelation, resolvedType, capture.relation, capture.rowtype, err)
	}
	var err error
	capture.rows, err = c.QueryContext(p.Ctx, "SELECT v,b FROM pgcrypto_retained")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { capture.rows.Close() })
	capture.metadata, err = capture.rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	capture.columns, err = capture.rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	capture.snapshot = pgcryptoTempMetadata(t, capture.columns, capture.metadata)
	return capture
}

func (capture pgcryptoTempCapture) unchanged(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	pgcryptoTempLinks(t, p, c)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT n.oid::bigint AS namespace,c.oid::bigint AS relation,c.reltype::bigint AS rowtype,n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='pgcrypto_retained'::regclass::oid", []string{"namespace", "relation", "rowtype", "nspname"}, []string{"INT8", "INT8", "INT8", "NAME"}, [][]any{{capture.namespace, capture.relation, capture.rowtype, capture.schema}}))
	typeName := `"` + strings.ReplaceAll(capture.schema, `"`, `""`) + `".pgcrypto_retained`
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT $1::text::regclass::oid::bigint AS relation,$1::text::regtype::oid::bigint AS rowtype", []string{"relation", "rowtype"}, []string{"INT8", "INT8"}, [][]any{{capture.relation, capture.rowtype}}, typeName))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT v,b FROM pgcrypto_retained", []string{"v", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(73), pgcryptoBytes(t, "0080ff")}}))
	columns, err := capture.rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := capture.rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, capture.columns) || !reflect.DeepEqual(pgcryptoTempMetadata(t, columns, metadata), capture.snapshot) || !reflect.DeepEqual(pgcryptoTempMetadata(t, capture.columns, capture.metadata), capture.snapshot) {
		t.Fatal("retained TEMP metadata changed")
	}
}
func (capture pgcryptoTempCapture) consume(t *testing.T) {
	t.Helper()
	if !capture.rows.Next() {
		t.Fatal("lost unread TEMP row", capture.rows.Err())
	}
	var v int64
	var b []byte
	if err := capture.rows.Scan(&v, &b); err != nil || v != 73 || !bytes.Equal(b, []byte{0, 128, 255}) {
		t.Fatal("retained TEMP row", v, b, err)
	}
	if capture.rows.Next() || capture.rows.Err() != nil {
		t.Fatal("retained TEMP tail", capture.rows.Err())
	}
	// Next exhausted and closed the cursor: only saved metadata is inspected.
	if !reflect.DeepEqual(pgcryptoTempMetadata(t, capture.columns, capture.metadata), capture.snapshot) {
		t.Fatal("retained TEMP metadata changed after consumption")
	}
}

type pgcryptoTempViewCapture struct {
	namespace, relation, rule, target int64
}

func pgcryptoCaptureTempView(t *testing.T, p *fixture.Pair, c *sql.Conn) pgcryptoTempViewCapture {
	t.Helper()
	var capture pgcryptoTempViewCapture
	if err := c.QueryRowContext(p.Ctx, "SELECT c.relnamespace,c.oid,r.oid,'pgcrypto_view'::regclass::oid FROM pg_class c JOIN pg_rewrite r ON r.ev_class=c.oid WHERE c.oid='pgcrypto_stale_capture'::regclass::oid AND r.rulename='_RETURN'").Scan(&capture.namespace, &capture.relation, &capture.rule, &capture.target); err != nil || capture.namespace <= 0 || capture.relation <= 0 || capture.rule <= 0 || capture.target <= 0 {
		t.Fatal("blocking TEMP view identity", capture, err)
	}
	return capture
}

func (capture pgcryptoTempViewCapture) unchanged(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT c.relnamespace::bigint AS namespace,c.oid::bigint AS relation,r.oid::bigint AS rule,'pgcrypto_view'::regclass::oid::bigint AS target FROM pg_class c JOIN pg_rewrite r ON r.ev_class=c.oid WHERE c.oid='pgcrypto_stale_capture'::regclass::oid AND r.rulename='_RETURN'", []string{"namespace", "relation", "rule", "target"}, []string{"INT8", "INT8", "INT8", "INT8"}, [][]any{{capture.namespace, capture.relation, capture.rule, capture.target}}))
	want := [][]any{
		fixture.PgDependEdge(2618, capture.rule, 0, 1259, capture.relation, 0, "i"),
		fixture.PgDependEdge(2618, capture.rule, 0, 1259, capture.target, 1, "n"),
		fixture.PgDependEdge(2618, capture.rule, 0, 1259, capture.target, 2, "n"),
	}
	// Ownership and the two projected persistent columns are separate edges.
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT classid::bigint AS classid,objid::bigint AS objid,objsubid,refclassid::bigint AS refclassid,refobjid::bigint AS refobjid,refobjsubid,deptype FROM pg_depend WHERE classid=2618 AND objid=$1 AND refclassid=1259 ORDER BY deptype,refobjsubid", []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"INT8", "INT8", "INT4", "INT8", "INT8", "INT4", "CHAR"}, want, capture.rule))
}

func pgcryptoFreshConsumers(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, failure fixture.Step) {
	t.Helper()
	run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, step) }
	sha := pgcryptoBytes(t, pgcryptoDigestVectors[3].abc)
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO pgcrypto_stored(id,v) VALUES(4,'abc')", 1))
	run(fixture.Select("SELECT d,g FROM pgcrypto_stored WHERE id=4", []string{"d", "g"}, []string{"BYTEA", "BYTEA"}, [][]any{{sha, sha}}))
	run(fixture.Select("SELECT digest,hmac FROM pgcrypto_view WHERE id=4", []string{"digest", "hmac"}, []string{"BYTEA", "BYTEA"}, [][]any{{sha, pgcryptoBytes(t, "7cf4ec4f741f51cb0d887013c46251d6f4175643c4f422906a1aaec688cc13e8")}}))
	run(fixture.Select("SELECT digest FROM pgcrypto_nested WHERE id=4", []string{"digest"}, []string{"BYTEA"}, [][]any{{sha}}))
	run(fixture.Select("SELECT id FROM pgcrypto_stored WHERE pgcrypto_part.digest(v,'sha256')='\\x"+pgcryptoDigestVectors[3].abc+"'::bytea AND id=4 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}))
	run(fixture.Exec("INSERT INTO pgcrypto_atomic VALUES('abc')", 1))
	run(fixture.Exec("ROLLBACK", 0))
	run(failure)
	run(fixture.Select("SELECT count(*) FROM pgcrypto_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

type pgcryptoHeldCall struct {
	statement *sql.Stmt
	step      fixture.Step
	rawUUID   bool
}

func pgcryptoHoldMembers(t *testing.T, p *fixture.Pair, c *sql.Conn) []pgcryptoHeldCall {
	t.Helper()
	calls := []fixture.Step{
		fixture.Select("SELECT pgcrypto_part.digest($1::text,'sha256')", []string{"digest"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, pgcryptoDigestVectors[3].abc)}}, "abc"),
		fixture.Select("SELECT pgcrypto_part.digest($1::bytea,'sha256')", []string{"digest"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, "5240672d7b51756b829ad0ef8d9468b7a078afa2f410484fd3892dab47becb72")}}, []byte{0, 128, 255}),
		fixture.Select("SELECT pgcrypto_part.hmac($1::text,$2::text,'sha256')", []string{"hmac"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843")}}, "what do ya want for nothing?", "Jefe"),
		fixture.Select("SELECT pgcrypto_part.hmac($1::bytea,$2::bytea,'sha256')", []string{"hmac"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, "f085c3bd7fc8d8f90f1a475a943a7f03a97c496a81013f8d54d2e2d229ab562c")}}, []byte{0, 128, 255}, []byte{255, 0, 128}),
		fixture.Select("SELECT pgcrypto_part.crypt($1,$2) AS value", []string{"value"}, []string{"TEXT"}, [][]any{{pgcryptoPasswordFoox}}, "foox", pgcryptoPasswordSalt),
		fixture.Select("SELECT pgcrypto_part.gen_salt($1) LIKE '$2a$06$%' AS valid", []string{"valid"}, []string{"BOOL"}, [][]any{{true}}, "bf"),
		fixture.Select("SELECT pgcrypto_part.gen_salt($1,$2) LIKE '$2a$04$%' AS valid", []string{"valid"}, []string{"BOOL"}, [][]any{{true}}, "bf", 4),
		fixture.Select("SELECT pgcrypto_part.encrypt($1::bytea,$2::bytea,$3)", []string{"encrypt"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, "f397f03d2819b7172b68d0706fda4693")}}, []byte("foo"), []byte("0123456789"), "aes"),
		fixture.Select("SELECT pgcrypto_part.decrypt($1::bytea,$2::bytea,$3)", []string{"decrypt"}, []string{"BYTEA"}, [][]any{{"foo"}}, aesBytes(t, "f397f03d2819b7172b68d0706fda4693"), []byte("0123456789"), "aes"),
		fixture.Select("SELECT pgcrypto_part.encrypt_iv($1::bytea,$2::bytea,$3::bytea,$4)", []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, "2c24cb7da91d6d5699801268b0f5adad")}}, []byte("foo"), []byte("0123456"), []byte("abcd"), "aes"),
		fixture.Select("SELECT pgcrypto_part.decrypt_iv($1::bytea,$2::bytea,$3::bytea,$4)", []string{"decrypt_iv"}, []string{"BYTEA"}, [][]any{{"foo"}}, aesBytes(t, "2c24cb7da91d6d5699801268b0f5adad"), []byte("0123456"), []byte("abcd"), "aes"),
		fixture.Select("SELECT octet_length(pgcrypto_part.gen_random_bytes($1)) AS size", []string{"size"}, []string{"INT4"}, [][]any{{int64(32)}}, 32),
		fixture.Select("SELECT pgcrypto_part.fips_mode() AS value", []string{"value"}, []string{"BOOL"}, [][]any{{pgcryptoRandomFIPSExpected(false)}}),
	}
	var held []pgcryptoHeldCall
	for _, step := range calls {
		statement, err := c.PrepareContext(p.Ctx, step.Query)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { statement.Close() })
		held = append(held, pgcryptoHeldCall{statement: statement, step: step})
	}
	q := "SELECT pgcrypto_part.gen_random_uuid() AS value"
	statement, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { statement.Close() })
	held = append(held, pgcryptoHeldCall{statement: statement, step: fixture.Step{Query: q}, rawUUID: true})
	return held
}

// These retained-control callers are native-only. Validate unpredictable UUIDs
// directly, rather than adding a cast outside the existing closed-source seam.
func pgcryptoRunHeldCall(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, call pgcryptoHeldCall) {
	t.Helper()
	if call.rawUUID {
		rows, err := call.statement.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		pgcryptoRandomUUIDRows(t, rows, false)
		return
	}
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: call.statement, SQL: call.step.Query}, r, call.step)
}
