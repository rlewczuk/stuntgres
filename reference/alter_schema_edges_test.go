//go:build reference

package reference_test

import (
	"reflect"
	"strings"
	"testing"
)

func TestReferenceAlterSchemaSearchPathAndViews(t *testing.T) {
	for _, postgres := range []bool{true, false} {
		t.Run(map[bool]string{true: "postgres", false: "native"}[postgres], func(t *testing.T) {
			p := newReferencePair(t)
			schemaReferenceCleanup(t, "s1", "s2")
			db := p.memory
			if postgres {
				db = p.postgres
			}
			c, err := db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r := &referenceRecorder{}
			referenceAlterSchemaSetup(t, p, c, r)
			for _, q := range []string{"SET search_path TO s1,s2,public", "CREATE VIEW public.pairs AS SELECT l.id AS left_id,r.id AS right_id FROM s1.t AS l JOIN s1.t AS r ON l.id<r.id", "CREATE VIEW public.nested AS SELECT left_id,right_id FROM public.pairs"} {
				referenceRunStep(t, p.ctx, c, r, referenceExec(q, 0))
			}
			held, err := c.PrepareContext(p.ctx, "SELECT id,note FROM t ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			insert, err := c.PrepareContext(p.ctx, "INSERT INTO t(id,note) VALUES($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer insert.Close()
			read := func(want [][]any) {
				t.Helper()
				rows, err := held.QueryContext(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				labels, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(labels, []string{"id", "note"}) {
					t.Fatal(labels, err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
					t.Fatal(types, err)
				}
				got := [][]any{}
				for rows.Next() {
					var id int64
					var note any
					if err := rows.Scan(&id, &note); err != nil {
						t.Fatal(err)
					}
					if bytes, ok := note.([]byte); ok {
						note = string(bytes)
					}
					got = append(got, []any{id, note})
				}
				if rows.Err() != nil || !reflect.DeepEqual(got, want) {
					t.Fatal(got, want, rows.Err())
				}
			}
			seed := [][]any{{int64(7), "keep"}, {int64(9), nil}}
			read(seed)
			referenceRunStep(t, p.ctx, c, r, referenceExec("ALTER TABLE s1.t SET SCHEMA s2", 0))
			read(seed)
			result, err := insert.ExecContext(p.ctx, 11, "new")
			if err != nil {
				t.Fatal(err)
			}
			referenceLockAffected(t, result, 1)
			read([][]any{{int64(7), "keep"}, {int64(9), nil}, {int64(11), "new"}})
			for _, name := range []string{"pairs", "nested"} {
				referenceRunStep(t, p.ctx, c, r, referenceSelect("SELECT left_id,right_id FROM public."+name+" ORDER BY left_id,right_id", []string{"left_id", "right_id"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}, {int64(7), int64(11)}, {int64(9), int64(11)}}))
			}
			var definition string
			// Make the moved source invisible so both deparsers must qualify it.
			referenceRunStep(t, p.ctx, c, r, referenceExec("SET search_path TO public", 0))
			if err := c.QueryRowContext(p.ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='pairs'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			source, oldSource := `"s2"."t"`, `"s1"."t"`
			if postgres {
				source, oldSource = "s2.t", "s1.t"
			}
			if !strings.Contains(definition, source) || strings.Contains(definition, oldSource) {
				t.Fatal("stale view source", definition)
			}
			referenceRunStep(t, p.ctx, c, r, referenceExec("CREATE VIEW public.replayed AS "+definition, 0))
			referenceRunStep(t, p.ctx, c, r, referenceSelect("SELECT left_id,right_id FROM public.replayed ORDER BY left_id,right_id", []string{"left_id", "right_id"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}, {int64(7), int64(11)}, {int64(9), int64(11)}}))
			referenceRunStep(t, p.ctx, c, r, referenceExec("SET search_path TO s1,s2,public", 0))
			referenceRunStep(t, p.ctx, c, r, referenceExec("CREATE TABLE s1.t(id INT CONSTRAINT nn_id NOT NULL,note TEXT)", 0))
			referenceRunStep(t, p.ctx, c, r, referenceExec("INSERT INTO s1.t VALUES(99,'shadow')", 1))
			// An unchanged effective path does not invalidate PostgreSQL's
			// revalidated s2 plan merely because a new s1 shadow now exists.
			// Native table statements instead bind their original names each time.
			shadow := [][]any{{int64(99), "shadow"}}
			moved := [][]any{{int64(7), "keep"}, {int64(9), nil}, {int64(11), "new"}}
			if postgres {
				read(moved)
			} else {
				read(shadow)
			}
			fresh := referenceSelect("SELECT id,note FROM t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, shadow)
			referenceRunStep(t, p.ctx, c, r, fresh)
			fresh.prepared = true
			referenceRunStep(t, p.ctx, c, r, fresh)
			referenceRunStep(t, p.ctx, c, r, referenceSelect("SELECT id,note FROM s2.t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, moved))
		})
	}
}

func TestReferenceAlterSchemaQuotedCollision(t *testing.T) {
	p := newReferencePair(t)
	schemaReferenceCleanup(t, "s1", `dest"\schema`)
	for _, step := range []referenceStep{
		referenceExec("CREATE SCHEMA s1", 0),
		referenceExec(`CREATE SCHEMA "dest""\schema"`, 0),
		referenceExec(`CREATE TABLE s1."t""\name"(id INT)`, 0),
		referenceExec(`CREATE SEQUENCE "dest""\schema"."t""\name"`, 0),
	} {
		p.step(t, step)
	}
	for _, prepared := range []bool{false, true} {
		step := referenceImplicitError(`ALTER TABLE s1."t""\name" SET SCHEMA "dest""\schema"`, referenceDiagnostic{Code: "42P07", Message: `relation "t"\name" already exists in schema "dest"\schema"`})
		step.prepared = prepared
		step.errorStage = "execute"
		p.step(t, step)
	}
	p.step(t, referenceSelect(`SELECT id FROM s1."t""\name"`, []string{"id"}, []string{"INT4"}, nil))
}
