package fixture

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func ExtensionTempExitBarrier(t *testing.T, p *Pair, peer *sql.Conn, r *Recorder, key int64) {
	t.Helper()
	// Server exit completes TEMP cleanup before releasing session advisory locks.
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	RunStep(t, ctx, peer, r, Select("SELECT pg_advisory_lock($1::bigint)", []string{"pg_advisory_lock"}, []string{"VOID"}, [][]any{{""}}, key))
	RunStep(t, p.Ctx, peer, r, Select("SELECT pg_advisory_unlock($1::bigint)", []string{"pg_advisory_unlock"}, []string{"BOOL"}, [][]any{{true}}, key))
}
