package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceMoneyControlsAndCancellation(t *testing.T) {
	moneyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		moneyReferenceRun(t, p, b,
			referenceExec("CREATE TABLE money_control(id INT,v MONEY DEFAULT MONEY '1.235', CONSTRAINT money_control_check CHECK(v >= MONEY '-92233720368547758.08'))", 0),
			referenceExec("INSERT INTO money_control VALUES(1,'92233720368547758.07'),(2,'-92233720368547758.08'),(3,NULL)", 3),
			referenceExec("INSERT INTO money_control(id) VALUES(4)", 1),
			referenceExec("CREATE VIEW money_view AS SELECT id,v,v/'2'::money AS ratio,CAST(NULL AS money) AS empty FROM money_control", 0))
		want := referenceSelect("SELECT id,v FROM money_control ORDER BY id", []string{"id", "v"}, []string{"INT4", "MONEY"}, [][]any{{int64(1), "$92,233,720,368,547,758.07"}, {int64(2), "-$92,233,720,368,547,758.08"}, {int64(3), nil}, {int64(4), "$1.24"}})
		view := referenceSelect("SELECT v,ratio,empty FROM money_view WHERE id=4", []string{"v", "ratio", "empty"}, []string{"MONEY", "FLOAT8", "MONEY"}, [][]any{{"$1.24", float64(.62), nil}})
		moneyReferenceRun(t, p, b, want, referencePrepared(want), view)
		canceled, cancel := context.WithCancel(p.ctx)
		cancel()
		rows, err := b.db.QueryContext(canceled, "SELECT v FROM money_control")
		if rows != nil {
			rows.Close()
			t.Fatal("precanceled query exposed rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		moneyReferenceRun(t, p, b, want)
		if b.pg {
			return
		} // Native controls have no PostgreSQL API equivalent.
		moneyReferenceNativeControls(t, p, b, want, view)
	})
}

func moneyReferenceNativeControls(t *testing.T, p *referencePair, b referenceBackend, want, view referenceStep) {
	t.Helper()
	moneyReferenceRun(t, p, b, referenceExec("SET lc_monetary TO 'POSIX'", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "lc_monetary") {
		t.Fatal("Dump serialized session settings")
	}
	conn, err := p.native.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Raw(func(raw any) error {
		query := raw.(driver.QueryerContext)
		owned := []byte("1.24")
		cursor, err := query.QueryContext(p.ctx, "SELECT id,v FROM money_control WHERE v=$1", []driver.NamedValue{{Ordinal: 1, Value: owned}})
		if err != nil {
			return err
		}
		defer cursor.Close()
		owned[0] = 'X'
		moneyReferenceRun(t, p, b, referenceExec("INSERT INTO money_control VALUES(5,'2')", 1))
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			return err
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			return err
		}
		if cursor.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(1) != "MONEY" || cursor.(driver.RowsColumnTypeScanType).ColumnTypeScanType(1) != reflect.TypeFor[string]() {
			t.Fatal("retained MONEY metadata changed")
		}
		precision, scale, known := cursor.(driver.RowsColumnTypePrecisionScale).ColumnTypePrecisionScale(1)
		if precision != 0 || scale != 0 || known {
			t.Fatal(precision, scale, known)
		}
		dst := make([]driver.Value, 2)
		if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{int64(4), "$1.24"}) {
			t.Fatal(dst, err)
		}
		if err := cursor.Next(dst); err != io.EOF {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(p.ctx)
		defer cancel()
		cursor, err = query.QueryContext(ctx, "SELECT v FROM money_control", nil)
		if err != nil {
			return err
		}
		defer cursor.Close()
		cancel()
		if err := cursor.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	moneyReferenceRun(t, p, b, want, view, referenceSelect("SHOW lc_monetary", []string{"lc_monetary"}, []string{"TEXT"}, [][]any{{"POSIX"}}))
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
		t.Fatal("money redump differs", err, redump.String())
	}
	for _, script := range []string{
		"CREATE TABLE staged_money(v MONEY); INSERT INTO staged_money VALUES('1'),('oops')",
		"CREATE TABLE staged_money(v MONEY); INSERT INTO staged_money VALUES('1'),('92233720368547758.08')",
		"CREATE TABLE staged_money(v MONEY); INSERT INTO staged_money VALUES('1'); SELECT (",
	} {
		if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err == nil {
			t.Fatal("invalid money Restore succeeded")
		}
		moneyReferenceRun(t, p, b, want, view)
		var unchanged bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &unchanged); err != nil || !bytes.Equal(dump.Bytes(), unchanged.Bytes()) {
			t.Fatal("failed Restore published catalog/rows", err)
		}
	}
	ctx, cancel := context.WithCancel(p.ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	moneyReferenceRun(t, p, b, want, view)
	var afterPrecanceled bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &afterPrecanceled); err != nil || !bytes.Equal(dump.Bytes(), afterPrecanceled.Bytes()) {
		t.Fatal("precanceled Restore changed catalog", err)
	}
	// Restore reads the whole source before parsing SQL. EOF cancellation after
	// consuming actual dump bytes proves reader progress beyond initial API
	// admission, not cancellation after staged SQL execution.
	duringRead, cancelRead := context.WithCancel(p.ctx)
	defer cancelRead()
	if duringRead.Err() != nil || len(dump.Bytes()) == 0 {
		t.Fatal("reader cancellation witness was not initially live")
	}
	reader := &enumCancelReader{Reader: bytes.NewReader(dump.Bytes()), cancel: cancelRead}
	err = stuntgres.Restore(duringRead, p.native, reader)
	var diagnostic interface{ SQLState() string }
	if reader.Len() != 0 || !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) {
		t.Fatal("Restore reader-progress cancellation", reader.Len(), err)
	}
	moneyReferenceRun(t, p, b, want, view)
	var afterReaderCancel bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &afterReaderCancel); err != nil || !bytes.Equal(dump.Bytes(), afterReaderCancel.Bytes()) {
		t.Fatal("reader-canceled Restore changed catalog", err)
	}
	if p.postgres != nil {
		// Replay actual native serialization, not a second hand-written fixture.
		if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, want)
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, view)
		// Leave the fixture empty for the subsequent shared PostgreSQL body.
		if _, err := p.postgres.ExecContext(p.ctx, "DROP VIEW money_view; DROP TABLE money_control"); err != nil {
			t.Fatal(err)
		}
	}
	moneyReferenceRun(t, p, b, referenceExec("INSERT INTO money_control(id) VALUES(6)", 1), referenceSelect("SELECT v FROM money_control WHERE id=6", []string{"v"}, []string{"MONEY"}, [][]any{{"$1.24"}}))
}

func TestReferenceMoneyFeatureSmoke(t *testing.T) {
	moneyReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		moneyReferenceRun(t, p, b, referenceExec("SET lc_monetary TO 'C'", 0), referenceExec("CREATE TABLE money_smoke(id INT,v MONEY)", 0), referenceExec("INSERT INTO money_smoke VALUES(1,'878.08')", 1))
		q := "INSERT INTO money_smoke VALUES($1,$2)"
		stmt, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}
		owned := []byte("$123.455")
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 1, int64(2), owned))
		owned[0] = 'X'
		s := referenceSelect("SELECT v/11::bigint AS truncated,v/11::float8 AS rounded,v/'2'::money AS ratio FROM money_smoke WHERE id=1", []string{"truncated", "rounded", "ratio"}, []string{"MONEY", "MONEY", "FLOAT8"}, [][]any{{"$79.82", "$79.83", float64(439.04)}})
		s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(columns) != 3 {
				t.Fatal(columns)
			}
			for i, typ := range []string{"MONEY", "MONEY", "FLOAT8"} {
				if columns[i].DatabaseTypeName() != typ {
					t.Fatal(columns[i])
				}
				if i < 2 {
					moneyReferenceScanType(t, columns[i], b.pg)
				}
			}
		}
		moneyReferenceRun(t, p, b, s, moneyReferenceValue("'92233720368547758.07'::money::numeric", "NUMERIC", "92233720368547758.07"), moneyReferenceFailure("SELECT '92233720368547758.07'::money+'0.01'::money", "22003", "money out of range", "", "", "execute"))
		want := referenceSelect("SELECT id,v FROM money_smoke ORDER BY id", []string{"id", "v"}, []string{"INT4", "MONEY"}, [][]any{{int64(1), "$878.08"}, {int64(2), "$123.46"}})
		moneyReferenceRun(t, p, b, want)
		referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 1, int64(3), "3"))
		want.rows = append(want.rows, []any{int64(3), "$3.00"})
		moneyReferenceRun(t, p, b, want)
		if !b.pg {
			var dump, redump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			moneyReferenceRun(t, p, b, want)
			if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
				t.Fatal(err, redump.String())
			}
			if p.postgres != nil {
				if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, want)
				if _, err := p.postgres.ExecContext(p.ctx, "DROP TABLE money_smoke"); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}
