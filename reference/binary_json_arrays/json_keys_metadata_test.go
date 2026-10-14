package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonKeysMetadata(t *testing.T, rows *sql.Rows, postgres bool, names, types []string) {
	t.Helper()
	computed := make([]int, len(types))
	for i := range computed {
		computed[i] = i
	}
	fixture.DriverBasicMetadata(t, rows, postgres, types, make([]bool, len(types)), computed...)
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, ct := range meta {
		if ct.Name() != names[i] {
			t.Errorf("column %d name=%q want=%q", i, ct.Name(), names[i])
		}
		if precision, scale, known := ct.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Errorf("column %d DecimalSize=%d/%d/%t", i, precision, scale, known)
		}
	}
}

func TestReferenceJSONKeysMetadata(t *testing.T) {
	jsonKeysScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, input := range []any{`{"a":1}`, `{}`, nil} {
			for _, tc := range []struct {
				q string
				names, types []string
			}{
				{"SELECT json_object_keys($1)", []string{"json_object_keys"}, []string{"TEXT"}},
				{"SELECT * FROM json_object_keys($1)", []string{"json_object_keys"}, []string{"TEXT"}},
				{"SELECT json_object_keys($1) AS key", []string{"key"}, []string{"TEXT"}},
				{"SELECT * FROM json_object_keys($1) AS f(key)", []string{"key"}, []string{"TEXT"}},
				{"SELECT * FROM json_object_keys($1) WITH ORDINALITY AS f(key,n) ORDER BY n", []string{"key", "n"}, []string{"TEXT", "INT8"}},
			} {
				t.Run(tc.q+" input", func(t *testing.T) {
					var want [][]any
					if input == `{"a":1}` {
						want = [][]any{{"a"}}
						if len(tc.types) == 2 {
							want = [][]any{{"a", int64(1)}}
						}
					}
					s := fixture.Select(tc.q, tc.names, tc.types, want, input)
					s.Bag = true
					s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonKeysMetadata(t, r, b.PG, tc.names, tc.types) }
					jsonKeysBoth(t, p, b, c, s)
				})
			}
		}
	})
}

func jsonKeysHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{"SELECT json_object_keys($1)", "SELECT * FROM json_object_keys($1)"} {
		t.Run(q, func(t *testing.T) {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct {
				input any
				keys []string
				code, message string
			}{
				{`{"a":1,"a":2,"b":3}`, []string{"a", "a", "b"}, "", ""},
				{`{}`, nil, "", ""},
				{nil, nil, "", ""},
				{`[]`, nil, "22023", "cannot call json_object_keys on an array"},
				{`{"a":"\u0000"}`, nil, "22P05", "unsupported Unicode escape sequence"},
				{`{"\u00e9":1}`, []string{"é"}, "", ""},
			} {
				if tc.code != "" {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					if tc.code == "22P05" {
						d.Detail = `\u0000 cannot be converted to text.`
						d.Context = `JSON data, line 1: {"a":"\u0000...`
					}
					jsonKeysError(t, p, b, held, q, false, d, "execute", tc.input)
					continue
				}
				s := jsonKeysSelect(q, tc.keys, tc.input)
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonKeysMetadata(t, r, b.PG, s.Columns, s.Types) }
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		})
	}
}

func TestReferenceJSONKeysParametersAndLifecycle(t *testing.T) {
	jsonKeysScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonKeysHeld(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jk_atomic_source(id int,j json)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jk_atomic_source VALUES(1,'{"ok":1}'),(2,'[]')`, 2))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jk_dest(k text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic("INSERT INTO jk_dest SELECT json_object_keys(j) FROM jk_atomic_source ORDER BY id", fixture.ControlsReferenceDiagnostic("22023", "cannot call json_object_keys on an array")))
		jsonKeysBoth(t, p, b, c, fixture.Select("SELECT k FROM jk_dest", []string{"k"}, []string{"TEXT"}, nil))
	})
}
