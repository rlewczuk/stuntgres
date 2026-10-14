package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func queryWindowValueControls(t *testing.T, p *fixture.Pair, steps []fixture.Step) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil || dump.Len() == 0 {
		t.Fatal("Dump", err)
	}
	sourceRows, err := p.Memory.QueryContext(p.Ctx, steps[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	defer sourceRows.Close()
	fresh := fixture.NewFixture(t, false, true)
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	retained, err := fresh.Memory.QueryContext(fresh.Ctx, steps[0].Query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	for _, s := range steps {
		for _, prepared := range []bool{false, true} {
			s.Prepared = prepared
			fixture.EnumReferenceRunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, s)
		}
	}
	if err := stuntgres.Reset(fresh.Ctx, fresh.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	queryWindowRangeRetained(t, sourceRows, steps[0].Columns, steps[0].Types, steps[0].Rows)
	queryWindowRangeRetained(t, retained, steps[0].Columns, steps[0].Types, steps[0].Rows)
	for _, s := range steps {
		fixture.EnumReferenceRunStep(t, fresh.Ctx, fresh.Memory, fresh.MemoryRecord, s)
		fixture.EnumReferenceRunStep(t, p.Ctx, p.Memory, p.MemoryRecord, s)
	}
}

func queryWindowValueViews(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE wv_arg(v INT,n INT)", 0),
		fixture.Exec("INSERT INTO wv_arg VALUES(11,2)", 1),
		fixture.Exec("CREATE DOMAIN wv_literal AS INTEGER", 0),
		fixture.Exec("CREATE VIEW wv_named AS SELECT id,first_value(v) OVER w AS f,last_value(v) OVER w AS l,nth_value(v,'4'::wv_literal::int) OVER w AS n FROM wv WINDOW base AS(PARTITION BY p ORDER BY id),w AS(base ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)", 0),
		fixture.Exec("CREATE VIEW wv_children AS SELECT id,first_value((SELECT v FROM wv_arg)) OVER w AS f,last_value(NULL::text) OVER w AS l,nth_value(v,(SELECT n FROM wv_arg)) OVER w AS n FROM wv WINDOW w AS(PARTITION BY p ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)", 0),
		fixture.Exec("CREATE VIEW wv_literals AS SELECT id,first_value('wv_arg'::regclass::text) OVER w AS f,last_value(NULL::text) OVER w AS l,nth_value('kept'::text,'2'::wv_literal::int) OVER w AS n FROM wv WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)", 0),
		fixture.Exec("CREATE VIEW wv_arrays AS SELECT id,first_value(ARRAY[id,NULL::int]) OVER w AS f,last_value(ARRAY[id,NULL::int]) OVER w AS l,nth_value(ARRAY[id,NULL::int],100) OVER w AS n FROM wv WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)", 0),
		fixture.Exec("CREATE VIEW wv_bytes AS SELECT id,first_value('\\x00ff'::bytea) OVER w AS f,last_value(NULL::bytea) OVER w AS l,nth_value('\\x01'::bytea,100) OVER w AS n FROM wv WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)", 0),
		fixture.Exec("ALTER TABLE wv_arg RENAME COLUMN v TO value_only", 0),
		fixture.Exec("ALTER TABLE wv_arg RENAME COLUMN n TO ordinal_only", 0),
	} {
		queryWindowRun(t, p, b, s)
	}
	var steps []fixture.Step
	for _, tc := range []struct {
		name  string
		types []string
		rows  [][]any
	}{
		{"wv_named", []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(40), int64(40)}, {int64(2), int64(10), int64(40), int64(40)}, {int64(3), int64(10), int64(40), int64(40)}, {int64(4), int64(10), int64(40), int64(40)}, {int64(5), int64(10), int64(40), int64(40)}, {int64(6), nil, int64(70), nil}, {int64(7), nil, int64(70), nil}}},
		{"wv_children", []string{"INT4", "INT4", "TEXT", "INT4"}, [][]any{{int64(1), int64(11), nil, int64(10)}, {int64(2), int64(11), nil, int64(10)}, {int64(3), int64(11), nil, int64(10)}, {int64(4), int64(11), nil, int64(10)}, {int64(5), int64(11), nil, int64(10)}, {int64(6), int64(11), nil, int64(70)}, {int64(7), int64(11), nil, int64(70)}}},
		{"wv_literals", []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "wv_arg", nil, "kept"}, {int64(2), "wv_arg", nil, "kept"}, {int64(3), "wv_arg", nil, "kept"}, {int64(4), "wv_arg", nil, "kept"}, {int64(5), "wv_arg", nil, "kept"}, {int64(6), "wv_arg", nil, "kept"}, {int64(7), "wv_arg", nil, "kept"}}},
		{"wv_arrays", []string{"INT4", "_INT4", "_INT4", "_INT4"}, [][]any{{int64(1), "{1,NULL}", "{7,NULL}", nil}, {int64(2), "{1,NULL}", "{7,NULL}", nil}, {int64(3), "{1,NULL}", "{7,NULL}", nil}, {int64(4), "{1,NULL}", "{7,NULL}", nil}, {int64(5), "{1,NULL}", "{7,NULL}", nil}, {int64(6), "{1,NULL}", "{7,NULL}", nil}, {int64(7), "{1,NULL}", "{7,NULL}", nil}}},
		{"wv_bytes", []string{"INT4", "BYTEA", "BYTEA", "BYTEA"}, [][]any{{int64(1), string([]byte{0, 255}), nil, nil}, {int64(2), string([]byte{0, 255}), nil, nil}, {int64(3), string([]byte{0, 255}), nil, nil}, {int64(4), string([]byte{0, 255}), nil, nil}, {int64(5), string([]byte{0, 255}), nil, nil}, {int64(6), string([]byte{0, 255}), nil, nil}, {int64(7), string([]byte{0, 255}), nil, nil}}},
	} {
		s := fixture.Select("SELECT * FROM "+tc.name+" ORDER BY id", []string{"id", "f", "l", "n"}, tc.types, tc.rows)
		queryWindowRoutes(t, p, b, s)
		steps = append(steps, queryWindowReviewReplay(t, p, b, tc.name, s)...)
		var definition string
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('"+tc.name+"'::regclass,true)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE VIEW "+tc.name+"_getdef AS "+definition, 0))
		s.Query = "SELECT * FROM " + tc.name + "_getdef ORDER BY id"
		queryWindowRoutes(t, p, b, s)
		steps = append(steps, s)
	}
	for _, q := range []string{
		"CREATE TYPE wv_owned AS ENUM('z','a')",
		"CREATE DOMAIN wv_wrapped AS wv_owned",
		"CREATE TABLE wv_owned_source(id INT,v wv_wrapped)",
	} {
		queryWindowRun(t, p, b, fixture.Exec(q, 0))
	}
	queryWindowRun(t, p, b, fixture.Exec("INSERT INTO wv_owned_source VALUES(1,'z'),(2,NULL)", 2))
	queryWindowRun(t, p, b, fixture.Exec("CREATE VIEW wv_owned_view AS SELECT id,first_value(v) OVER w AS f,last_value(v) OVER w AS l,nth_value(v,100) OVER w AS n FROM wv_owned_source WINDOW w AS(ORDER BY id ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)", 0))
	owned := fixture.Select("SELECT * FROM wv_owned_view ORDER BY id", []string{"id", "f", "l", "n"}, []string{"INT4", "", "", ""}, [][]any{{int64(1), "z", nil, nil}, {int64(2), "z", nil, nil}})
	for _, prepared := range []bool{false, true} {
		owned.Prepared = prepared
		fixture.EnumReferenceRunStep(t, p.Ctx, b.DB, b.Recorder, owned)
	}
	owned.Prepared = false
	steps = append(steps, owned)
	if !b.PG {
		queryWindowValueControls(t, p, steps)
	}
}

func TestReferenceQueryWindowValueViews(t *testing.T) {
	t.Run("persistence", func(t *testing.T) {
		queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			queryWindowValueSeed(t, p, b)
			queryWindowValueViews(t, p, b)
		})
	})
	t.Run("child-authorization", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"wv_reader"})
		queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			queryWindowValueAuthorization(t, p, b)
		})
	})
	t.Run("child-dependencies", func(t *testing.T) {
		queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE wv_dep(id INT,v INT,n INT)", 0),
				fixture.Exec("CREATE VIEW wv_dep_value AS SELECT first_value(v) OVER(ORDER BY id) FROM wv_dep", 0),
				fixture.Exec("CREATE VIEW wv_dep_ordinal AS SELECT nth_value(1,n) OVER(ORDER BY id) FROM wv_dep", 0),
			} {
				queryWindowRun(t, p, b, s)
			}
			for _, tc := range []struct{ column, view string }{{"v", "wv_dep_value"}, {"n", "wv_dep_ordinal"}} {
				d := fixture.Diagnostic{Code: "2BP01", Message: "cannot drop column " + tc.column + " of table wv_dep because other objects depend on it", Detail: "view " + tc.view + " depends on column " + tc.column + " of table wv_dep", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("ALTER TABLE wv_dep DROP COLUMN "+tc.column, d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					queryWindowRun(t, p, b, s)
				}
			}
		})
	})
}
