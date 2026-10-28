package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func intaggStored(run func(fixture.Step)) {
	intaggInstall(run)
	intaggInput(run)
	run(fixture.Exec("CREATE TABLE ia_arrays(id int,a integer[])", 0))
	run(fixture.Exec("INSERT INTO ia_arrays VALUES(1,'{2,2,NULL}')", 1))
	run(fixture.Exec("CREATE VIEW ia_aggregate_view AS SELECT ia_addon.int_array_aggregate(v ORDER BY id) FILTER(WHERE k='a') AS a FROM ia_input", 0))
	run(fixture.Exec("CREATE VIEW ia_enum_view AS SELECT v,n FROM ia_arrays,ia_addon.int_array_enum(a) WITH ORDINALITY AS u(v,n)", 0))
	run(fixture.Exec("CREATE VIEW ia_target_view AS SELECT ia_addon.int_array_enum(a) AS v FROM ia_arrays", 0))
}

func intaggStoredChecks(b fixture.Backend) []fixture.Step {
	target := fixture.Select("SELECT v FROM ia_target_view", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}, {nil}})
	target.Bag = true
	return []fixture.Step{
		intaggArray(b, "SELECT a FROM ia_aggregate_view", []string{"a"}, []string{"_INT4"}, [][]any{{"{2,NULL,2}"}}),
		fixture.Select("SELECT v,n FROM ia_enum_view ORDER BY n", []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(1)}, {int64(2), int64(2)}, {nil, int64(3)}}),
		target,
	}
}

func intaggDump(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	var buf bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// Drains actual retained/independent-driver cursors without fabricating recorder
// operations for native control pools. Standard calls still use RunStep above.
func intaggControlRows(t *testing.T, rows *sql.Rows, s fixture.Step) {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, s.Columns) {
		t.Fatal("columns", names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(s.Types) {
		t.Fatal("metadata", meta, err)
	}
	for i, typ := range s.Types {
		if meta[i].DatabaseTypeName() != typ {
			t.Fatal("type", i, meta[i].DatabaseTypeName(), typ)
		}
	}
	var got [][]any
	for rows.Next() {
		values := make([]any, len(s.Columns))
		ptrs := make([]any, len(values))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i := range values {
			fixture.AssertRawValue(t, false, s.Types[i], values[i])
			values[i] = fixture.Normalize(values[i])
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := fixture.CompareRows(got, s.Rows, s.Bag); !ok {
		t.Fatalf("rows=%#v want %#v", got, s.Rows)
	}
}

func TestReferenceIntaggControls(t *testing.T) {
	intaggCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		intaggStored(run)
		checks := intaggStoredChecks(b)
		for _, s := range checks {
			fixture.ArrayAggBoth(run, s)
		}
		// Both installed functions must own real stored-expression edges, not
		// merely have equal values to their builtin implementation kernels.
		deps := fixture.Select("SELECT DISTINCT p.proname FROM pg_depend d JOIN pg_proc p ON p.oid=d.refobjid JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='ia_addon' AND d.refclassid='pg_proc'::regclass::oid AND p.proname IN ('int_array_aggregate','int_array_enum') ORDER BY p.proname", []string{"proname"}, []string{"NAME"}, [][]any{{"int_array_aggregate"}, {"int_array_enum"}})
		fixture.ArrayAggBoth(run, deps)
		t.Run("schema-restrict", func(t *testing.T) {
			w := fixture.Diagnostic{
				Code: "2BP01", Message: "cannot drop schema ia_addon because other objects depend on it",
				Detail: "extension intagg depends on schema ia_addon\nview ia_aggregate_view depends on function ia_addon.int_array_aggregate(integer)\nview ia_enum_view depends on function ia_addon.int_array_enum(integer[])\nview ia_target_view depends on function ia_addon.int_array_enum(integer[])",
				Hint:   "Use DROP ... CASCADE to drop the dependent objects too.",
			}
			intaggError(t, p, b, c, "DROP SCHEMA ia_addon RESTRICT", w, true)
		})
		for _, s := range checks {
			run(s)
		}
		if b.PG {
			return
		} // Native replacement APIs have no PostgreSQL counterpart.
		dump := intaggDump(t, p.Ctx, p.Native)
		install, view := strings.Index(dump, "CREATE EXTENSION"), strings.Index(dump, "CREATE VIEW")
		if install < 0 || view < 0 || install > view || !strings.Contains(dump, "int_array_aggregate") || !strings.Contains(dump, "int_array_enum") {
			t.Fatal("missing provider-first wrapper recipes", dump)
		}
		if strings.Contains(dump, "CREATE FUNCTION") || strings.Contains(dump, "array_agg(") || strings.Contains(dump, "unnest(") {
			t.Fatal("wrapper replaced by helper/kernel", dump)
		}
		q := "SELECT ia_addon.int_array_aggregate($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		frame := intaggArray(b, q, []string{"int_array_aggregate"}, []string{"_INT4"}, [][]any{{"{6}"}}, int64(6))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
		eq := "SELECT v,n FROM ia_addon.int_array_enum($1) WITH ORDINALITY AS u(v,n) ORDER BY n"
		es, err := c.PrepareContext(p.Ctx, eq)
		if err != nil {
			t.Fatal(err)
		}
		defer es.Close()
		eheld := fixture.HeldStatement{Executor: c, Statement: es, SQL: eq}
		eframe := fixture.Select(eq, []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(2), int64(1)}, {int64(2), int64(2)}, {nil, int64(3)}}, "{2,2,NULL}")
		fixture.RunStep(t, p.Ctx, eheld, b.Recorder, eframe)
		type retained struct {
			rows *sql.Rows
			step fixture.Step
			meta []*sql.ColumnType
		}
		var old []retained
		for _, s := range checks[:2] {
			rows, err := c.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			meta, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			old = append(old, retained{rows, s, meta})
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='intagg'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
		fixture.RunStep(t, p.Ctx, eheld, b.Recorder, eframe)
		for _, r := range old {
			for i, m := range r.meta {
				if m.Name() != r.step.Columns[i] || m.DatabaseTypeName() != r.step.Types[i] {
					t.Fatal("retained descriptor", m)
				}
			}
			intaggControlRows(t, r.rows, r.step)
		}
		for _, s := range checks {
			fixture.ArrayAggBoth(run, s)
		}
		run(deps)
		if intaggDump(t, p.Ctx, p.Native) != dump {
			t.Fatal("symbolic redump changed")
		}
		other := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "intagg_destination")
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		for _, s := range checks {
			rows, err := other.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			intaggControlRows(t, rows, s)
		}
		if intaggDump(t, p.Ctx, other) != dump {
			t.Fatal("independent replay changed symbolic identities")
		}
		intaggRestoreFailures(t, p, b, c, dump, checks, func() {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
			fixture.RunStep(t, p.Ctx, eheld, b.Recorder, eframe)
		})
	})
}

func intaggRestoreFailures(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, dump string, checks []fixture.Step, held func()) {
	t.Helper()
	const prefix = "-- stuntgres-extension-owners-v2 "
	start := strings.Index(dump, prefix)
	if start < 0 {
		t.Fatal("missing extension owners")
	}
	rel := strings.Index(dump[start:], "\n")
	if rel < 0 {
		t.Fatal("unterminated owners")
	}
	end := start + rel
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dump[start:end], prefix))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	members, ok := record["members"].([]any)
	if !ok || len(members) != 2 {
		t.Fatal("two-member owner manifest", record)
	}
	mutate := func(key string, value any) string {
		copy := make(map[string]any, len(record))
		for k, v := range record {
			copy[k] = v
		}
		copy[key] = value
		encoded, err := json.Marshal(copy)
		if err != nil {
			t.Fatal(err)
		}
		return dump[:start] + prefix + base64.StdEncoding.EncodeToString(encoded) + dump[end:]
	}
	for _, bad := range []string{
		mutate("members", []any{}), mutate("version", "missing"), mutate("schema", "other_schema"),
		mutate("members", []any{members[0], members[0]}),
		dump + "\nSELECT missing_intagg_restore_column;",
		"CREATE SCHEMA ia_addon; CREATE EXTENSION intagg SCHEMA ia_addon VERSION 'missing';",
	} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid replacement accepted", bad)
		}
		for _, s := range checks {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		held()
		if intaggDump(t, p.Ctx, p.Native) != dump {
			t.Fatal("failed Restore changed live data/provider")
		}
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("Restore cancellation", err)
	}
	for _, s := range checks {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
	held()
	if intaggDump(t, p.Ctx, p.Native) != dump {
		t.Fatal("cancellation changed live data/provider")
	}
}

func TestReferenceIntaggFeatureSmoke(t *testing.T) {
	intaggCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		intaggStored(run)
		for _, s := range intaggStoredChecks(b) {
			fixture.ArrayAggBoth(run, s)
		}
		q := "SELECT ia_addon.int_array_enum($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, arg := range []any{"{2,2,NULL}", "{}", nil} {
			var want [][]any
			if arg == "{2,2,NULL}" {
				want = [][]any{{int64(2)}, {int64(2)}, {nil}}
			}
			s := fixture.Select(q, []string{"int_array_enum"}, []string{"INT4"}, want, arg)
			s.Bag = true
			fixture.ArrayAggBoth(run, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		t.Run("bad-signature", func(t *testing.T) {
			intaggError(t, p, b, c, "SELECT ia_addon.int_array_enum(1)", intaggMissing("ia_addon.int_array_enum(integer)"), false)
		})
		if !b.PG {
			dump := intaggDump(t, p.Ctx, p.Native)
			if !strings.Contains(dump, "int_array_aggregate") || !strings.Contains(dump, "int_array_enum") {
				t.Fatal("wrapper recipes missing", dump)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			for _, s := range intaggStoredChecks(b) {
				fixture.ArrayAggBoth(run, s)
			}
		}
	})
}
