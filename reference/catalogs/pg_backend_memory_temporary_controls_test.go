package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

const backendMemoryTemporaryText = "SELECT saved::text AS saved,saved_array::text AS saved_array FROM backend_memory_saved"
const backendMemoryTemporaryRaw = "SELECT saved,saved_array FROM backend_memory_saved"

func backendMemoryTemporarySetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	pgSequenceExec(t, p, b, c, "CREATE TEMP TABLE backend_memory_saved(saved pg_catalog.pg_backend_memory_contexts,saved_array pg_catalog.pg_backend_memory_contexts[])")
	t.Cleanup(func() { c.ExecContext(context.Background(), "DROP TABLE IF EXISTS backend_memory_saved") })
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO backend_memory_saved SELECT m,ARRAY[m] FROM pg_catalog.pg_backend_memory_contexts AS m WHERE name='TopMemoryContext'", 1))
}

// The stored capture is checked independently on each backend. Only after its
// ten fields satisfy that backend's invariants does it become an immutability
// witness; live PostgreSQL counters are never native-derived golden values.
func backendMemoryTemporaryCapture(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) (fixture.Step, backendMemoryRow) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, backendMemoryTemporaryText)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing populated TEMP capture", rows.Err())
	}
	var saved, array string
	if err := rows.Scan(&saved, &array); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("TEMP capture must have exactly one row", rows.Err())
	}
	root := backendMemoryRecord(t, saved)
	var elements pq.StringArray
	if err := elements.Scan(array); err != nil || len(elements) != 1 {
		t.Fatal("one stored composite array element", elements, err)
	}
	if element := backendMemoryRecord(t, elements[0]); !reflect.DeepEqual(element, root) {
		t.Fatal("array did not retain the same captured row", element, root)
	}
	if root.Name != "TopMemoryContext" || root.Level != 1 || !reflect.DeepEqual(root.Path, []int64{1}) {
		t.Fatal("stored root identity", root)
	}
	if b.PG {
		// A single captured root is a valid one-context tree. Its own allocator
		// counters obey PostgreSQL's identities, without fixed byte expectations.
		backendMemoryTopology(t, []backendMemoryRow{root}, true)
	} else if root.Ident.Valid || root.Type != "GoObjectLayout" || !root.Total.Valid || root.Total.Int64 <= 0 || root.Used != root.Total || root.Blocks.Valid || root.Free.Valid || root.Chunks.Valid {
		t.Fatal("stored root must report its real selected layout", root)
	}
	return fixture.Select(backendMemoryTemporaryText, []string{"saved", "saved_array"}, []string{"TEXT", "TEXT"}, [][]any{{saved, array}}), root
}

func backendMemoryTemporaryIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	backendMemoryStepRoutes(t, p, b, c, fixture.Select(
		"SELECT attname::text,atttypid::bigint FROM pg_catalog.pg_attribute WHERE attrelid='backend_memory_saved'::regclass AND attnum>0 ORDER BY attnum",
		[]string{"attname", "atttypid"}, []string{"TEXT", "INT8"},
		[][]any{{"saved", int64(12144)}, {"saved_array", int64(12143)}},
	))
	q := "SELECT pg_typeof(s.saved)::oid::bigint AS row_type,pg_typeof(s.saved_array)::oid::bigint AS array_type,pg_typeof((s.saved).path)::oid::bigint AS path_type,pg_typeof(s.saved_array[1])::oid::bigint AS element_type,pg_typeof((s.saved_array[1]).path)::oid::bigint AS element_path_type,(s.saved).name AS name,(s.saved_array[1]).name AS element_name,(s.saved).level AS depth,(s.saved_array[1]).level AS element_depth,(s.saved).path::text AS path,(s.saved_array[1]).path::text AS element_path,array_length(s.saved_array,1) AS elements FROM backend_memory_saved AS s"
	backendMemoryStepRoutes(t, p, b, c, fixture.Select(q,
		[]string{"row_type", "array_type", "path_type", "element_type", "element_path_type", "name", "element_name", "depth", "element_depth", "path", "element_path", "elements"},
		[]string{"INT8", "INT8", "INT8", "INT8", "INT8", "TEXT", "TEXT", "INT4", "INT4", "TEXT", "TEXT", "INT4"},
		[][]any{{int64(12144), int64(12143), int64(1007), int64(12144), int64(1007), "TopMemoryContext", "TopMemoryContext", int64(1), int64(1), "{1}", "{1}", int64(1)}},
	))
	backendMemoryCatalogMetadata(t, p, b, c)
}

func backendMemoryTemporaryStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held fixture.HeldStatement, saved fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, saved)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(saved))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, saved)
	backendMemoryTemporaryIdentity(t, p, b, c)
}

func TestReferenceBackendMemoryTemporaryControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			backendMemorySetupSentinel(t, p, b, c)
			backendMemoryTemporarySetup(t, p, b, c)
			saved, root := backendMemoryTemporaryCapture(t, p, b, c)
			held := backendMemoryHeld(t, p, c, saved.Query)
			backendMemoryTemporaryStored(t, p, b, c, held, saved)
			backendMemoryRead(t, p, b, c, backendMemoryView, false)
			backendMemoryRead(t, p, b, c, backendMemoryFunction, true)
			backendMemorySentinel(t, p, b, c)
			if b.PG {
				// Shared storage, field/array access and catalog assertions above
				// are the PostgreSQL oracle. Native controls have no PG equivalent.
				return
			}

			dump := backendMemoryDump(t, p, p.Native)
			if strings.Contains(dump, "backend_memory_saved") || strings.Contains(dump, "GoObjectLayout") {
				t.Fatal("TEMP values or live memory observations serialized", dump)
			}
			// Unlike the text preservation reads, these eager results retain the
			// actual named composite and its array until after all replacements.
			retained, err := c.QueryContext(p.Ctx, backendMemoryTemporaryRaw)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			metadata, err := retained.ColumnTypes()
			if err != nil || len(metadata) != 2 || metadata[0].Name() != "saved" || metadata[1].Name() != "saved_array" {
				t.Fatal("retained typed TEMP metadata", metadata, err)
			}
			check := func(t *testing.T) {
				t.Helper()
				backendMemoryTemporaryStored(t, p, b, c, held, saved)
				for _, query := range []string{backendMemoryView, backendMemoryFunction} {
					fresh := backendMemoryRead(t, p, b, c, query, false)
					if backendMemoryOwn(fresh)["TopMemoryContext"] != root.Total.Int64 {
						t.Fatal("replacement changed the still-live physical session allocation", fresh, root)
					}
				}
				backendMemoryRetainedMetadata(t, retained, metadata)
			}
			t.Run("reset", func(t *testing.T) {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT to_regclass('backend_memory_sentinel') IS NULL AS missing", []string{"missing"}, []string{"BOOL"}, [][]any{{true}}))
				check(t)
			})
			if t.Failed() {
				return
			}
			for _, name := range []string{"restore", "restore_again"} {
				if !t.Run(name, func(t *testing.T) {
					backendMemoryRestore(t, p, p.Native, dump)
					backendMemorySentinel(t, p, b, c)
					check(t)
				}) {
					return
				}
			}
			t.Run("failed_restore", func(t *testing.T) {
				bad := dump + "\nnot valid SQL;\n"
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
					t.Fatal("invalid staged Restore succeeded")
				}
				backendMemorySentinel(t, p, b, c)
				check(t)
			})
			backendMemoryRetainedMetadata(t, retained, metadata)
			if !retained.Next() {
				t.Fatal("old eager TEMP values disappeared", retained.Err())
			}
			var oldRow, oldArray string
			if err := retained.Scan(&oldRow, &oldArray); err != nil {
				t.Fatal(err)
			}
			if oldRow != saved.Rows[0][0] || oldArray != saved.Rows[0][1] {
				t.Fatal("old eager TEMP values were retagged or mutated", oldRow, oldArray, saved.Rows)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("old eager TEMP row count changed", retained.Err())
			}
		})
	}
}
