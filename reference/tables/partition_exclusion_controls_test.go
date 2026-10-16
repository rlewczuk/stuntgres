package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePartitionExclusionControls(t *testing.T) {
	partitionExclusionCase(t, "persistent_graph_and_failed_replacement", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionExclusionTree(run, "")
		run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)'),(2,'10.0.0.129','[3,7)')", 2))
		run(fixture.Exec("ALTER TABLE pe_low ADD CONSTRAINT pe_local EXCLUDE USING gist("+partitionExclusionKeys+") DEFERRABLE INITIALLY DEFERRED", 0))
		run(fixture.Exec("ALTER INDEX pe_low_tenant_during_excl RENAME TO pe_renamed", 0))
		run(fixture.Exec("CREATE TABLE pe_nested(tenant INET,site INET,during INT4RANGE,CONSTRAINT pe_nested_ex EXCLUDE USING gist(tenant inet_ops WITH =,site inet_ops WITH =,during WITH &&)) PARTITION BY RANGE(tenant)", 0))
		run(fixture.Exec("CREATE TABLE pe_mid PARTITION OF pe_nested DEFAULT PARTITION BY RANGE(site)", 0))
		run(fixture.Exec("CREATE TABLE pe_leaf PARTITION OF pe_mid DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pe_nested VALUES('10.0.0.1','192.0.2.1','[1,5)')", 1))
		run(fixture.Exec("ALTER TABLE pe_nested DETACH PARTITION pe_mid", 0))
		check := func(x fixture.Executor, recorder *fixture.Recorder) {
			apply := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, x, recorder, s) }
			apply(partitionExclusionIDs(1, 2))
			apply(fixture.Select("SELECT t.relname,c.conname,p.conname AS parent,c.conislocal,c.condeferrable,c.condeferred FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid LEFT JOIN pg_constraint p ON p.oid=c.conparentid WHERE t.relname IN ('pe_root','pe_low','pe_other','pe_nested','pe_mid','pe_leaf') AND c.contype='x' ORDER BY t.relname,c.conname", []string{"relname", "conname", "parent", "conislocal", "condeferrable", "condeferred"}, []string{"NAME", "NAME", "NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{"pe_leaf", "pe_leaf_tenant_site_during_excl", "pe_mid_tenant_site_during_excl", false, false, false}, {"pe_low", "pe_local", nil, true, true, true}, {"pe_low", "pe_renamed", "pe_ex", false, false, false}, {"pe_mid", "pe_mid_tenant_site_during_excl", nil, true, false, false}, {"pe_nested", "pe_nested_ex", nil, true, false, false}, {"pe_other", "pe_other_tenant_during_excl", "pe_ex", false, false, false}, {"pe_root", "pe_ex", nil, true, false, false}}))
			apply(partitionExclusionConflict("INSERT INTO pe_root VALUES(3,'10.0.0.129','[4,8)')", "pe_other", "pe_other_tenant_during_excl", "10.0.0.129, [4,8)", "10.0.0.129, [3,7)"))
			apply(fixture.Select("SELECT tenant,site,during FROM pe_mid", []string{"tenant", "site", "during"}, []string{"INET", "INET", "INT4RANGE"}, [][]any{{"10.0.0.1", "192.0.2.1", "[1,5)"}}))
		}
		check(b.DB, b.Recorder)
		if b.PG {
			return // Native controls have no server API analogue.
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		retained, err := b.DB.QueryContext(p.Ctx, "SELECT id,tenant,during FROM pe_root ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		referenceRangeMetadata(false, []string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"}, []bool{true, true, true})(t, retained)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(b.DB, b.Recorder)
		redump := func() {
			var got bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &got); err != nil || !bytes.Equal(got.Bytes(), dump.Bytes()) {
				t.Fatal("replacement changed persistent graph/data", err, got.String())
			}
		}
		redump()
		if p.Postgres != nil {
			oracle, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
					if _, err := oracle.ExecContext(ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			if _, err := oracle.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual emitted exclusion hierarchy replay", err)
			}
			check(oracle, p.PostgresRecord)
		}
		c := fixture.ConcurrentIndexConn(t, p, b)
		fixture.ExclusionReferenceRun(t, p, b, c, fixture.Exec("CREATE TEMP TABLE pe_retained(id INT)", 0))
		fixture.ExclusionReferenceRun(t, p, b, c, fixture.Exec("INSERT INTO pe_retained VALUES(9)", 1))
		// A failed suffix must not publish even a valid imported prefix.
		script := dump.String() + "INSERT INTO pe_root VALUES(3,'10.0.0.129','[4,8)');"
		want := fixture.ExclusionReferenceConflict("pe_other", "pe_other_tenant_during_excl", "tenant, during", "10.0.0.129, [4,8)", "10.0.0.129, [3,7)")
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &want)
		redump()
		check(b.DB, b.Recorder)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Restore got %v", err)
		}
		redump()
		check(b.DB, b.Recorder)
		fixture.ExclusionReferenceRun(t, p, b, c, fixture.Select("SELECT id FROM pe_retained", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
		for _, want := range []struct {
			id             int64
			tenant, during string
		}{{1, "10.0.0.1", "[1,5)"}, {2, "10.0.0.129", "[3,7)"}} {
			if !retained.Next() {
				t.Fatal("retained cursor lost row", retained.Err())
			}
			var id int64
			var tenant, during string
			if err := retained.Scan(&id, &tenant, &during); err != nil || id != want.id || tenant != want.tenant || during != want.during {
				t.Fatal("retained cursor changed", id, tenant, during, err)
			}
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained cursor tail", retained.Err())
		}
		run(fixture.Prepared(fixture.Exec("INSERT INTO pe_root VALUES($1,$2,$3)", 1, int64(3), "10.0.0.3", "[3,7)")))
		run(partitionExclusionIDs(1, 2, 3))
	})
}

func TestReferencePartitionExclusionFeatureSmoke(t *testing.T) {
	partitionExclusionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.ExclusionReferenceRun(t, p, b, c, s) }
		partitionExclusionTree(run, "")
		run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)')", 1))
		run(fixture.Prepared(partitionExclusionConflict("INSERT INTO pe_root VALUES($1,$2,$3)", "pe_low", "pe_low_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)", int64(2), "10.0.0.1", "[3,7)")))
		run(fixture.Exec("INSERT INTO pe_root VALUES(3,'10.0.0.2','[3,7)'),(4,'10.0.0.129','[3,7)')", 2))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO pe_root VALUES(5,'10.0.0.1','[5,8)')", 1))
		run(fixture.Exec("ROLLBACK", 0))
		run(partitionExclusionIDs(1, 3, 4))
		run(partitionExclusionGraph())
	})
}
