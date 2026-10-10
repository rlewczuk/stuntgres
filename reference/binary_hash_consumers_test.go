package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceBinaryHashConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			binaryHashSetup(t, p, backend.db, backend.recorder)
			for _, s := range []referenceStep{
				referenceSelect("SELECT id FROM hash_input WHERE crc32(v)=891568578 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}),
				referenceSelect("SELECT id FROM hash_input WHERE id<3 ORDER BY crc32(v)", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(0)}}),
				referenceSelect("SELECT crc32(v),count(*) FROM hash_input WHERE id<3 GROUP BY crc32(v) ORDER BY crc32(v)", []string{"crc32", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(0), int64(1)}, {int64(891568578), int64(1)}, {nil, int64(1)}}),
				referenceSelect("SELECT CASE WHEN id=0 THEN sha256(v) ELSE sha256('abc') END AS digest FROM hash_input WHERE id<3 ORDER BY id", []string{"digest"}, []string{"BYTEA"}, [][]any{{nil}, {binaryHashBytes(t, binaryHashCrypto[1][2])}, {binaryHashBytes(t, binaryHashCrypto[1][2])}}),
				referenceExec("CREATE TABLE hash_dst(id INTEGER,d BYTEA)", 0),
				referenceExec("INSERT INTO hash_dst VALUES(1,sha256('abc'))", 1),
				referenceSelect("SELECT id,d FROM hash_dst", []string{"id", "d"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), binaryHashBytes(t, binaryHashCrypto[1][2])}}),
				referenceSelect("UPDATE hash_dst SET d=sha256('') WHERE id=1 RETURNING id,d", []string{"id", "d"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), binaryHashBytes(t, binaryHashCrypto[0][2])}}),
				referenceExec("INSERT INTO hash_dst VALUES(2,sha256('abc'))", 1),
				referenceExec("UPDATE hash_dst SET d=sha256('abc') WHERE id=1", 1),
			} {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			for _, s := range []referenceStep{
				referenceSelect("SELECT sha256(sha224(v)) FROM hash_input WHERE id=2", []string{"sha256"}, []string{"BYTEA"}, [][]any{{binaryHashBytes(t, "a05a17b2ee93714f17d1d1cdcf7366729149ebc8f7198a467d8c6b0338f3ee54")}}),
				referenceSelect("SELECT sha256($1),md5($1) FROM hash_input WHERE v=$1 AND id=2", []string{"sha256", "md5"}, []string{"BYTEA", "TEXT"}, [][]any{{binaryHashBytes(t, binaryHashCrypto[1][2]), binaryHashCrypto[1][0]}}, []byte("abc")),
				referenceSelect("SELECT md5(sha256('abc'))", []string{"md5"}, []string{"TEXT"}, [][]any{{"6758bdecb9389db85fa950e7fa2fce82"}}),
			} {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(s))
			}
			md5First := "SELECT md5($1),sha256($1) FROM hash_input WHERE v=$1 AND id=2"
			diagnostic := referenceDiagnostic{Code: "42883", Message: "function sha256(text) does not exist", Hint: binaryHashHint, Position: "16"}
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(md5First, diagnostic, []byte("abc"))
				s.selectRows = true
				s.errorStage = "execute"
				if prepared {
					s = referencePrepared(s)
					s.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			contextual := "SELECT sha256($1),md5($1) FROM hash_input WHERE v=$1"
			contextualConn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer contextualConn.Close()
			contextualStmt, err := contextualConn.PrepareContext(p.ctx, contextual)
			if err != nil {
				t.Fatal(err)
			}
			defer contextualStmt.Close()
			contextualHeld := referenceHeldStatement{referenceExecutor: contextualConn, statement: contextualStmt, query: contextual}
			for _, tc := range []struct {
				arg   any
				index int
			}{{[]byte("abc"), 1}, {[]byte{0}, 2}, {[]byte{0, 128, 255}, 3}, {[]byte{}, 0}, {nil, -1}, {[]byte("abc"), 1}} {
				var want [][]any
				if tc.index >= 0 {
					v := binaryHashCrypto[tc.index]
					want = [][]any{{binaryHashBytes(t, v[2]), v[0]}}
				}
				referenceRunStep(t, p.ctx, contextualHeld, backend.recorder, referenceSelect(contextual, []string{"sha256", "md5"}, []string{"BYTEA", "TEXT"}, want, tc.arg))
			}
			if err := contextualStmt.Close(); err != nil {
				t.Fatal(err)
			}
			if err := contextualConn.Close(); err != nil {
				t.Fatal(err)
			}
			q := `INSERT INTO hash_dst VALUES(3,sha256('abc')),(4,sha256('\x0'))`
			pos := strings.Index(q, `'\x0'`) + 1
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(q, referenceDiagnostic{Code: "22023", Message: "invalid hexadecimal data: odd number of digits", Position: referencePosition(pos)})
				s.errorStage = "execute"
				if prepared {
					s = referencePrepared(s)
					s.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT id,d FROM hash_dst ORDER BY id", []string{"id", "d"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), binaryHashBytes(t, binaryHashCrypto[1][2])}, {int64(2), binaryHashBytes(t, binaryHashCrypto[1][2])}}))
			}
			// PostgreSQL's cancellation watcher on an already borrowed Conn can race a
			// tiny query. The public pool-acquisition boundary rejects a precanceled
			// context deterministically; provider units separately prove mid-hash checks.
			stmt, err := backend.db.PrepareContext(p.ctx, "SELECT sha256($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			write, err := backend.db.PrepareContext(p.ctx, "INSERT INTO hash_dst VALUES(99,sha256($1))")
			if err != nil {
				t.Fatal(err)
			}
			defer write.Close()
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := backend.db.QueryContext(canceled, "SELECT sha256($1)", []byte("abc"))
			if rows != nil {
				rows.Close()
				t.Fatal("canceled direct query exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("direct cancellation", err)
			}
			rows, err = stmt.QueryContext(canceled, []byte("abc"))
			if rows != nil {
				rows.Close()
				t.Fatal("canceled prepared query exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("prepared cancellation", err)
			}
			if _, err := backend.db.ExecContext(canceled, "INSERT INTO hash_dst VALUES(99,sha256($1))", []byte("abc")); !errors.Is(err, context.Canceled) {
				t.Fatal("write cancellation", err)
			}
			if _, err := write.ExecContext(canceled, []byte("abc")); !errors.Is(err, context.Canceled) {
				t.Fatal("prepared write cancellation", err)
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT count(*) FROM hash_dst", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT sha256('abc')", []string{"sha256"}, []string{"BYTEA"}, [][]any{{binaryHashBytes(t, binaryHashCrypto[1][2])}}))
		})
	}
}

type binaryHashFailingReader struct{ err error }

func (r binaryHashFailingReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceBinaryHashControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			steps := []referenceStep{
				referenceExec("CREATE TABLE hash_saved(id INTEGER,v BYTEA,CONSTRAINT hash_valid CHECK(crc32(v)=891568578))", 0),
				referenceExec("INSERT INTO hash_saved VALUES(1,'abc'),(2,NULL)", 2),
				referenceExec("CREATE VIEW hash_view AS SELECT id,crc32(v),crc32c(v),md5(v),sha224(v),sha256(v),sha384(v),sha512(v) FROM hash_saved", 0),
				referenceExec("CREATE TABLE hash_stored(d BYTEA)", 0),
				referenceExec("INSERT INTO hash_stored VALUES(sha256('abc'))", 1),
				referenceExec("CREATE TABLE hash_raw(id INTEGER,v BYTEA)", 0),
				referenceExec(`INSERT INTO hash_raw VALUES(1,''),(2,'\x00'),(3,'\x0080ff')`, 3),
			}
			for _, s := range steps {
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			assertSaved := func() {
				t.Helper()
				row := append([]any{int64(1)}, binaryHashRow(t, 1, 891568578, 910901175)...)
				nullRow := []any{int64(2), nil, nil, nil, nil, nil, nil, nil}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT * FROM hash_view ORDER BY id", append([]string{"id"}, binaryHashNames...), append([]string{"INT4"}, binaryHashTypes...), [][]any{row, nullRow}))
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(referenceSelect("SELECT d FROM hash_stored", []string{"d"}, []string{"BYTEA"}, [][]any{{binaryHashBytes(t, binaryHashCrypto[1][2])}})))
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT id,v FROM hash_saved ORDER BY id", []string{"id", "v"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "abc"}, {int64(2), nil}}))
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT id,v FROM hash_raw ORDER BY id", []string{"id", "v"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), ""}, {int64(2), "\x00"}, {int64(3), "\x00\x80\xff"}}))
				rawDigests := [][]any{binaryHashRow(t, 0, 0, 0), binaryHashRow(t, 2, 3523407757, 1383945041), binaryHashRow(t, 3, 3921718996, 920259282)}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT "+binaryHashProjection+" FROM hash_raw ORDER BY id", binaryHashNames, binaryHashTypes, rawDigests))
				s := referenceImplicitError("INSERT INTO hash_saved VALUES(3,'bad')", referenceDiagnostic{Code: "23514", Message: `new row for relation "hash_saved" violates check constraint "hash_valid"`, Detail: `Failing row contains (3, \x626164).`, Schema: "public", Table: "hash_saved", Constraint: "hash_valid"})
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			assertSaved()
			deparse := referenceSelect("SELECT pg_get_constraintdef(oid,false),pg_get_constraintdef(oid,true) FROM pg_constraint WHERE conname='hash_valid'", []string{"pg_get_constraintdef", "pg_get_constraintdef"}, []string{"TEXT", "TEXT"}, [][]any{{"CHECK ((crc32(v) = 891568578))", "CHECK (crc32(v) = 891568578)"}})
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, deparse)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(deparse))
			if backend.pg {
				return
			} // Native controls have no PostgreSQL equivalent.
			held, err := backend.db.QueryContext(p.ctx, "SELECT d FROM hash_stored")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			columns, err := held.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			assertSaved()
			if !held.Next() {
				t.Fatal("retained digest row", held.Err())
			}
			var retained []byte
			if err := held.Scan(&retained); err != nil || string(retained) != binaryHashBytes(t, binaryHashCrypto[1][2]) {
				t.Fatal(retained, err)
			}
			retained[0] ^= 255
			if columns[0].DatabaseTypeName() != "BYTEA" {
				t.Fatal("retained metadata")
			}
			if err := held.Close(); err != nil {
				t.Fatal(err)
			}
			assertSaved()
			for _, name := range binaryHashNames {
				// CRC/SHA unique unknown inference; MD5 gets known BYTEA from SHA.
				arg := "'abc'"
				if name == "md5" {
					arg = "sha256('abc')"
				}
				i := 0
				for binaryHashNames[i] != name {
					i++
				}
				script := "CREATE TABLE hash_restore(v " + binaryHashTypes[i] + "); INSERT INTO hash_restore VALUES(" + name + "(" + arg + "))"
				if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
					t.Fatal(name, err)
				}
				want := binaryHashRow(t, 1, 891568578, 910901175)[i]
				if name == "md5" {
					want = "6758bdecb9389db85fa950e7fa2fce82"
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v FROM hash_restore", []string{"v"}, []string{binaryHashTypes[i]}, [][]any{{want}}))
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			assertSaved()
			script := "CREATE TABLE replacement(d BYTEA); INSERT INTO replacement VALUES(sha256('abc')); INSERT INTO replacement VALUES(sha256(sha224($1)))"
			err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(script))
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "parameters are not allowed in restore", Position: referencePosition(strings.Index(script, "$1") + 1)})
			assertSaved()
			binaryHashDiagnostic(t, p, backend.db, "SELECT d FROM replacement", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: `relation "replacement" does not exist`, Position: "15"})
			script = `CREATE TABLE replacement(d BYTEA); INSERT INTO replacement VALUES(sha256('abc')),(sha256('\x0'))`
			err = stuntgres.Restore(p.ctx, p.native, strings.NewReader(script))
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid hexadecimal data: odd number of digits", Position: referencePosition(strings.Index(script, `'\x0'`) + 1)})
			assertSaved()
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			assertSaved()
			failure := errors.New("binary hash restore reader failure")
			if err := stuntgres.Restore(p.ctx, p.native, binaryHashFailingReader{failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			assertSaved()
		})
	}
}
