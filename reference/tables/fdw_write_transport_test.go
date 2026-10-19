package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwWritePostgresTrigger(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	// A genuine remote procedural trigger; native tests instead exercise an
	// explicitly different SQL-provider rewrite, not native trigger support.
	run(fixture.Exec("CREATE FUNCTION fdw_source.rewrite_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id=41 THEN RETURN NULL; END IF; NEW.label := 'actually-stored'; RETURN NEW; END $$", 0))
	run(fixture.Exec("CREATE TRIGGER fdw_remote_rewrite BEFORE INSERT OR UPDATE ON fdw_source.data FOR EACH ROW EXECUTE FUNCTION fdw_source.rewrite_write()", 0))
	run(fdwRows("INSERT INTO fdw_data VALUES(40,'proposal',true) RETURNING id,label,flag", [][]any{{int64(40), "actually-stored", true}}))
	run(fdwRows("INSERT INTO fdw_data VALUES(41,'suppressed',true) RETURNING id,label,flag", nil))
	run(fixture.Exec("INSERT INTO fdw_data VALUES(41,'suppressed',true)", 0))
	run(fdwRows("UPDATE fdw_data SET label='proposal-again' WHERE id=40 RETURNING id,label,flag", [][]any{{int64(40), "actually-stored", true}}))
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id>=40 ORDER BY id", [][]any{{int64(40), "actually-stored", true}}))
}

type fdwCopyReadFailure struct {
	sent bool
	err  error
}

func (r *fdwCopyReadFailure) Read(p []byte) (int, error) {
	if r.sent {
		return 0, r.err
	}
	r.sent = true
	return copy(p, "90\treader-prefix\tt\n"), nil
}
func fdwWriteCopyFormats(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, stream *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	fdwSourceExec(t, p, b, r, run, "CREATE TABLE fdw_source.copy_defaults(id integer,label text,flag boolean)", 0)
	run(fixture.Exec("CREATE FOREIGN TABLE fdw_copy_defaults(id integer DEFAULT 70,label text DEFAULT 'fallback',flag boolean DEFAULT true) SERVER fdw_live OPTIONS(schema_name 'fdw_source',table_name 'copy_defaults')", 0))
	for _, tc := range []struct {
		q, input string
		count    int64
	}{
		{"COPY fdw_copy_defaults(label) FROM STDIN", "first\n\\N\n", 2},
		{"COPY fdw_copy_defaults(id,label,flag) FROM STDIN WITH(FORMAT csv)", "71,\"csv,label\",false\n72,,true\n73,\"\",false\n74,last,true\n75,remainder,false\n", 5},
	} {
		result, err := fixture.CopyFrom(p.Ctx, stream, b.PG, tc.q, strings.NewReader(tc.input))
		if err != nil || result != (stuntgres.CopyResult{Count: tc.count, CommandTag: fmt.Sprintf("COPY %d", tc.count)}) {
			t.Fatal(result, err)
		}
	}
	// Literal PostgreSQL binary framing: one int4/text/bool row, then trailer.
	var payload bytes.Buffer
	payload.WriteString("PGCOPY\n\xff\r\n\x00")
	for _, v := range []int32{0, 0} {
		_ = binary.Write(&payload, binary.BigEndian, v)
	}
	_ = binary.Write(&payload, binary.BigEndian, int16(3))
	_ = binary.Write(&payload, binary.BigEndian, int32(4))
	_ = binary.Write(&payload, binary.BigEndian, int32(76))
	_ = binary.Write(&payload, binary.BigEndian, int32(6))
	payload.WriteString("binary")
	_ = binary.Write(&payload, binary.BigEndian, int32(1))
	payload.WriteByte(1)
	_ = binary.Write(&payload, binary.BigEndian, int16(-1))
	result, err := fixture.CopyFrom(p.Ctx, stream, b.PG, "COPY fdw_copy_defaults FROM STDIN WITH(FORMAT binary)", &payload)
	if err != nil || result != (stuntgres.CopyResult{Count: 1, CommandTag: "COPY 1"}) {
		t.Fatal(result, err)
	}
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.copy_defaults ORDER BY id,label NULLS FIRST", [][]any{{int64(70), nil, true}, {int64(70), "first", true}, {int64(71), "csv,label", false}, {int64(72), nil, true}, {int64(73), "", false}, {int64(74), "last", true}, {int64(75), "remainder", false}, {int64(76), "binary", true}}))
	failure := errors.New("COPY reader terminal failure")
	result, err = fixture.CopyFrom(p.Ctx, stream, b.PG, "COPY fdw_data FROM STDIN", &fdwCopyReadFailure{err: failure})
	if err == nil || result != (stuntgres.CopyResult{}) {
		t.Fatal("reader failure", result, err)
	}
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=90", nil))
	if b.PG {
		return
	}
	w := r.writer
	ordinary := w.modifies
	begins, ends := w.copyBegins, w.copyEnds
	w.writeHook = func(ctx context.Context, name string) error {
		if name == "EndForeignInsert" {
			return failure
		}
		return nil
	}
	result, err = fixture.CopyFrom(p.Ctx, stream, false, "COPY fdw_data FROM STDIN", strings.NewReader("91\tend-fails\tt\n"))
	w.writeHook = nil
	if !errors.Is(err, failure) || result != (stuntgres.CopyResult{}) || w.copyBegins-begins != 1 || w.copyEnds-ends != 1 || w.modifies != ordinary {
		t.Fatal("COPY lifecycle", result, err, w.copyBegins-begins, w.copyEnds-ends, w.modifies-ordinary)
	}
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=91", nil))
}

func fdwWriteSessionClose(t *testing.T, p *fixture.Pair, b fixture.Backend, r *fdwSQLReader) {
	t.Helper()
	pool := fdwRecordedPool(t, p.NativeConnector)
	pool.SetMaxIdleConns(0)
	conn, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"BEGIN", "INSERT INTO fdw_data VALUES(55,'closed-session',true)"} {
		if _, err := conn.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=55", nil))
}

var _ io.Reader = (*fdwCopyReadFailure)(nil)
