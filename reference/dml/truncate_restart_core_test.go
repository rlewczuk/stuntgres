package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateRestartCore(t *testing.T) {
	truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
		for _, q := range []string{
			"CREATE TABLE serial_t(id SERIAL PRIMARY KEY)",
			"CREATE INDEX serial_extra ON serial_t(id)",
			"CREATE TABLE identity_t(id INT GENERATED ALWAYS AS IDENTITY(START WITH 44))",
			"CREATE TABLE owned_t(id INT)",
			"CREATE SEQUENCE explicit_s START 33 OWNED BY owned_t.id",
			"ALTER TABLE owned_t ALTER COLUMN id SET DEFAULT nextval('explicit_s')",
			"CREATE TABLE descending_t(id INT)",
			"CREATE SEQUENCE descending_s INCREMENT -2 START -7 OWNED BY descending_t.id",
			"ALTER TABLE descending_t ALTER COLUMN id SET DEFAULT nextval('descending_s')",
		} {
			truncateExec(t, f, f.A, q, 0)
		}
		for _, table := range []string{"serial_t", "identity_t", "owned_t", "descending_t"} {
			truncateExec(t, f, f.A, "INSERT INTO "+table+" DEFAULT VALUES", 1)
			truncateExec(t, f, f.A, "INSERT INTO "+table+" DEFAULT VALUES", 1)
		}
		q := "TRUNCATE serial_t,identity_t,owned_t,descending_t,serial_t RESTART IDENTITY"
		x := truncateRestartRoute(t, p, f.A, q, held)
		truncateRows(t, f, f.A, "serial_t", 1, 2) // Prepare has no effects.
		truncateExec(t, f, x, q, 0)
		for _, s := range []struct {
			name  string
			start int64
		}{{"serial_t_id_seq", 1}, {"identity_t_id_seq", 44}, {"explicit_s", 33}, {"descending_s", -7}} {
			truncateRestartState(t, f, f.A, s.name, s.start, false)
		}
		for _, s := range []struct {
			name  string
			value int64
		}{{"serial_t", 1}, {"identity_t", 44}, {"owned_t", 33}, {"descending_t", -7}} {
			truncateRows(t, f, f.A, s.name)
			truncateExec(t, f, f.A, "INSERT INTO "+s.name+" DEFAULT VALUES", 1)
			truncateRows(t, f, f.A, s.name, s.value)
		}
		truncateRun(t, f, f.A, fixture.Select("SELECT indexname,indexdef FROM pg_indexes WHERE schemaname='public' AND tablename='serial_t' ORDER BY indexname", []string{"indexname", "indexdef"}, []string{"NAME", "TEXT"}, [][]any{
			{"serial_extra", "CREATE INDEX serial_extra ON public.serial_t USING btree (id)"},
			{"serial_t_pkey", "CREATE UNIQUE INDEX serial_t_pkey ON public.serial_t USING btree (id)"},
		}))
		// Same genuinely held utility works repeatedly; indexes still enforce keys.
		truncateExec(t, f, x, q, 0)
		truncateExec(t, f, f.A, "INSERT INTO serial_t DEFAULT VALUES", 1)
		d := truncateWant("23505", `duplicate key value violates unique constraint "serial_t_pkey"`)
		d.Detail = "Key (id)=(1) already exists."
		d.Schema, d.Table, d.Constraint = "public", "serial_t", "serial_t_pkey"
		truncateRestartError(t, f, f.A, "INSERT INTO serial_t VALUES(1)", d)
		// Utility queries have no result columns, including a real prepared route.
		truncateRun(t, f, x, fixture.Select(q, []string{}, []string{}, [][]any{}))
	})
}

func TestReferenceTruncateRestartOwnership(t *testing.T) {
	truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
		for _, q := range []string{
			"CREATE TABLE t(id INT,other INT)",
			"CREATE SEQUENCE owned_no_default START 33 OWNED BY t.id",
			"CREATE SEQUENCE default_only START 70",
			"ALTER TABLE t ALTER COLUMN other SET DEFAULT nextval('default_only')",
			"CREATE SEQUENCE detached START 90 OWNED BY t.other",
			"ALTER SEQUENCE detached OWNED BY NONE",
			"CREATE SEQUENCE t_id_seq START 110",
		} {
			truncateExec(t, f, f.A, q, 0)
		}
		for _, s := range []struct {
			name  string
			value int64
		}{{"owned_no_default", 33}, {"default_only", 70}, {"detached", 90}, {"t_id_seq", 110}} {
			truncateRestartValue(t, f, f.A, "SELECT nextval('"+s.name+"')", "nextval", s.value)
		}
		truncateExec(t, f, f.A, "INSERT INTO t(id) VALUES(7)", 1)
		q := "TRUNCATE t RESTART IDENTITY"
		x := truncateRestartRoute(t, p, f.A, q, held)
		truncateExec(t, f, x, q, 0)
		truncateRestartState(t, f, f.A, "owned_no_default", 33, false)
		for _, s := range []struct {
			name  string
			value int64
		}{{"owned_no_default", 33}, {"default_only", 72}, {"detached", 91}, {"t_id_seq", 111}} {
			// INSERT demanded default_only=71 before the reset.
			truncateRestartValue(t, f, f.A, "SELECT nextval('"+s.name+"')", "nextval", s.value)
		}
		truncateRows(t, f, f.A, "t")
	})
}

func TestReferenceTruncateRestartClosure(t *testing.T) {
	t.Run("Inheritance", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			for _, q := range []string{"CREATE TABLE parent(id INT)", "CREATE TABLE child() INHERITS(parent)", "CREATE SEQUENCE parent_s START 10 OWNED BY parent.id", "CREATE SEQUENCE child_s START 20 OWNED BY child.id"} {
				truncateExec(t, f, f.A, q, 0)
			}
			truncateExec(t, f, f.A, "INSERT INTO parent VALUES(1)", 1)
			truncateExec(t, f, f.A, "INSERT INTO child VALUES(2)", 1)
			truncateRestartValue(t, f, f.A, "SELECT nextval('parent_s')", "nextval", 10)
			truncateRestartValue(t, f, f.A, "SELECT nextval('child_s')", "nextval", 20)
			q := "TRUNCATE ONLY parent RESTART IDENTITY"
			truncateExec(t, f, truncateRestartRoute(t, p, f.A, q, held), q, 0)
			truncateRows(t, f, f.A, "child", 2)
			truncateRestartValue(t, f, f.A, "SELECT nextval('parent_s')", "nextval", 10)
			truncateRestartValue(t, f, f.A, "SELECT nextval('child_s')", "nextval", 21)
			q = "TRUNCATE parent * RESTART IDENTITY"
			truncateExec(t, f, truncateRestartRoute(t, p, f.A, q, held), q, 0)
			truncateRows(t, f, f.A, "parent")
			truncateRestartValue(t, f, f.A, "SELECT nextval('parent_s')", "nextval", 10)
			truncateRestartValue(t, f, f.A, "SELECT nextval('child_s')", "nextval", 20)
		})
	})
	t.Run("Partitions", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			for _, q := range []string{"CREATE TABLE root(id INT) PARTITION BY RANGE(id)", "CREATE TABLE low PARTITION OF root FOR VALUES FROM(0) TO(10)", "CREATE TABLE high PARTITION OF root FOR VALUES FROM(10) TO(20)", "CREATE SEQUENCE root_s START 30 OWNED BY root.id", "CREATE SEQUENCE low_s START 40 OWNED BY low.id", "CREATE SEQUENCE high_s START 50 OWNED BY high.id"} {
				truncateExec(t, f, f.A, q, 0)
			}
			truncateExec(t, f, f.A, "INSERT INTO root VALUES(1),(11)", 2)
			for _, s := range []struct {
				name  string
				start int64
			}{{"root_s", 30}, {"low_s", 40}, {"high_s", 50}} {
				truncateRestartValue(t, f, f.A, "SELECT nextval('"+s.name+"')", "nextval", s.start)
			}
			q := "TRUNCATE low RESTART IDENTITY"
			truncateExec(t, f, truncateRestartRoute(t, p, f.A, q, held), q, 0)
			truncateRows(t, f, f.A, "high", 11)
			truncateRestartValue(t, f, f.A, "SELECT nextval('root_s')", "nextval", 31)
			truncateRestartValue(t, f, f.A, "SELECT nextval('low_s')", "nextval", 40)
			truncateRestartValue(t, f, f.A, "SELECT nextval('high_s')", "nextval", 51)
			q = "TRUNCATE root RESTART IDENTITY"
			truncateExec(t, f, truncateRestartRoute(t, p, f.A, q, held), q, 0)
			truncateRows(t, f, f.A, "root")
			for _, s := range []struct {
				name  string
				start int64
			}{{"root_s", 30}, {"low_s", 40}, {"high_s", 50}} {
				truncateRestartValue(t, f, f.A, "SELECT nextval('"+s.name+"')", "nextval", s.start)
			}
		})
	})
	t.Run("Cascade", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			for _, q := range []string{"CREATE TABLE outbound(id SERIAL PRIMARY KEY)", "CREATE TABLE parent(id SERIAL PRIMARY KEY,oid INT REFERENCES outbound)", "CREATE TABLE child(id SERIAL,pid INT REFERENCES parent)"} {
				truncateExec(t, f, f.A, q, 0)
			}
			truncateExec(t, f, f.A, "INSERT INTO outbound DEFAULT VALUES", 1)
			truncateExec(t, f, f.A, "INSERT INTO parent(oid) VALUES(1)", 1)
			truncateExec(t, f, f.A, "INSERT INTO child(pid) VALUES(1)", 1)
			q := "TRUNCATE parent RESTART IDENTITY CASCADE"
			truncateExec(t, f, truncateRestartRoute(t, p, f.A, q, held), q, 0)
			truncateNotices(t, f, "child")
			truncateRows(t, f, f.A, "parent")
			truncateRows(t, f, f.A, "child")
			truncateRows(t, f, f.A, "outbound", 1)
			truncateRestartValue(t, f, f.A, "SELECT nextval('parent_id_seq')", "nextval", 1)
			truncateRestartValue(t, f, f.A, "SELECT nextval('child_id_seq')", "nextval", 1)
			truncateRestartValue(t, f, f.A, "SELECT nextval('outbound_id_seq')", "nextval", 2)
		})
	})
	t.Run("ReciprocalTemporary", func(t *testing.T) {
		truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
			truncateExec(t, f, f.A, "CREATE TABLE parent(id INT)", 0)
			for _, c := range []fixture.Executor{f.A, f.B} {
				truncateExec(t, f, c, "CREATE TEMP TABLE local_t() INHERITS(parent)", 0)
				truncateExec(t, f, c, "CREATE TEMP SEQUENCE local_s START 60 OWNED BY local_t.id", 0)
				truncateExec(t, f, c, "INSERT INTO local_t VALUES(7)", 1)
				truncateRestartValue(t, f, c, "SELECT nextval('local_s')", "nextval", 60)
			}
			q := "TRUNCATE parent RESTART IDENTITY"
			defer f.A.ExecContext(p.Ctx, "ROLLBACK")
			defer f.B.ExecContext(p.Ctx, "ROLLBACK")
			truncateExec(t, f, f.A, "SET lock_timeout='2s'", 0)
			truncateExec(t, f, f.B, "SET lock_timeout='2s'", 0)
			truncateExec(t, f, f.A, "BEGIN", 0)
			truncateExec(t, f, truncateRestartRoute(t, p, f.A, q, held), q, 0)
			truncateRows(t, f, f.A, "local_t")
			truncateRows(t, f, f.B, "local_t", 7)
			truncateRestartValue(t, f, f.A, "SELECT nextval('local_s')", "nextval", 60)
			truncateRestartValue(t, f, f.B, "SELECT nextval('local_s')", "nextval", 61)
			// A still owns its successful reset AX: B's physical TEMP branch
			// remains usable, proving it was excluded from lock acquisition too.
			truncateExec(t, f, f.A, "COMMIT", 0)
			truncateExec(t, f, f.B, "BEGIN", 0)
			truncateExec(t, f, truncateRestartRoute(t, p, f.B, q, held), q, 0)
			truncateRestartValue(t, f, f.A, "SELECT nextval('local_s')", "nextval", 61)
			truncateRestartValue(t, f, f.B, "SELECT nextval('local_s')", "nextval", 60)
			truncateExec(t, f, f.B, "COMMIT", 0)
		})
	})
}
