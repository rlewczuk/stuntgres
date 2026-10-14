package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRegProcMaterializedPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "regproc_materialized_store")
	fixture.RoleReferenceCleanup(t, []string{"regproc_materialized_role"})
	var emitted []byte
	regprocScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, step) }
		for _, q := range []string{
			"CREATE ROLE regproc_materialized_role",
			"CREATE SCHEMA regproc_materialized_store",
			"CREATE TYPE regproc_materialized_store.mixed AS (p regprocedure,o regoperator,t regtype,r regrole)",
			"CREATE TABLE regproc_materialized_store.source(id integer)",
			`CREATE VIEW regproc_materialized_store.inner_source AS SELECT id,'("abs(integer)","+(integer,integer)",integer,regproc_materialized_role)'::regproc_materialized_store.mixed AS c FROM regproc_materialized_store.source`,
			"CREATE VIEW regproc_materialized_store.outer_source AS SELECT id,c FROM regproc_materialized_store.inner_source",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO regproc_materialized_store.source VALUES(1)", 1))
		// Both definitions are admitted opaque composites, not forbidden scalar
		// REGROLE constants or unsupported composite arrays. The direct consumer
		// exercises refresh itself; the nested source exercises recursive capture.
		run(fixture.Exec(`CREATE MATERIALIZED VIEW regproc_materialized_store.direct_mv AS SELECT id,'("abs(integer)","+(integer,integer)",integer,regproc_materialized_role)'::regproc_materialized_store.mixed AS c FROM regproc_materialized_store.source`, 1))
		run(fixture.Exec("CREATE MATERIALIZED VIEW regproc_materialized_store.nested_mv AS SELECT id,c FROM regproc_materialized_store.outer_source", 1))
		regprocMaterializedCheck(t, p, c, r, []int64{1})
		// Dump must preserve stale materialized rows separately from the defining
		// source. Subsequent refresh must actually consume the frozen definition.
		run(fixture.Exec("UPDATE regproc_materialized_store.source SET id=2", 1))
		regprocMaterializedCheck(t, p, c, r, []int64{1})

		if b.PG {
			run(fixture.Exec("DROP SCHEMA regproc_materialized_store CASCADE", 0))
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("PostgreSQL rejected actual materialized routine Dump", err)
			}
			// Native controls retain saved populations; emitted SQL creates
			// PostgreSQL materialized definitions WITH NO DATA instead.
			for _, name := range []string{"direct_mv", "nested_mv"} {
				step := fixture.MaterializedViewUnpopulated(name)
				step.Query = "SELECT * FROM regproc_materialized_store." + name
				run(step)
			}
			regprocMaterializedRefresh(t, p, c, r)
			return
		}

		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if dump.Len() == 0 {
			t.Fatal("empty materialized routine Dump")
		}
		emitted = bytes.Clone(dump.Bytes())
		retained, err := c.QueryContext(p.Ctx, "SELECT id,(c).p AS p,(c).o AS o,(c).t AS t,(c).r AS r FROM regproc_materialized_store.direct_mv ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		metadata, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		regprocMaterializedRetainedMetadata(t, metadata)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("materialized routine Restore", err)
		}
		regprocMaterializedCheck(t, p, c, r, []int64{1})
		regprocMaterializedRefresh(t, p, c, r)
		currentMetadata, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		regprocMaterializedRetainedMetadata(t, currentMetadata)
		if !retained.Next() {
			t.Fatal("retained materialized row absent", retained.Err())
		}
		var id int64
		var routine, operator, typ, role string
		if err := retained.Scan(&id, &routine, &operator, &typ, &role); err != nil {
			t.Fatal(err)
		}
		if id != 1 || routine != "abs(integer)" || operator != "+(integer,integer)" || typ != "integer" || role != "regproc_materialized_role" || retained.Next() || retained.Err() != nil {
			t.Fatal("retained materialized output changed across replacement/refresh", id, routine, operator, typ, role, retained.Err())
		}
	})
}

func regprocMaterializedRefresh(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	for _, name := range []string{"direct_mv", "nested_mv"} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("REFRESH MATERIALIZED VIEW regproc_materialized_store."+name, 0))
	}
	regprocMaterializedCheck(t, p, c, r, []int64{2})
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO regproc_materialized_store.source VALUES(3)", 1))
	// Fresh reads must still see the old population until the next refresh.
	regprocMaterializedCheck(t, p, c, r, []int64{2})
	for _, name := range []string{"direct_mv", "nested_mv"} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(fixture.Exec("REFRESH MATERIALIZED VIEW regproc_materialized_store."+name, 0)))
	}
	regprocMaterializedCheck(t, p, c, r, []int64{2, 3})
}

func regprocMaterializedCheck(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, ids []int64) {
	t.Helper()
	values := make([][]any, len(ids))
	identities := make([][]any, len(ids))
	for i, id := range ids {
		values[i] = []any{id, "abs(integer)", "+(integer,integer)", "integer", "regproc_materialized_role"}
		identities[i] = []any{id, int64(1397), int64(551), int64(23), true}
	}
	for _, name := range []string{"direct_mv", "nested_mv"} {
		regprocMixedOperatorBoth(t, p, x, r, fixture.Select(
			"SELECT id,(c).p AS p,(c).o AS o,(c).t AS t,(c).r::text AS r FROM regproc_materialized_store."+name+" ORDER BY id",
			[]string{"id", "p", "o", "t", "r"}, []string{"INT4", "REGPROCEDURE", "REGOPERATOR", "REGTYPE", "TEXT"}, values,
		))
		regprocMixedOperatorBoth(t, p, x, r, fixture.Select(
			"SELECT id,(c).p::oid::bigint AS p,(c).o::oid::bigint AS o,(c).t::oid::bigint AS t,(c).r::oid='regproc_materialized_role'::regrole::oid AS named_role FROM regproc_materialized_store."+name+" ORDER BY id",
			[]string{"id", "p", "o", "t", "named_role"}, []string{"INT4", "INT8", "INT8", "INT8", "BOOL"}, identities,
		))
	}
}

func regprocMaterializedRetainedMetadata(t *testing.T, columns []*sql.ColumnType) {
	t.Helper()
	if len(columns) != 5 {
		t.Fatal("retained materialized metadata width", columns)
	}
	for i, family := range []string{"INT4", "REGPROCEDURE", "REGOPERATOR", "REGTYPE", "REGROLE"} {
		wantScan := reflect.TypeFor[string]()
		if i == 0 {
			wantScan = reflect.TypeFor[int64]()
		}
		if columns[i].Name() != []string{"id", "p", "o", "t", "r"}[i] || columns[i].DatabaseTypeName() != family || columns[i].ScanType() != wantScan {
			t.Fatal("retained materialized metadata", i, columns[i])
		}
	}
}
