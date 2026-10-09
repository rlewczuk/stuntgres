package reference_test

import (
	"bytes"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceAlterTypeStandaloneDefaultComposition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			// The fixture asserts these exact outcomes on each backend before parity.
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			check := func(typ string, want [][]any) {
				run(referenceSelect("SELECT c FROM t ORDER BY id", []string{"c"}, []string{typ}, want))
			}
			run(referenceExec("CREATE TABLE t(id INT,c TEXT DEFAULT '7')", 0))
			run(referenceExec("INSERT INTO t VALUES(1,'8')", 1))

			// No explicit transaction or combined TYPE/DEFAULT list is used.
			run(referenceExec("ALTER TABLE t ALTER COLUMN c DROP DEFAULT", 0))
			check("TEXT", [][]any{{"8"}})
			run(referenceExec("ALTER TABLE t ALTER COLUMN c TYPE INT USING c::int", 0))
			check("INT4", [][]any{{int64(8)}})
			run(referenceExec("ALTER TABLE t ALTER COLUMN c SET DEFAULT 7", 0))
			check("INT4", [][]any{{int64(8)}})
			run(referenceExec("INSERT INTO t VALUES(2,DEFAULT)", 1))
			want := [][]any{{int64(8)}, {int64(7)}}
			check("INT4", want)

			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "ALTER TABLE") {
				t.Fatal("Dump contains utility history", dump.String())
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			var again bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil || !bytes.Equal(again.Bytes(), dump.Bytes()) {
				t.Fatal("canonical redump", err, again.String(), dump.String())
			}
			check("INT4", want)
			run(referenceExec("INSERT INTO t(id) VALUES(3)", 1))
			want = append(want, []any{int64(7)})
			check("INT4", want)

			// USING changes rows, not the standalone-installed default expression.
			run(referenceExec("ALTER TABLE t ALTER c TYPE BIGINT USING c+1", 0))
			check("INT8", [][]any{{int64(9)}, {int64(8)}, {int64(8)}})
			run(referenceExec("INSERT INTO t VALUES(4,DEFAULT)", 1))
			check("INT8", [][]any{{int64(9)}, {int64(8)}, {int64(8)}, {int64(7)}})
		})
	}
}
