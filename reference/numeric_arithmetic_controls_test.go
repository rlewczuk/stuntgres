package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
)

func numericArithmeticStored(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
	run(referenceExec("CREATE TABLE stored(id INT,n NUMERIC,s NUMERIC(8,3),l NUMERIC DEFAULT 2::numeric^3::numeric^2::numeric,r NUMERIC DEFAULT 2::numeric^(3::numeric^2::numeric),CHECK (l+0::numeric=64::numeric))", 0))
	run(referenceExec("INSERT INTO stored(id,n,s) VALUES(1,0.1+0.2,1.200),(2,1.20*2.300,NULL),(3,9223372036854775808+1,NULL),(4,1.0/3,NULL),(5,2::numeric^4.2,NULL),(6,'NaN'::numeric,NULL),(7,'Infinity'::numeric,NULL),(8,'-Infinity'::numeric,NULL),(9,NULL,NULL)", 9))
	run(referenceExec("CREATE VIEW powers AS SELECT id,l+0::numeric AS left_power,2::numeric^(3::numeric^2::numeric) AS right_power FROM stored", 0))
	values := referenceSelect("SELECT id,n,s,l,r FROM stored ORDER BY id", []string{"id", "n", "s", "l", "r"}, []string{"INT4", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC"}, [][]any{
		{int64(1), "0.3", "1.200", "64.0000000000000000", "512.0000000000000000"},
		{int64(2), "2.76000", nil, "64.0000000000000000", "512.0000000000000000"},
		{int64(3), "9223372036854775809", nil, "64.0000000000000000", "512.0000000000000000"},
		{int64(4), "0.33333333333333333333", nil, "64.0000000000000000", "512.0000000000000000"},
		{int64(5), "18.379173679952560", nil, "64.0000000000000000", "512.0000000000000000"},
		{int64(6), "NaN", nil, "64.0000000000000000", "512.0000000000000000"},
		{int64(7), "Infinity", nil, "64.0000000000000000", "512.0000000000000000"},
		{int64(8), "-Infinity", nil, "64.0000000000000000", "512.0000000000000000"},
		{int64(9), nil, nil, "64.0000000000000000", "512.0000000000000000"},
	})
	run(values)
	run(referencePrepared(values))
	view := referenceSelect("SELECT left_power,right_power FROM powers WHERE id=1", []string{"left_power", "right_power"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"64.0000000000000000", "512.0000000000000000"}})
	run(view)
	run(referencePrepared(view))
	t.Run("symbolic deparse replay", func(t *testing.T) {
		for _, pretty := range []string{"false", "true"} {
			t.Run(pretty, func(t *testing.T) {
				referenceViewCopySQL(t, p, b, "powers_copy", "SELECT pg_get_viewdef('powers'::regclass,"+pretty+") AS definition FROM stored WHERE id=1")
				copy := referenceSelect("SELECT left_power,right_power FROM powers_copy WHERE id=1", view.columns, view.types, view.rows)
				referenceRunStep(t, p.ctx, b.db, b.recorder, copy)
				referenceRunStep(t, p.ctx, b.db, b.recorder, referencePrepared(copy))
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("DROP VIEW powers_copy", 0))
			})
		}
		numericArithmeticDefaultReplay(t, p, b)
	})
	// Replaying the selected symbolic SQL is independent of any dump formatting.
	run(referenceExec("CREATE TABLE replay(id INT,l NUMERIC DEFAULT 2::numeric^3::numeric^2::numeric,r NUMERIC DEFAULT 2::numeric^(3::numeric^2::numeric),CHECK(l+0::numeric=64::numeric))", 0))
	run(referenceExec("INSERT INTO replay(id) VALUES(1)", 1))
	run(referenceSelect("SELECT l,r FROM replay", []string{"l", "r"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"64.0000000000000000", "512.0000000000000000"}}))
	if b.pg {
		return
	} // Native controls have no PostgreSQL API equivalent.
	stmt, err := b.db.PrepareContext(p.ctx, view.query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		cursor, err := raw.(driver.QueryerContext).QueryContext(p.ctx, "SELECT n+0::numeric FROM stored WHERE id=1", nil)
		if err != nil {
			return err
		}
		defer cursor.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			return err
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		var again bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &again); err != nil {
			return err
		}
		if !bytes.Equal(dump.Bytes(), again.Bytes()) {
			t.Fatal("numeric/symbolic redump changed")
		}
		run(values)
		run(referencePrepared(values))
		run(view)
		referenceRunStep(t, p.ctx, referenceHeldStatement{b.db, stmt, view.query}, b.recorder, view)
		for _, tc := range []struct{ tail, code, message string }{
			{"1::numeric/0", "22012", "division by zero"}, {"(-2)::numeric^0.5", "2201F", "a negative number raised to a non-integer power yields a complex result"}, {"1e131071::numeric*10", "22003", "value overflows numeric format"},
		} {
			script := "CREATE TABLE replacement(n NUMERIC); INSERT INTO replacement VALUES(0.1+0.2),(9223372036854775808+1); INSERT INTO replacement VALUES(" + tc.tail + ")"
			want := referenceImplicitError(script, referenceDiagnostic{Code: tc.code, Message: tc.message})
			referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)), want.diagnostic)
			var preserved bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &preserved); err != nil {
				return err
			}
			if !bytes.Equal(preserved.Bytes(), dump.Bytes()) {
				t.Fatal("failed staged arithmetic restore published")
			}
			run(values)
		}
		script := "CREATE TABLE replacement(n NUMERIC); INSERT INTO replacement VALUES(/*é*/(0.1::numeric+$1)^2)"
		position := len([]rune(script[:strings.Index(script, "$1")])) + 1
		want := referenceImplicitError(script, referenceDiagnostic{Code: "42P02", Message: "parameters are not allowed in restore", Position: strconv.Itoa(position)})
		referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)), want.diagnostic)
		run(values)
		// Symbolic defaults execute again, rather than being replaced with old values.
		run(referenceExec("INSERT INTO stored(id,n,s) VALUES(10,0.1+0.2,1.2)", 1))
		run(referenceSelect("SELECT l,r FROM stored WHERE id=10", []string{"l", "r"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"64.0000000000000000", "512.0000000000000000"}}))
		meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
		if meta.ColumnTypeDatabaseTypeName(0) != "NUMERIC" {
			t.Fatal("retained computed type changed")
		}
		dest := make([]driver.Value, 1)
		if err := cursor.Next(dest); err != nil || !reflect.DeepEqual(dest, []driver.Value{"0.3"}) {
			return fmt.Errorf("retained computed row %v: %w", dest, err)
		}
		if err := cursor.Next(dest); err != io.EOF {
			return fmt.Errorf("retained cursor end: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The dump's supported SQL also replays through the PG public SQL route.
	if p.postgres != nil {
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, "DROP VIEW IF EXISTS powers; DROP TABLE IF EXISTS stored; DROP TABLE IF EXISTS replay"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, view)
		if _, err := p.postgres.ExecContext(p.ctx, "DROP VIEW powers; DROP TABLE stored; DROP TABLE replay"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceNumericArithmeticNativeControlsAndStoredExpressions(t *testing.T) {
	numericArithmeticBackends(t, numericArithmeticStored)
}

func TestReferenceNumericArithmeticFeatureSmoke(t *testing.T) {
	t.Run("exact", TestReferenceNumericArithmeticExactValues)
	t.Run("division", TestReferenceNumericArithmeticDivisionRemainderScale)
	t.Run("grammar", TestReferenceNumericArithmeticPowerGrammar)
	t.Run("precision", TestReferenceNumericArithmeticPowerPrecision)
	t.Run("diagnostics", TestReferenceNumericArithmeticDiagnosticsAndBindings)
	t.Run("held", TestReferenceNumericArithmeticParametersAndMetadata)
	t.Run("atomicity", TestReferenceNumericArithmeticAtomicityAndCancellation)
	t.Run("stored", TestReferenceNumericArithmeticNativeControlsAndStoredExpressions)
}

// Replay captured DEFAULT expressions, asserting grouping by its numeric effect
// instead of pinning deparser whitespace or redundant casts/parentheses.
func numericArithmeticDefaultReplay(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	const q = "SELECT pg_get_expr(d.adbin,d.adrelid) FROM pg_attrdef d JOIN pg_class c ON d.adrelid=c.oid WHERE c.relname='stored' ORDER BY d.adnum"
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("defaults/prepared%t", prepared), func(t *testing.T) {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, b.recorder)
			var exec referenceExecutor = b.db
			if prepared {
				stmt, err := b.db.PrepareContext(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				exec = referenceHeldStatement{b.db, stmt, q}
			}
			rows, err := exec.QueryContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.ColumnTypes()
			if err != nil || len(columns) != 1 || columns[0].Name() != "pg_get_expr" || columns[0].DatabaseTypeName() != "TEXT" || columns[0].ScanType() != reflect.TypeFor[string]() {
				t.Fatal(columns, err)
			}
			definitions := make([]string, 0, 2)
			for rows.Next() {
				var raw any
				if err := rows.Scan(&raw); err != nil {
					t.Fatal(err)
				}
				referenceAssertRawValue(t, b.pg, "TEXT", raw)
				definition, ok := referenceNormalize(raw).(string)
				if !ok || definition == "" {
					t.Fatal("empty/nontext stored definition", raw)
				}
				definitions = append(definitions, definition)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if len(definitions) != 2 {
				t.Fatalf("default definition count=%d want2", len(definitions))
			}
			referenceViewRecorded(t, b.recorder, q, []string{"pg_get_expr"}, []string{"TEXT"}, [][]any{{definitions[0]}, {definitions[1]}}, false)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE captured_defaults(l NUMERIC DEFAULT "+definitions[0]+",r NUMERIC DEFAULT "+definitions[1]+")", 0))
			run(referenceExec("INSERT INTO captured_defaults DEFAULT VALUES", 1))
			values := referenceSelect("SELECT l,r FROM captured_defaults", []string{"l", "r"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{"64.0000000000000000", "512.0000000000000000"}})
			run(values)
			run(referencePrepared(values))
			run(referenceExec("DROP TABLE captured_defaults", 0))
		})
	}
}
