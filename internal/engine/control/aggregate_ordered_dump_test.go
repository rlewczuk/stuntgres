package control

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/dump"
)

func TestAggregateOrderedDumpAuthority(t *testing.T) {
	const definition = "CREATE AGGREGATE public.ua_rank(VARIADIC pg_catalog.\"any\" ORDER BY VARIADIC pg_catalog.\"any\") (SFUNC=pg_catalog.ordered_set_transition_multi, STYPE=internal, FINALFUNC=pg_catalog.rank_final, FINALFUNC_EXTRA, HYPOTHETICAL);\n"
	const ownership = "ALTER AGGREGATE public.ua_rank(VARIADIC pg_catalog.\"any\" ORDER BY VARIADIC pg_catalog.\"any\") OWNER TO \"Owned 'role\";\n"
	declaration := dump.Statement{Owner: "Owned 'role", SQL: definition, FollowingSQL: ownership, PrivilegedCreation: true}
	var out bytes.Buffer
	w := dumpWriter{ctx: context.Background(), dst: &out, currentOwner: "prior_owner"}
	if err := emitDumpStatement(&dumpScript{Capture: &Capture{}}, &w, 94001, declaration); err != nil { t.Fatal(err) }
	const restored = "SET SESSION AUTHORIZATION 'Owned ''role';\n"
	want := "SET SESSION AUTHORIZATION DEFAULT;\n" + definition + ownership + restored
	if out.String() != want || w.currentOwner != declaration.Owner {
		t.Fatalf("privileged creation/transfer/authority restoration = %q owner=%q, want %q", out.String(), w.currentOwner, want)
	}
	// A following object must not inherit privileged creation authority, even
	// when its owner is the same as the transferred aggregate's owner.
	const dependent = "CREATE VIEW public.ua_view AS SELECT 1;\n"
	if err := emitDumpStatement(&dumpScript{Capture: &Capture{}}, &w, 94002, dump.Statement{Owner: declaration.Owner, SQL: dependent}); err != nil { t.Fatal(err) }
	if out.String() != want+dependent || w.currentOwner != declaration.Owner {
		t.Fatalf("following declaration authority = %q owner=%q", out.String(), w.currentOwner)
	}
}

func TestAggregateOrderedDumpAuthorityCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	w := dumpWriter{ctx: ctx, dst: &out, currentOwner: "prior_owner"}
	err := emitDumpStatement(&dumpScript{Capture: &Capture{}}, &w, 94001, dump.Statement{Owner: "new_owner", SQL: "CREATE AGGREGATE public.ua_rank(VARIADIC pg_catalog.\"any\" ORDER BY VARIADIC pg_catalog.\"any\") (SFUNC=pg_catalog.ordered_set_transition_multi, STYPE=internal, FINALFUNC=pg_catalog.rank_final, FINALFUNC_EXTRA, HYPOTHETICAL);\n", PrivilegedCreation: true})
	if !errors.Is(err, context.Canceled) || out.Len() != 0 || w.currentOwner != "prior_owner" {
		t.Fatalf("cancelled authority switch: err=%v output=%q owner=%q", err, out.String(), w.currentOwner)
	}
}
