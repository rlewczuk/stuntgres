package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const enabledRolesQuery = "SELECT role_name FROM information_schema.enabled_roles"

type enabledRolesCase struct {
	t *testing.T
	p *fixture.Pair
	b fixture.Backend
	c *sql.Conn
	r *fixture.Recorder
}

func enabledRolesScenario(t *testing.T, roles []string, body func(*enabledRolesCase)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, roles)
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			t.Cleanup(func() {
				defer c.Close()
				for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION"} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			})
			body(&enabledRolesCase{t: t, p: p, b: b, c: c, r: r})
			if len(notices) != 0 {
				t.Fatalf("unexpected notices: %+v", notices)
			}
		})
	}
}

func (s *enabledRolesCase) run(step fixture.Step) {
	s.t.Helper()
	fixture.RunStep(s.t, s.p.Ctx, s.c, s.r, step)
}

func (s *enabledRolesCase) exec(q string) {
	s.t.Helper()
	s.run(fixture.Exec(q, 0))
}

func enabledRolesMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
	t.Helper()
	if len(columns) != 1 {
		t.Fatalf("columns=%d want 1", len(columns))
	}
	c := columns[0]
	length, lengthOK := c.Length()
	precision, scale, decimalOK := c.DecimalSize()
	nullable, nullableOK := c.Nullable()
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if c.Name() != "role_name" || c.DatabaseTypeName() != "NAME" || c.ScanType() != scan || length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
		t.Fatalf("name=%s type=%s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", c.Name(), c.DatabaseTypeName(), c.ScanType(), length, lengthOK, precision, scale, decimalOK, nullable, nullableOK)
	}
}

func (s *enabledRolesCase) projection(query string, names ...string) fixture.Step {
	s.t.Helper()
	var want [][]any
	for _, name := range names {
		want = append(want, []any{name})
	}
	step := fixture.Select(query, []string{"role_name"}, []string{"NAME"}, want)
	step.Bag = true
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		enabledRolesMetadata(t, columns, s.b.PG)
	}
	step.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		text, ok := value.(string)
		if s.b.PG {
			bytes, bytesOK := value.([]byte)
			text, ok = string(bytes), bytesOK
		}
		if !ok {
			t.Fatalf("raw NAME=%T(%v)", value, value)
		}
		for _, name := range names {
			if name == text {
				return true
			}
		}
		t.Fatalf("unexpected raw NAME=%q", text)
		return true
	}
	return step
}

func (s *enabledRolesCase) read(names ...string) {
	s.t.Helper()
	step := s.projection(enabledRolesQuery, names...)
	s.run(step)
	s.run(fixture.Prepared(step))
}

func (s *enabledRolesCase) held(query string) func(fixture.Step) {
	s.t.Helper()
	stmt, err := s.c.PrepareContext(s.p.Ctx, query)
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _ = stmt.Close() })
	held := fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: query}
	return func(step fixture.Step) { fixture.RunStep(s.t, s.p.Ctx, held, s.r, step) }
}

func (s *enabledRolesCase) missing(name string) {
	s.t.Helper()
	d := fixture.ControlsReferenceDiagnostic("42703", `column "`+name+`" does not exist`)
	d.Position = "8"
	for _, prepared := range []bool{false, true} {
		step := fixture.ErrorDiagnostic("SELECT "+name+" FROM information_schema.enabled_roles", d)
		step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
		if prepared {
			step.ErrorStage = "prepare"
		}
		s.run(step)
	}
}
