package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLimitDemand(t *testing.T) {
	for _, query := range []string{
		"SELECT 10/v AS n FROM t LIMIT 1",
		"SELECT 10/v AS n FROM t WHERE 10/v > 0 LIMIT 1",
		"SELECT 10/t.v AS n FROM t CROSS JOIN u WHERE 10/t.v > 0 LIMIT 1",
		"SELECT 10/v AS n FROM t WHERE 10/v > 0 ORDER BY 10/v LIMIT 0",
	} {
		t.Run(query, func(t *testing.T) {
			p := referenceLimitFixture(t)
			p.Step(t, fixture.Exec("CREATE TABLE u(x INT)", 0))
			p.Step(t, fixture.Exec("INSERT INTO u VALUES(1)", 1))
			want := referenceLimitIDs(5)
			if query == "SELECT 10/v AS n FROM t WHERE 10/v > 0 ORDER BY 10/v LIMIT 0" {
				want = referenceLimitIDs()
			}
			step := fixture.Select(query, []string{"n"}, []string{"INT4"}, want)
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
		})
	}
	for _, key := range []string{"id", "n"} {
		t.Run("sort evaluates cheap target/"+key, func(t *testing.T) {
			p := referenceLimitFixture(t)
			step := fixture.ImplicitError("SELECT 10/v AS n FROM t ORDER BY "+key+" LIMIT 1", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
			step.SelectRows = true
			step.ErrorStage = "execute"
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
		})
	}
	for _, suffix := range []string{"LIMIT 0", "OFFSET 1 LIMIT 1", "LIMIT 1"} {
		for _, prepared := range []bool{false, true} {
			t.Run("projection/"+suffix+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceLimitFixture(t)
				p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
				want := referenceLimitIDs(1)
				next := int64(2)
				if suffix == "LIMIT 0" {
					want = referenceLimitIDs()
					next = 1
				}
				if suffix == "OFFSET 1 LIMIT 1" {
					want = referenceLimitIDs(2)
					next = 3
				}
				step := fixture.Select("SELECT nextval('s') AS n FROM t ORDER BY id "+suffix, []string{"n"}, []string{"INT8"}, want)
				step.Prepared = prepared
				p.Step(t, step)
				p.Step(t, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(next)))
			})
		}
	}
	t.Run("held bound evaluates once offset first", func(t *testing.T) {
		p := referenceLimitFixture(t)
		p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) {
				stmt, err := backend.DB.PrepareContext(p.Ctx, "SELECT id FROM t ORDER BY id LIMIT nextval('s') OFFSET nextval('s')")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, want := range [][]int64{{2, 3}, {4}} {
					rows, err := stmt.QueryContext(p.Ctx)
					fixture.ViewValues(t, fixture.RelationOutcome{Rows: rows, Err: err}, "id", "INT4", want...)
				}
				fixture.ViewValues(t, fixture.RowQuery(p.Ctx, backend.DB, "SELECT nextval('s')", false), "nextval", "INT8", 5)
			})
		}
	})
	t.Run("empty still evaluates bound", func(t *testing.T) {
		p := referenceLimitFixture(t)
		p.Step(t, fixture.Exec("CREATE TABLE empty_t(id INT)", 0))
		p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
		p.Step(t, fixture.Select("SELECT id FROM empty_t LIMIT nextval('s')", []string{"id"}, []string{"INT4"}, referenceLimitIDs()))
		p.Step(t, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(2)))
		step := fixture.ImplicitError("SELECT id FROM empty_t LIMIT -1", fixture.Diagnostic{Code: "2201W", Message: "LIMIT must not be negative"})
		step.SelectRows = true
		step.ErrorStage = "execute"
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	})
	for _, suffix := range []string{"LIMIT 0", "OFFSET 1", "LIMIT 1"} {
		for _, prepared := range []bool{false, true} {
			t.Run("selected singleton/"+suffix+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceLimitFixture(t)
				p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
				want := referenceLimitIDs()
				next := int64(1)
				if suffix == "LIMIT 1" {
					want = referenceLimitIDs(1)
					next = 2
				}
				if suffix == "OFFSET 1" {
					next = 2
				}
				step := fixture.Select("SELECT nextval('s') "+suffix, []string{"nextval"}, []string{"INT8"}, want)
				step.Prepared = prepared
				p.Step(t, step)
				p.Step(t, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(next)))
			})
		}
	}
}

func TestReferenceQueryLimitOrderCache(t *testing.T) {
	for _, key := range []string{"n", "1", "nextval('s')"} {
		for _, prepared := range []bool{false, true} {
			t.Run(key+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceLimitFixture(t)
				p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
				step := fixture.Select("SELECT nextval('s') AS n FROM t ORDER BY "+key+" DESC OFFSET 1 LIMIT 2", []string{"n"}, []string{"INT8"}, referenceLimitIDs(3, 2))
				step.Prepared = prepared
				p.Step(t, step)
				p.Step(t, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(5)))
			})
		}
	}
}

func TestReferenceQueryLimitViews(t *testing.T) {
	for _, barrier := range []string{"", " WITH (security_barrier=true)"} {
		for _, suffix := range []string{"LIMIT 0", "LIMIT 1"} {
			for _, prepared := range []bool{false, true} {
				t.Run(barrier+suffix+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					p := referenceLimitFixture(t)
					p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
					p.Step(t, fixture.Exec("CREATE VIEW inner_v"+barrier+" AS SELECT id,nextval('s') AS n FROM t", 0))
					p.Step(t, fixture.Exec("CREATE VIEW outer_v AS SELECT id,n FROM inner_v", 0))
					want := referenceLimitIDs(1)
					next := int64(2)
					if suffix == "LIMIT 0" {
						want = referenceLimitIDs()
						next = 1
					}
					step := fixture.Select("SELECT n FROM outer_v "+suffix, []string{"n"}, []string{"INT8"}, want)
					step.Prepared = prepared
					p.Step(t, step)
					p.Step(t, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(next)))
				})
			}
		}
	}
	t.Run("persistent decorated definition", func(t *testing.T) {
		p := referenceLimitFixture(t)
		p.Step(t, fixture.Exec("CREATE VIEW limited AS SELECT id FROM t ORDER BY id OFFSET 1 FETCH NEXT 2 ROWS ONLY", 0))
		step := fixture.Select("SELECT id FROM limited ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(2, 3))
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
		p.Step(t, fixture.Exec("ALTER TABLE t RENAME COLUMN id TO renamed", 0))
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	})
}

func TestReferenceQueryLimitInsert(t *testing.T) {
	p := referenceLimitFixture(t)
	p.Step(t, fixture.Exec("CREATE TABLE dst(id INT)", 0))
	for _, step := range []fixture.Step{
		fixture.Exec("INSERT INTO dst SELECT id FROM t ORDER BY id OFFSET 1 LIMIT 2", 2),
		fixture.Exec("INSERT INTO dst SELECT id FROM t LIMIT 0", 0),
		fixture.Exec("INSERT INTO dst VALUES(3),(1),(2) ORDER BY column1 OFFSET 1 FETCH NEXT 1 ROW ONLY", 1),
		fixture.Exec("INSERT INTO dst (VALUES(8),(9) LIMIT 1)", 1),
		fixture.Exec("INSERT INTO dst (SELECT 10 LIMIT 0)", 0),
	} {
		p.Step(t, step)
	}
	step := fixture.Select("SELECT id FROM dst ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(2, 2, 3, 8))
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
}
