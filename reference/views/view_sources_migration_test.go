package reference_test

import (
	"context"
	"reflect"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewCatalogMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT)", 0))
			run(fixture.Exec("CREATE VIEW v AS SELECT id FROM t", 0))
			query := "SELECT oid,relname,relnamespace,relowner,relkind,relnatts,relhasrules FROM pg_catalog.pg_class WHERE relname='v'"
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
			rows, err := b.DB.QueryContext(ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			names, types := []string{"oid", "relname", "relnamespace", "relowner", "relkind", "relnatts", "relhasrules"}, []string{"OID", "NAME", "OID", "OID", "CHAR", "INT2", "BOOL"}
			if !reflect.DeepEqual(columns, names) {
				t.Fatal(columns)
			}
			metadata, err := rows.ColumnTypes()
			if err != nil || len(metadata) != 7 {
				t.Fatal(metadata, err)
			}
			for i, c := range metadata {
				if c.DatabaseTypeName() != types[i] {
					t.Fatal(i, c)
				}
			}
			if !rows.Next() {
				t.Fatal("missing class row", rows.Err())
			}
			// Probe the codec before typed destinations convert pq OID byte payloads.
			var raw [7]any
			if err := rows.Scan(&raw[0], &raw[1], &raw[2], &raw[3], &raw[4], &raw[5], &raw[6]); err != nil {
				t.Fatal(err)
			}
			for i, v := range raw {
				fixture.AssertRawValue(t, b.PG, types[i], v)
			}
			var oid, namespace, owner int64
			var name, kind string
			var n int16
			var rules bool
			if err := rows.Scan(&oid, &name, &namespace, &owner, &kind, &n, &rules); err != nil {
				t.Fatal(err)
			}
			if oid <= 0 || namespace <= 0 || owner <= 0 || name != "v" || kind != "v" || n != 1 || !rules {
				t.Fatal(oid, namespace, owner, name, kind, n, rules)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra class row or error", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			oidValue, nsValue, ownerValue := any(oid), any(namespace), any(owner)
			if b.PG {
				oidValue = strconv.FormatInt(oid, 10)
				nsValue = strconv.FormatInt(namespace, 10)
				ownerValue = strconv.FormatInt(owner, 10)
			}
			fixture.ViewRecorded(t, b.Recorder, query, names, types, [][]any{{oidValue, "v", nsValue, ownerValue, "v", int64(1), true}}, false)
			run(fixture.Select("SELECT rulename,ev_class,ev_type,ev_enabled,is_instead FROM pg_catalog.pg_rewrite WHERE ev_class=$1", []string{"rulename", "ev_class", "ev_type", "ev_enabled", "is_instead"}, []string{"NAME", "OID", "CHAR", "CHAR", "BOOL"}, [][]any{{"_RETURN", oidValue, "1", "O", true}}, oid))
		})
	}
}

func referenceViewGroupedIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend) int64 {
	t.Helper()
	query := "SELECT oid,relnamespace,relowner FROM pg_catalog.pg_class WHERE relname='grouped'"
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
	rows, err := b.DB.QueryContext(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names := []string{"oid", "relnamespace", "relowner"}
	types := []string{"OID", "OID", "OID"}
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, names) {
		t.Fatal(columns, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != 3 {
		t.Fatal(metadata, err)
	}
	for _, c := range metadata {
		if c.DatabaseTypeName() != "OID" {
			t.Fatal(c)
		}
	}
	if !rows.Next() {
		t.Fatal("missing grouped identity", rows.Err())
	}
	var raw [3]any
	if err := rows.Scan(&raw[0], &raw[1], &raw[2]); err != nil {
		t.Fatal(err)
	}
	for _, v := range raw {
		fixture.AssertRawValue(t, b.PG, "OID", v)
	}
	var oid, namespace, owner int64
	if err := rows.Scan(&oid, &namespace, &owner); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || namespace <= 0 || owner <= 0 {
		t.Fatal("invalid live catalog identity", oid, namespace, owner)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra identity or error", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	want := [][]any{{oid, namespace, owner}}
	if b.PG {
		want = [][]any{{strconv.FormatInt(oid, 10), strconv.FormatInt(namespace, 10), strconv.FormatInt(owner, 10)}}
	}
	fixture.ViewRecorded(t, b.Recorder, query, names, types, want, false)
	return oid
}

func TestReferenceViewSourcesAndCatalogLinks(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("SET SESSION AUTHORIZATION postgres", 0), fixture.Exec("CREATE TABLE t(id INT,txt TEXT,visible BOOL)", 0), fixture.Exec("INSERT INTO t VALUES(1,'a',true),(2,'b',false),(3,NULL,true),(3,NULL,true)", 4),
				fixture.Prepared(fixture.Exec("CREATE VIEW grouped(k,n,label) AS SELECT id,COUNT(*),COALESCE(txt,'x') FROM t WHERE visible GROUP BY id,txt HAVING COUNT(*)>1", 0)), fixture.Select("SELECT k,n,label FROM grouped", []string{"k", "n", "label"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(3), int64(2), "x"}}),
				fixture.Exec("CREATE TABLE destination(k INT,n BIGINT,label TEXT)", 0), fixture.Prepared(fixture.Exec("INSERT INTO destination SELECT k,n,label FROM grouped", 1)), fixture.Select("SELECT k,n,label FROM destination", []string{"k", "n", "label"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(3), int64(2), "x"}}),
				fixture.Exec("CREATE VIEW z AS SELECT FROM t", 0), fixture.Exec("CREATE SEQUENCE s", 0), fixture.Exec("CREATE VIEW sequence_state AS SELECT last_value,is_called FROM s", 0), fixture.Prepared(fixture.Exec("CREATE VIEW sequence_call(n) AS SELECT nextval('s') FROM t", 0)),
				fixture.Select("SELECT last_value,is_called FROM sequence_state", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}), fixture.Select("SELECT n FROM sequence_call ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}),
				fixture.Exec("CREATE VIEW provider_view AS SELECT schemaname,viewname FROM pg_catalog.pg_views WHERE viewname='grouped'", 0), fixture.Select("SELECT schemaname,viewname FROM provider_view", []string{"schemaname", "viewname"}, []string{"NAME", "NAME"}, [][]any{{"public", "grouped"}}), fixture.Select("SELECT relkind,relnatts,relhasrules FROM pg_catalog.pg_class WHERE relname='grouped'", []string{"relkind", "relnatts", "relhasrules"}, []string{"CHAR", "INT2", "BOOL"}, [][]any{{"v", int64(3), true}}), fixture.Select("SELECT viewowner FROM pg_catalog.pg_views WHERE viewname='grouped'", []string{"viewowner"}, []string{"NAME"}, [][]any{{"postgres"}}),
			} {
				run(s)
			}
			oid := referenceViewGroupedIdentity(t, p, b)
			run(fixture.Prepared(fixture.Select("SELECT rulename,ev_type,ev_enabled,is_instead FROM pg_catalog.pg_rewrite WHERE ev_class=$1", []string{"rulename", "ev_type", "ev_enabled", "is_instead"}, []string{"NAME", "CHAR", "CHAR", "BOOL"}, [][]any{{"_RETURN", "1", "O", true}}, oid)))
		})
	}
}
