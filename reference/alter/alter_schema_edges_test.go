package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterSchemaSearchPathAndViews(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s1", "s2")
	for _, backend := range p.Backends() {
		postgres := backend.PG
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			c, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			r := backend.Recorder
			referenceAlterSchemaSetup(t, p, c, r)
			for _, q := range []string{"SET search_path TO s1,s2,public", "CREATE VIEW public.pairs AS SELECT l.id AS left_id,r.id AS right_id FROM s1.t AS l JOIN s1.t AS r ON l.id<r.id", "CREATE VIEW public.nested AS SELECT left_id,right_id FROM public.pairs"} {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
			}
			held, err := c.PrepareContext(p.Ctx, "SELECT id,note FROM t ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			insert, err := c.PrepareContext(p.Ctx, "INSERT INTO t(id,note) VALUES($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer insert.Close()
			sequence, err := c.PrepareContext(p.Ctx, "SELECT nextval('s1.q1')")
			if err != nil {
				t.Fatal(err)
			}
			defer sequence.Close()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, sequence, "SELECT nextval('s1.q1')"}, r, fixture.Select("SELECT nextval('s1.q1')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(10)}}))
			read := func(want [][]any) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, held, "SELECT id,note FROM t ORDER BY id"}, r, fixture.Select("SELECT id,note FROM t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, want))
			}
			seed := [][]any{{int64(7), "keep"}, {int64(9), nil}}
			read(seed)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ALTER TABLE s1.t SET SCHEMA s2", 0))
			sequenceFailure := referenceDDLPositionError("SELECT nextval('s1.q1')", "42P01", `relation "s1.q1" does not exist`, "16", false, "execute")
			sequenceFailure.SelectRows = true
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, sequence, sequenceFailure.Query}, r, sequenceFailure)
			read(seed)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, insert, "INSERT INTO t(id,note) VALUES($1,$2)"}, r, fixture.Exec("INSERT INTO t(id,note) VALUES($1,$2)", 1, 11, "new"))
			read([][]any{{int64(7), "keep"}, {int64(9), nil}, {int64(11), "new"}})
			for _, name := range []string{"pairs", "nested"} {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT left_id,right_id FROM public."+name+" ORDER BY left_id,right_id", []string{"left_id", "right_id"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}, {int64(7), int64(11)}, {int64(9), int64(11)}}))
			}
			var definition string
			// Make the moved source invisible so both deparsers must qualify it.
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO public", 0))
			if err := c.QueryRowContext(p.Ctx, "SELECT definition FROM pg_catalog.pg_views WHERE viewname='pairs'").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			source, oldSource := `"s2"."t"`, `"s1"."t"`
			if postgres {
				source, oldSource = "s2.t", "s1.t"
			}
			if !strings.Contains(definition, source) || strings.Contains(definition, oldSource) {
				t.Fatal("stale view source", definition)
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE VIEW public.replayed AS "+definition, 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT left_id,right_id FROM public.replayed ORDER BY left_id,right_id", []string{"left_id", "right_id"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}, {int64(7), int64(11)}, {int64(9), int64(11)}}))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO s1,s2,public", 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE s1.t(id INT CONSTRAINT nn_id NOT NULL,note TEXT)", 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO s1.t VALUES(99,'shadow')", 1))
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
			fresh := fixture.Select("SELECT id,note FROM t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, shadow)
			fixture.RunStep(t, p.Ctx, c, r, fresh)
			fresh.Prepared = true
			fixture.RunStep(t, p.Ctx, c, r, fresh)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id,note FROM s2.t ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, moved))
		})
	}
}

func TestReferenceAlterSchemaQuotedCollision(t *testing.T) {
	p := fixture.NewScenario(t)
	fixture.SchemaReferenceCleanup(t, "s1", "s2", `dest"\schema`)
	for _, backend := range p.Backends() {
		conn, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		referenceAlterSchemaSetup(t, p, conn, backend.Recorder)
		if err = conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range []fixture.Step{
		fixture.Exec(`CREATE SCHEMA "dest""\schema"`, 0),
		fixture.Exec(`CREATE TABLE s1."t""\name"(id INT)`, 0),
		fixture.Exec(`CREATE SEQUENCE "dest""\schema"."t""\name"`, 0),
	} {
		p.Step(t, step)
	}
	before := referenceDDLNativeDump(t, p)
	for _, prepared := range []bool{false, true} {
		step := fixture.ImplicitError(`ALTER TABLE s1."t""\name" SET SCHEMA "dest""\schema"`, fixture.Diagnostic{Code: "42P07", Message: `relation "t"\name" already exists in schema "dest"\schema"`})
		step.Prepared = prepared
		step.ErrorStage = "execute"
		p.Step(t, step)
		if referenceDDLNativeDump(t, p) != before {
			t.Fatal("quoted schema collision changed complete native closure")
		}
	}
	p.Step(t, fixture.Select(`SELECT id FROM s1."t""\name"`, []string{"id"}, []string{"INT4"}, nil))
}
