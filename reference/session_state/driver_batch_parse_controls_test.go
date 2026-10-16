package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func batchParseControls(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle_parse_control", true: "explicit_parse_control"}[explicit], func(t *testing.T) {
			batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				batchSeed(t, p, b, a)
				if explicit {
					batchExec(t, p, b, a, "BEGIN", 0)
					batchExec(t, p, b, a, "INSERT INTO batch_t VALUES(3)", 1)
				}
				q := "INSERT INTO batch_t VALUES(9); COMMIT; SELECT i FROM batch_t WHERE"
				w := batchDiag("42601", "syntax error at end of input")
				w.Position = fixture.Position(utf8.RuneCountInString(q) + 1)
				batchError(t, p, a, q, w)
				if explicit {
					batchError(t, p, a, "SELECT i FROM batch_t", batchDiag("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
					batchExec(t, p, b, a, "ROLLBACK", 0)
				}
				batchValues(t, p, b, peer, "SELECT i FROM batch_t", 7)
			})
		})
	}
}
