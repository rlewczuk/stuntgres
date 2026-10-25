package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func loScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func loRun(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
}

func loInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	loRun(t, p, b, db, fixture.Exec("CREATE EXTENSION lo", 0))
}

func loAbsent(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	loRun(t, p, b, db, fixture.Select("SELECT extname FROM pg_extension WHERE extname='lo'", []string{"extname"}, []string{"NAME"}, [][]any{}))
	loRun(t, p, b, db, fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('lo','_lo') ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{}))
}

func loHeld(t *testing.T, p *fixture.Pair, db fixture.Executor, q string) fixture.HeldStatement {
	t.Helper()
	s, err := db.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return fixture.HeldStatement{Executor: db, Statement: s, SQL: q}
}

func loRows(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.OidReferenceRows(t, p.Ctx, b, db, s)
}

func loColumnMetadata(t *testing.T, pg bool, columns []*sql.ColumnType, names []string, nullable []bool) {
	t.Helper()
	if len(columns) != len(names) {
		t.Fatal("column count", len(columns), names)
	}
	for i, c := range columns {
		scan := reflect.TypeFor[int64]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != names[i] || c.DatabaseTypeName() != "OID" || c.ScanType() != scan {
			t.Fatalf("column %d: name=%s type=%s scan=%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType())
		}
		if _, ok := c.Length(); ok {
			t.Fatal("lo length")
		}
		if _, _, ok := c.DecimalSize(); ok {
			t.Fatal("lo decimal size")
		}
		n, known := c.Nullable()
		wantKnown := !pg && nullable != nil
		wantNullable := wantKnown && nullable[i]
		if n != wantNullable || known != wantKnown {
			t.Fatalf("%s nullable=%v,%v want %v,%v", c.Name(), n, known, wantNullable, wantKnown)
		}
	}
}

func loMetadataRows(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, s fixture.Step, nullable []bool) {
	t.Helper()
	loRows(t, p, b, db, s)
	if s.Prepared {
		held := loHeld(t, p, db, s.Query)
		db = held
	}
	rows, err := db.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder), s.Query, s.Args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	loColumnMetadata(t, b.PG, columns, s.Columns, nullable)
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	loColumnMetadata(t, b.PG, columns, s.Columns, nullable)
}

func loInputDiagnostic(input, code string) fixture.Diagnostic {
	message := `invalid input syntax for type oid: "` + input + `"`
	if code == "22003" {
		message = `value "` + input + `" is out of range for type oid`
	}
	return fixture.ControlsReferenceDiagnostic(code, message)
}

func loBoundError(q, input, code, parameter string, args ...any) fixture.Step {
	d := loInputDiagnostic(input, code)
	d.Context = "unnamed portal parameter $" + parameter + " = '...'"
	if input == "" {
		d.Context = "unnamed portal parameter $" + parameter + " = ''"
	}
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.ErrorStage = "execute"
	return s
}
