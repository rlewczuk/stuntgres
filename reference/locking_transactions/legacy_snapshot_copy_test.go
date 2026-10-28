package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLegacySnapshotCopy(t *testing.T) {
	legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE legacy_copy(id integer,s txid_snapshot)", 0))
		db := p.Native
		if b.PG {
			db = p.CopyPostgres
		}
		stream, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		result, err := fixture.CopyFrom(p.Ctx, stream, b.PG, "COPY legacy_copy(id,s) FROM STDIN", strings.NewReader("1\t12:16:14,14\n2\t\\N\n3\t1:18446744073709551615:3\n"))
		if err != nil || result.Count != 3 || result.CommandTag != "COPY 3" {
			t.Fatal("typed COPY import", result, err)
		}
		identityBoth(t, p, b, b.DB, fixture.Select("SELECT id,s::text AS s,txid_snapshot_xmax(s) AS hi,pg_typeof(s)::text AS kind FROM legacy_copy ORDER BY id", []string{"id", "s", "hi", "kind"}, []string{"INT4", "TEXT", "INT8", "TEXT"}, [][]any{{int64(1), "12:16:14", int64(16), "txid_snapshot"}, {int64(2), nil, nil, "txid_snapshot"}, {int64(3), "1:18446744073709551615:3", int64(-1), "txid_snapshot"}}))
	})
}
