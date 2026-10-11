package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryDistinctTypesTreeCarrier(t *testing.T) {
	fixture.QueryDistinctScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE tree_repeat(id INT)", 0))
		for _, populated := range []bool{false, true} {
			if populated {
				run(fixture.Exec("INSERT INTO tree_repeat VALUES(1),(2)", 2))
			}
			want := [][]any{}
			if populated {
				want = [][]any{{nil}}
			}
			fixture.QueryDistinctBoth(run, fixture.Select("SELECT DISTINCT NULL::pg_node_tree AS tree FROM tree_repeat", []string{"tree"}, []string{"PG_NODE_TREE"}, want))
		}
		// Use admitted nonNULL catalog carriers, not forbidden private tree input.
		// The cross join repeats each exact carrier; it does not assume that
		// different source-position encodings represent an equal tree datum.
		run(fixture.Exec("CREATE TABLE tree_source(a INT DEFAULT 1,b INT DEFAULT 2)", 0))
		run(fixture.Exec("CREATE VIEW tree_dv AS SELECT DISTINCT d.adbin FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid CROSS JOIN tree_repeat WHERE c.relname='tree_source'", 0))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT count(*) AS n FROM tree_dv", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT pg_get_expr(adbin,0) AS definition FROM tree_dv ORDER BY definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"1"}, {"2"}}))
		fixture.QueryDistinctBoth(run, fixture.Select("SELECT DISTINCT adbin FROM pg_catalog.pg_attrdef WHERE FALSE", []string{"adbin"}, []string{"PG_NODE_TREE"}, [][]any{}))
		const query = "SELECT adbin AS tree,pg_get_expr(adbin,0) AS definition FROM tree_dv ORDER BY definition"
		queryDistinctTreeConsumers(t, p, c, query)
		if !b.PG {
			// The actual persistent nonNULL tree view participates in the shared
			// atomic replay seam, not just an unrelated numeric/array fixture.
			s := fixture.Select("SELECT pg_get_expr(adbin,0) AS definition FROM tree_dv ORDER BY definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"1"}, {"2"}})
			queryDistinctNativeReplay(t, p, s, "CREATE VIEW invalid_distinct AS SELECT DISTINCT a FROM tree_source ORDER BY b;\n", "b;")
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, reset := range []bool{false, true} {
				retained, err := c.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				if reset {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						retained.Close()
						t.Fatal(err)
					}
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					retained.Close()
					t.Fatal(err)
				}
				queryDistinctTreeRows(t, retained, nil)
				// Fresh direct/prepared/held consumers use remapped tree payloads
				// and descriptor generations after each catalog replacement.
				queryDistinctTreeConsumers(t, p, c, query)
				fixture.QueryDistinctBoth(run, fixture.Select("SELECT count(*) AS n FROM tree_dv", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
			}
		}
	})
}

func queryDistinctTreeConsumers(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, query)
	queryDistinctTreeRows(t, rows, err)
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for range 2 {
		rows, err := stmt.QueryContext(p.Ctx)
		queryDistinctTreeRows(t, rows, err)
	}
}

func queryDistinctTreeRows(t *testing.T, rows *sql.Rows, err error) {
	t.Helper()
	if err != nil {
		if rows != nil {
			rows.Close()
		}
		t.Fatal(err)
	}
	if rows == nil {
		t.Fatal("tree consumer returned no cursor")
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 2 {
		t.Fatal("tree cursor width", len(columns))
	}
	for i, want := range []struct{ name, typ string }{{"tree", "PG_NODE_TREE"}, {"definition", "TEXT"}} {
		if columns[i].Name() != want.name || columns[i].DatabaseTypeName() != want.typ {
			t.Fatal("tree cursor metadata", i, columns[i])
		}
	}
	var first string
	for i, want := range []string{"1", "2"} {
		if !rows.Next() {
			t.Fatal("missing nonNULL tree class", i, rows.Err())
		}
		var tree, definition string
		if err := rows.Scan(&tree, &definition); err != nil {
			t.Fatal(err)
		}
		if definition != want || tree == "" {
			t.Fatal("tree payload/output", i, tree, definition, want)
		}
		// Physical node text is backend-specific. The independently specified
		// classes are nonNULL, distinct carriers whose logical payloads deparse
		// to 1 and 2; no native/PostgreSQL raw-text parity is assumed.
		if i == 0 {
			first = tree
		} else if tree == first {
			t.Fatal("different tree payloads have identical structural output")
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra/error tree row", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
