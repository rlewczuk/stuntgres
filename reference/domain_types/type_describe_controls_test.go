package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTypeDescribeControls(t *testing.T) {
	typeDescribeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		typeDescribeGraph(run)
		typeDescribeConsumersSetup(run)
		typeDescribeFreezeUnknown(t, p, c, run, "unknown")
		run(fixture.Exec("CREATE TABLE public.td_aliases(id integer,r regtype)", 0))
		run(fixture.Exec("INSERT INTO public.td_aliases VALUES(1,'integer'),(2,'td.outer_domain'),(3,NULL)", 3))
		run(fixture.Exec("CREATE TABLE public.td_recursive(a regtype[])", 0))
		run(fixture.Exec("INSERT INTO public.td_recursive VALUES('{integer,td.outer_domain,NULL}')", 1))
		proof := func(run func(fixture.Step)) {
			typeDescribeGraphProof(run)
			typeDescribeConsumersProof(run)
			typeDescribeUnknownViewProof(run, "public.td_unknown_frozen", "unknown")
			typeDescribeUnknownSourcesProof(run, "public.td_unknown_sources_frozen", "unknown")
			typeDescribePseudoViewProof(run, "public.td_pseudo_frozen", "unknown")
			typeDescribeBoth(run, fixture.Select("SELECT id,r::oid=23::oid AS builtin,r::oid='td.outer_domain'::regtype::oid AS nominal,r IS NULL AS missing FROM public.td_aliases ORDER BY id", []string{"id", "builtin", "nominal", "missing"}, []string{"INT4", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, false, false}, {int64(2), false, true, false}, {int64(3), nil, nil, true}}))
			typeDescribeBoth(run, fixture.Select("SELECT a,pg_typeof(a)::oid='regtype[]'::regtype::oid AS identity FROM public.td_recursive", []string{"a", "identity"}, []string{"_REGTYPE", "BOOL"}, [][]any{{"{integer,td.outer_domain,NULL}", true}}))
		}
		proof(run)
		if b.PG {
			return
		} // Native controls are a phase, never a second SQL scenario.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		proof(run)
		oldID := typeDescribeOID(t, p.Ctx, c, "td.outer_domain")
		old, err := b.DB.QueryContext(p.Ctx, "SELECT pg_typeof(NULL::td.outer_domain) AS identity,format_type('td.outer_domain'::regtype::oid,NULL) AS label")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		proof(run)
		fixture.RegtypeAssertRowsWithMetadata(t, old, [][]any{{"td.outer_domain", "td.outer_domain"}}, false, func(t *testing.T, rows *sql.Rows) {
			fixture.RegtypeFamilyMetadata(t, rows, false, []string{"REGTYPE", "TEXT"})
		})
		if metadata[0].DatabaseTypeName() != "REGTYPE" || metadata[1].DatabaseTypeName() != "TEXT" {
			t.Fatal("retained descriptors", metadata)
		}
		var first, second bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &first); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Dump(p.Ctx, p.Native, &second); err != nil {
			t.Fatal(err)
		}
		if first.String() != second.String() {
			t.Fatal("unchanged state redump differs")
		}
		target := fixture.NewFixture(t, false, true)
		targetConn, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer targetConn.Close()
		targetRun := func(s fixture.Step) { fixture.RegtypeRunStep(t, target.Ctx, targetConn, target.MemoryRecord, s) }
		// Retained TEMP objects occupy destination identities across replacement;
		// permanent setup would be discarded by Restore's isolated staging.
		for i := range 8 {
			targetRun(fixture.Exec(fmt.Sprintf("CREATE TEMP TABLE td_displacement_%d(v integer)", i), 0))
		}
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		newID := typeDescribeOID(t, target.Ctx, targetConn, "td.outer_domain")
		if oldID == newID {
			t.Fatalf("foreign restore did not displace user OID: %d", oldID)
		}
		proof(targetRun)
		targetRun(fixture.Exec("CREATE TABLE public.td_sentinel(v integer)", 0))
		targetRun(fixture.Exec("INSERT INTO public.td_sentinel VALUES(42)", 1))
		unchanged := func() {
			targetRun(fixture.Select("SELECT v FROM public.td_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			targetRun(fixture.Select("SELECT count(*) AS n FROM pg_namespace WHERE nspname='staged'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			proof(targetRun)
		}
		for _, bad := range []struct{ sql, state string }{
			{dump.String() + "\nSELECT (", "42601"},
			{"CREATE SCHEMA staged; CREATE DOMAIN staged.d AS integer; CREATE TABLE staged.t(v staged.d); INSERT INTO staged.t VALUES($1);", "42P02"},
		} {
			fixture.State(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad.sql)), bad.state)
			unchanged()
		}
		ioerr := errors.New("type describe control I/O")
		if err := stuntgres.Restore(target.Ctx, target.Native, io.MultiReader(strings.NewReader(dump.String()), fixture.CatalogControlFailingReader{Err: ioerr})); !errors.Is(err, ioerr) {
			t.Fatalf("reader error=%v", err)
		}
		unchanged()
		if err := stuntgres.Dump(target.Ctx, target.Native, fixture.CatalogControlFailingWriter{Err: ioerr}); !errors.Is(err, ioerr) {
			t.Fatalf("writer error=%v", err)
		}
		unchanged()
		ctx, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore=%v", err)
		}
		unchanged()
	})
}

func typeDescribeOID(t *testing.T, ctx context.Context, x fixture.Executor, name string) int64 {
	t.Helper()
	rows, err := x.QueryContext(ctx, "SELECT $1::regtype::oid::bigint", name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing identity", rows.Err())
	}
	var id int64
	if err := rows.Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Fatal("invalid allocated identity", id)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("identity rows", rows.Err())
	}
	return id
}
