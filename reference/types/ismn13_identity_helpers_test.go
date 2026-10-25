package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func ismn13MovedControl(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held fixture.HeldStatement, stamp [4]int64) {
	t.Helper()
	for _, q := range []string{
		"ALTER TYPE ismn13_identity_ns.music SET SCHEMA public",
		"ALTER FUNCTION ismn13_identity_ns.music_in(cstring) SET SCHEMA public",
		"ALTER FUNCTION ismn13_identity_ns.music_out(public.music) SET SCHEMA public",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
	if got := isbnIdentityStamp(t, p, c, "public.music"); got != stamp {
		t.Fatalf("moving ISMN13/codecs changed identity: %v, want %v", got, stamp)
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(held.SQL, []string{"v"}, []string{"TEXT"}, [][]any{{"979-0-1234-5678-5!"}}, []byte("M123456780!")))
	verify := func(pair *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		s := ean13Select(backend, "SELECT v,e FROM ismn13_qualified", []string{"v", "e"}, []string{"ISMN13", "EAN13"}, [][]any{{"979-0-1234-5678-5!", "978-0-12-345678-6"}}, []bool{true, true})
		fixture.RunStep(t, pair.Ctx, conn, backend.Recorder, s)
		fixture.RunStep(t, pair.Ctx, conn, backend.Recorder, fixture.Prepared(s))
		fixture.RunStep(t, pair.Ctx, conn, backend.Recorder, fixture.Select("SELECT typinput='public.music_in(cstring)'::regprocedure::oid AS input_identity,typoutput='public.music_out(public.music)'::regprocedure::oid AS output_identity FROM pg_type WHERE oid='public.music'::regtype::oid", []string{"input_identity", "output_identity"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	}
	verify(p, b, c)
	if b.PG { return }
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	verify(p, b, c)
	target := fixture.NewFixture(t, false, true)
	conn, err := target.Memory.Conn(target.Ctx)
	if err != nil { t.Fatal(err) }
	defer conn.Close()
	tb := fixture.Backend{Recorder: target.MemoryRecord, DB: target.Memory}
	fixture.RunStep(t, target.Ctx, conn, tb.Recorder, fixture.Exec("CREATE TABLE ismn13_move_spacer(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	verify(target, tb, conn)
	if p.Postgres != nil {
		isbnReplay(t, p, dump.String(), nil, func(pg *sql.Conn, pb fixture.Backend) {
			verify(p, pb, pg)
			fixture.RunStep(t, p.Ctx, pg, pb.Recorder, fixture.Exec("DROP SCHEMA ismn13_identity_ns CASCADE", 0))
		})
	}
}
