package stuntgres_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestAlterTypeStandaloneDefaultComposition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			ctx := context.Background()
			exec := func(q string, count int64) { tableCoreExec(t, db, prepared, q, count) }
			check := func(typ string, want [][]any) {
				alterTypePublicRows(t, db, "SELECT c FROM t ORDER BY id", []string{"c"}, []string{typ}, want)
			}
			exec("CREATE TABLE t(id INT,c TEXT DEFAULT '7')", 0)
			exec("INSERT INTO t VALUES(1,'8')", 1)

			// Each utility is a separate autocommit statement, not an action list.
			exec("ALTER TABLE t ALTER COLUMN c DROP DEFAULT", 0)
			check("TEXT", [][]any{{"8"}})
			exec("ALTER TABLE t ALTER COLUMN c TYPE INT USING c::int", 0)
			check("INT4", [][]any{{int64(8)}})
			exec("ALTER TABLE t ALTER COLUMN c SET DEFAULT 7", 0)
			check("INT4", [][]any{{int64(8)}})
			exec("INSERT INTO t VALUES(2,DEFAULT)", 1)
			want := [][]any{{int64(8)}, {int64(7)}}
			check("INT4", want)

			var dump bytes.Buffer
			if err := stuntgres.Dump(ctx, db, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "ALTER TABLE") {
				t.Fatal("Dump contains utility history", dump.String())
			}
			if err := stuntgres.Reset(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			var again bytes.Buffer
			if err := stuntgres.Dump(ctx, db, &again); err != nil || !bytes.Equal(again.Bytes(), dump.Bytes()) {
				t.Fatal("canonical redump", err, again.String(), dump.String())
			}
			check("INT4", want)
			exec("INSERT INTO t(id) VALUES(3)", 1)
			want = append(want, []any{int64(7)})
			check("INT4", want)

			// The standalone-installed default is independently coerced by TYPE.
			exec("ALTER TABLE t ALTER c TYPE BIGINT USING c+1", 0)
			check("INT8", [][]any{{int64(9)}, {int64(8)}, {int64(8)}})
			exec("INSERT INTO t VALUES(4,DEFAULT)", 1)
			check("INT8", [][]any{{int64(9)}, {int64(8)}, {int64(8)}, {int64(7)}})
		})
	}
}
