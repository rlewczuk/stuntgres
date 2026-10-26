package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceGiSTControls(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "gist_portable")
	p := fixture.NewScenario(t)
	var emitted string
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := classConn(t, p, b)
			for _, q := range []string{
				"CREATE SCHEMA gist_portable",
				"CREATE TABLE gist_portable.t(id integer,p point,b box,g polygon,c circle,i inet,r int4range,m int4multirange,q tsquery,v tsvector,w tsvector)",
				"INSERT INTO gist_portable.t VALUES(1,'(0,0)','(2,2),(0,0)','((0,0),(4,0),(0,4))','<(0,0),1>','10.0.0.1/24','[1,3)','{[1,3),[7,9)}','a & b','a:1','b:2')",
				"CREATE INDEX gist_portable_point ON gist_portable.t USING gist(p) INCLUDE(id) WITH(buffering=off,fillfactor=80)",
				"CREATE INDEX gist_portable_box ON gist_portable.t USING gist(b)",
				"CREATE INDEX gist_portable_poly ON gist_portable.t USING gist(g)",
				"CREATE INDEX gist_portable_circle ON gist_portable.t USING gist(c)",
				"CREATE INDEX gist_portable_inet ON gist_portable.t USING gist(i inet_ops)",
				"CREATE INDEX gist_portable_range ON gist_portable.t USING gist(r)",
				"CREATE INDEX gist_portable_multi ON gist_portable.t USING gist(m)",
				"CREATE INDEX gist_portable_query ON gist_portable.t USING gist(q)",
				"CREATE INDEX gist_portable_vector ON gist_portable.t USING gist(v tsvector_ops(siglen=32),w tsvector_ops(siglen=2024))",
				"CREATE INDEX gist_portable_expr ON gist_portable.t USING gist((point(id,0))) WHERE id>0",
			} {
				count := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					count = 1
				}
				run(fixture.Exec(q, count))
			}
			const query = "SELECT id,p <-> point '(0,0)' AS d FROM gist_portable.t ORDER BY id"
			want := fixture.Select(query, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(0)}})
			held := classHeld(t, p, conn, query)
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				emitted = dump.String()
				if !strings.Contains(emitted, "siglen='32'") || !strings.Contains(emitted, "siglen='2024'") || !strings.Contains(emitted, "buffering") {
					t.Fatal("actual options absent from Dump", emitted)
				}
				rows, err := conn.QueryContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				foreign := fixture.NewFixture(t, false, true)
				if err := stuntgres.Restore(foreign.Ctx, foreign.Native, strings.NewReader(emitted)); err != nil {
					t.Fatal("foreign replay", err)
				}
				fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, want)
				fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Exec("INSERT INTO gist_portable.t(id,p,v,w) VALUES(2,'(3,4)','c:3','d:4')", 1))
				fixture.RunStep(t, foreign.Ctx, foreign.Memory, foreign.MemoryRecord, fixture.Select(query, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(0)}, {int64(2), float64(5)}}))
				var source bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &source); err != nil || source.String() != emitted {
					t.Fatal("foreign replay mutated source", err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
					t.Fatal(err)
				}
				fixture.DeleteReferenceConsume(t, false, rows, nil, want)
				bad := emitted + "CREATE INDEX late_invalid ON gist_portable.t USING gist(v tsvector_ops(siglen=0));"
				err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
				d := fixture.ControlsReferenceDiagnostic("22023", `value 0 out of bounds for option "siglen"`)
				d.Detail = `Valid values are between "1" and "2024".`
				fixture.AssertDiagnostic(t, err, &d)
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || after.String() != emitted {
					t.Fatal("invalid Restore changed destination", err)
				}
			} else {
				if emitted == "" {
					t.Fatal("missing actual native SQL")
				}
				run(fixture.Exec("DROP SCHEMA gist_portable CASCADE", 0))
				if _, err := conn.ExecContext(p.Ctx, emitted); err != nil {
					t.Fatal("PostgreSQL rejected actual Dump", err)
				}
			}
			classRoutes(t, p, b, conn, want)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
			run(fixture.Select("SELECT a.attnum,a.attoptions::text AS options FROM pg_attribute a WHERE a.attrelid='gist_portable.gist_portable_vector'::regclass ORDER BY a.attnum", []string{"attnum", "options"}, []string{"INT2", "TEXT"}, [][]any{{int64(1), "{siglen=32}"}, {int64(2), "{siglen=2024}"}}))
			run(fixture.Exec("INSERT INTO gist_portable.t(id,p,v,w) VALUES(2,'(3,4)','c:3','d:4')", 1))
			run(fixture.Select(query, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(0)}, {int64(2), float64(5)}}))
		})
	}
}
