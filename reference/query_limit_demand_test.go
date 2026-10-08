package reference_test

import "testing"

func TestReferenceQueryLimitDemand(t *testing.T) {
	for _, query := range []string{
		"SELECT 10/v AS n FROM t LIMIT 1",
		"SELECT 10/v AS n FROM t WHERE 10/v > 0 LIMIT 1",
		"SELECT 10/t.v AS n FROM t CROSS JOIN u WHERE 10/t.v > 0 LIMIT 1",
		"SELECT 10/v AS n FROM t WHERE 10/v > 0 ORDER BY 10/v LIMIT 0",
	} {
		t.Run(query, func(t *testing.T) {
			p := referenceLimitFixture(t)
			p.step(t, referenceExec("CREATE TABLE u(x INT)", 0))
			p.step(t, referenceExec("INSERT INTO u VALUES(1)", 1))
			want := referenceLimitIDs(5)
			if query == "SELECT 10/v AS n FROM t WHERE 10/v > 0 ORDER BY 10/v LIMIT 0" {
				want = referenceLimitIDs()
			}
			step := referenceSelect(query, []string{"n"}, []string{"INT4"}, want)
			p.step(t, step)
			p.step(t, referencePrepared(step))
		})
	}
	for _, key := range []string{"id", "n"} {
		t.Run("sort evaluates cheap target/"+key, func(t *testing.T) {
			p := referenceLimitFixture(t)
			step := referenceImplicitError("SELECT 10/v AS n FROM t ORDER BY "+key+" LIMIT 1", referenceDiagnostic{Code: "22012", Message: "division by zero"})
			step.selectRows = true
			step.errorStage = "execute"
			p.step(t, step)
			p.step(t, referencePrepared(step))
		})
	}
	for _, suffix := range []string{"LIMIT 0", "OFFSET 1 LIMIT 1", "LIMIT 1"} {
		for _, prepared := range []bool{false, true} {
			t.Run("projection/"+suffix+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceLimitFixture(t)
				p.step(t, referenceExec("CREATE SEQUENCE s START WITH 1", 0))
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
				step := referenceSelect("SELECT nextval('s') AS n FROM t ORDER BY id "+suffix, []string{"n"}, []string{"INT8"}, want)
				step.prepared = prepared
				p.step(t, step)
				p.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(next)))
			})
		}
	}
	t.Run("held bound evaluates once offset first", func(t *testing.T) {
		p := referenceLimitFixture(t)
		p.step(t, referenceExec("CREATE SEQUENCE s START WITH 1", 0))
		for _, backend := range p.backends() {
			t.Run(backend.name, func(t *testing.T) {
				stmt, err := backend.db.PrepareContext(p.ctx, "SELECT id FROM t ORDER BY id LIMIT nextval('s') OFFSET nextval('s')")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, want := range [][]int64{{2, 3}, {4}} {
					rows, err := stmt.QueryContext(p.ctx)
					referenceViewValues(t, relationOutcome{rows: rows, err: err}, "id", "INT4", want...)
				}
				referenceViewValues(t, referenceRowQuery(p.ctx, backend.db, "SELECT nextval('s')", false), "nextval", "INT8", 5)
			})
		}
	})
	t.Run("empty still evaluates bound", func(t *testing.T) {
		p := referenceLimitFixture(t)
		p.step(t, referenceExec("CREATE TABLE empty_t(id INT)", 0))
		p.step(t, referenceExec("CREATE SEQUENCE s START WITH 1", 0))
		p.step(t, referenceSelect("SELECT id FROM empty_t LIMIT nextval('s')", []string{"id"}, []string{"INT4"}, referenceLimitIDs()))
		p.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(2)))
		step := referenceImplicitError("SELECT id FROM empty_t LIMIT -1", referenceDiagnostic{Code: "2201W", Message: "LIMIT must not be negative"})
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		p.step(t, referencePrepared(step))
	})
	for _, suffix := range []string{"LIMIT 0", "OFFSET 1", "LIMIT 1"} {
		for _, prepared := range []bool{false, true} {
			t.Run("selected singleton/"+suffix+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceLimitFixture(t)
				p.step(t, referenceExec("CREATE SEQUENCE s START WITH 1", 0))
				want := referenceLimitIDs()
				next := int64(1)
				if suffix == "LIMIT 1" {
					want = referenceLimitIDs(1)
					next = 2
				}
				if suffix == "OFFSET 1" {
					next = 2
				}
				step := referenceSelect("SELECT nextval('s') "+suffix, []string{"nextval"}, []string{"INT8"}, want)
				step.prepared = prepared
				p.step(t, step)
				p.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(next)))
			})
		}
	}
}

func TestReferenceQueryLimitOrderCache(t *testing.T) {
	for _, key := range []string{"n", "1", "nextval('s')"} {
		for _, prepared := range []bool{false, true} {
			t.Run(key+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceLimitFixture(t)
				p.step(t, referenceExec("CREATE SEQUENCE s START WITH 1", 0))
				step := referenceSelect("SELECT nextval('s') AS n FROM t ORDER BY "+key+" DESC OFFSET 1 LIMIT 2", []string{"n"}, []string{"INT8"}, referenceLimitIDs(3, 2))
				step.prepared = prepared
				p.step(t, step)
				p.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(5)))
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
					p.step(t, referenceExec("CREATE SEQUENCE s START WITH 1", 0))
					p.step(t, referenceExec("CREATE VIEW inner_v"+barrier+" AS SELECT id,nextval('s') AS n FROM t", 0))
					p.step(t, referenceExec("CREATE VIEW outer_v AS SELECT id,n FROM inner_v", 0))
					want := referenceLimitIDs(1)
					next := int64(2)
					if suffix == "LIMIT 0" {
						want = referenceLimitIDs()
						next = 1
					}
					step := referenceSelect("SELECT n FROM outer_v "+suffix, []string{"n"}, []string{"INT8"}, want)
					step.prepared = prepared
					p.step(t, step)
					p.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, referenceLimitIDs(next)))
				})
			}
		}
	}
	t.Run("persistent decorated definition", func(t *testing.T) {
		p := referenceLimitFixture(t)
		p.step(t, referenceExec("CREATE VIEW limited AS SELECT id FROM t ORDER BY id OFFSET 1 FETCH NEXT 2 ROWS ONLY", 0))
		step := referenceSelect("SELECT id FROM limited ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(2, 3))
		p.step(t, step)
		p.step(t, referencePrepared(step))
		p.step(t, referenceExec("ALTER TABLE t RENAME COLUMN id TO renamed", 0))
		p.step(t, step)
		p.step(t, referencePrepared(step))
	})
}

func TestReferenceQueryLimitInsert(t *testing.T) {
	p := referenceLimitFixture(t)
	p.step(t, referenceExec("CREATE TABLE dst(id INT)", 0))
	for _, step := range []referenceStep{
		referenceExec("INSERT INTO dst SELECT id FROM t ORDER BY id OFFSET 1 LIMIT 2", 2),
		referenceExec("INSERT INTO dst SELECT id FROM t LIMIT 0", 0),
		referenceExec("INSERT INTO dst VALUES(3),(1),(2) ORDER BY column1 OFFSET 1 FETCH NEXT 1 ROW ONLY", 1),
		referenceExec("INSERT INTO dst (VALUES(8),(9) LIMIT 1)", 1),
		referenceExec("INSERT INTO dst (SELECT 10 LIMIT 0)", 0),
	} {
		p.step(t, step)
	}
	step := referenceSelect("SELECT id FROM dst ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(2, 2, 3, 8))
	p.step(t, step)
	p.step(t, referencePrepared(step))
}
