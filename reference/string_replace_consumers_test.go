package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestReferenceStringReplaceMetadata(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		metadata := func(t *testing.T, rows *sql.Rows) {
			ct, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			if len(ct) != 5 {
				t.Fatal(ct)
			}
			for i, typ := range []string{"TEXT", "TEXT", "TEXT", "BOOL", "BOOL"} {
				scan := reflect.TypeFor[string]()
				if typ == "BOOL" {
					scan = reflect.TypeFor[bool]()
				}
				length, lk := ct[i].Length()
				precision, scale, dk := ct[i].DecimalSize()
				nullable, nk := ct[i].Nullable()
				wantLength, wantLengthKnown := int64(0), false
				if typ == "TEXT" {
					wantLength = 9223372036854775807
					wantLengthKnown = true
				}
				if ct[i].DatabaseTypeName() != typ || ct[i].ScanType() != scan || length != wantLength || lk != wantLengthKnown || dk || nullable || nk {
					t.Fatalf("column %d type=%s scan=%v length=%v/%v decimal=%v/%v/%v nullable=%v/%v", i, ct[i].DatabaseTypeName(), ct[i].ScanType(), length, lk, precision, scale, dk, nullable, nk)
				}
			}
		}
		q := "SELECT overlay($1,$2,$3,$4) AS o,replace($1,$2,'Z') AS r,translate($1,$2,'Z') AS t,starts_with($1,$2) AS f,$1 ^@ $2 AS p"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		for _, tc := range []struct {
			args []any
			row  []any
		}{
			{[]any{"abc", "a", 2, 1}, []any{"aac", "Zbc", "Zbc", true, true}},
			{[]any{"é🙂", "🙂", 1, 0}, []any{"🙂é🙂", "éZ", "éZ", false, false}},
			{[]any{nil, "a", 2, 1}, []any{nil, nil, nil, nil, nil}},
			{[]any{"abc", "a", 2, 1}, []any{"aac", "Zbc", "Zbc", true, true}},
		} {
			s := referenceSelect(q, []string{"o", "r", "t", "f", "p"}, []string{"TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{tc.row}, tc.args...)
			s.checkMetadata = metadata
			referenceRunStep(t, p.ctx, held, b.recorder, s)
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE sr_empty(v text)", 0))
		s := referenceSelect("SELECT overlay(v,'X',1) AS o,replace(v,'a','X') AS r,translate(v,'a','X') AS t,starts_with(v,'a') AS f,v ^@ 'a' AS p FROM sr_empty", []string{"o", "r", "t", "f", "p"}, []string{"TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, nil)
		s.checkMetadata = metadata
		stringReplaceBoth(t, p, b, c, s)
	})
}

func TestReferenceStringReplaceConsumers(t *testing.T) {
	roleReferenceCleanup(t, []string{"sr_reader"})
	stringReplaceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE sr_source(id int,s text)", 0), referenceExec("INSERT INTO sr_source VALUES(1,'ab'),(2,'ac'),(3,NULL)", 3),
			referenceSelect("SELECT id,replace(s,'a','X') AS r,translate(s,'bc','12') AS t,overlay(s,'Z',2,1) AS o FROM sr_source ORDER BY id", []string{"id", "r", "t", "o"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "Xb", "a1", "aZ"}, {int64(2), "Xc", "a2", "aZ"}, {int64(3), nil, nil, nil}}),
			referenceSelect("SELECT id FROM sr_source WHERE starts_with(s,'a') AND s ^@ 'a' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			referenceSelect("SELECT a.id,b.id AS other FROM sr_source a JOIN sr_source b ON starts_with(a.s,b.s) ORDER BY a.id,b.id", []string{"id", "other"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}),
			referenceSelect("SELECT overlay(s,'Z',2,1) AS v,count(*) AS n FROM sr_source GROUP BY overlay(s,'Z',2,1) HAVING starts_with(overlay(s,'Z',2,1),'a') ORDER BY v", []string{"v", "n"}, []string{"TEXT", "INT8"}, [][]any{{"aZ", int64(2)}}),
			referenceSelect("SELECT id FROM sr_source ORDER BY replace(s,'a','X') DESC NULLS LAST", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}, {int64(3)}}),
			referenceSelect("VALUES (replace('ab','a','X'),translate('ab','b','1'),overlay('ab','Z',2),starts_with('ab','a'),'ab' ^@ 'a')", []string{"column1", "column2", "column3", "column4", "column5"}, []string{"TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{"Xb", "a1", "aZ", true, true}}),
		} {
			if s.selectRows {
				stringReplaceBoth(t, p, b, c, s)
			} else {
				referenceRunStep(t, p.ctx, c, b.recorder, s)
			}
		}
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE sr_dest(id int,s text)", 0),
			referencePrepared(referenceExec("INSERT INTO sr_dest SELECT id,replace(s,'a','X') FROM sr_source ORDER BY id", 3)),
			referenceExec("INSERT INTO sr_dest VALUES(4,translate('ab','b','1'))", 1),
			referenceSelect("UPDATE sr_dest SET s=s WHERE id=4 RETURNING id,overlay(s,'Z',2) AS v,starts_with(s,'a') AS f", []string{"id", "v", "f"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(4), "aZ", true}}),
			referencePrepared(referenceSelect("UPDATE sr_dest SET s=overlay(s,'Z',2,1) WHERE s ^@ 'X' RETURNING id,translate(s,'X','a') AS v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "aZ"}, {int64(2), "aZ"}})),
			referenceSelect("DELETE FROM sr_dest WHERE starts_with(s,'a') RETURNING id,replace(s,'a','X') AS v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(4), "X1"}}),
			referenceSelect("SELECT id,s FROM sr_dest ORDER BY id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "XZ"}, {int64(2), "XZ"}, {int64(3), nil}}),
		} {
			if s.selectRows && (s.query[0] == 'U' || s.prepared) {
				s.bag = true
			}
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE ROLE sr_reader", 0))
		if b.pg {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("GRANT USAGE ON SCHEMA public TO sr_reader", 0))
		}
		defer c.ExecContext(p.ctx, "RESET ROLE")
		for _, empty := range []bool{false, true} {
			if empty {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("DELETE FROM sr_source", 3))
			}
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET ROLE sr_reader", 0))
			stringReplaceFailure(t, p, b, c, "SELECT overlay(s,'X',1),replace(NULL::text,'a','X'),starts_with(s,'a'),s ^@ 'a' FROM sr_source", controlsReferenceDiagnostic("42501", "permission denied for table sr_source"), false)
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("RESET ROLE", 0))
		}
	})
}

func TestReferenceStringReplaceAtomicity(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, s := range []referenceStep{
			referenceExec("CREATE TABLE sr_atomic(id int,s text,i int,n int)", 0), referenceExec("INSERT INTO sr_atomic VALUES(1,'abc',2,1),(2,'def',0,1)", 2),
			referenceExec("CREATE TABLE sr_sink(id int,s text)", 0), referenceExec("INSERT INTO sr_sink VALUES(0,'sentinel')", 1),
		} {
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		}
		for _, tc := range []struct {
			query string
			rows  bool
		}{
			{"INSERT INTO sr_sink SELECT id,overlay(s,'X',i,n) FROM sr_atomic ORDER BY id", false},
			{"UPDATE sr_atomic SET s=overlay(s,'X',i,n)", false},
			{"UPDATE sr_atomic SET s='new' RETURNING overlay(s,'X',i,n) AS v", true},
			{"UPDATE sr_atomic SET s=replace(s,'a','Z') RETURNING overlay(s,'X',2-id) AS v", true},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.query+referencePosition(map[bool]int{false: 1, true: 2}[prepared]), func(t *testing.T) {
					var old *sql.Rows
					if !b.pg {
						var err error
						old, err = c.QueryContext(p.ctx, "SELECT id,s FROM sr_atomic ORDER BY id")
						if err != nil {
							t.Fatal(err)
						}
						defer old.Close()
					}
					s := referenceErrorDiagnostic(tc.query, controlsReferenceDiagnostic("22011", "negative substring length not allowed"))
					s.selectRows = tc.rows
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
					}
					referenceRunStep(t, p.ctx, c, b.recorder, s)
					if old != nil {
						stringCoreRetainedRows(t, old, nil, []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "abc"}, {int64(2), "def"}}, false)
					}
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id,s FROM sr_atomic ORDER BY id", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "abc"}, {int64(2), "def"}}))
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id,s FROM sr_sink", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "sentinel"}}))
				})
			}
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("UPDATE sr_atomic SET i=2147483647 WHERE id=2", 1))
		s := referenceErrorDiagnostic("INSERT INTO sr_sink SELECT id,overlay(s,'X',i,n) FROM sr_atomic ORDER BY id", controlsReferenceDiagnostic("22003", "integer out of range"))
		s.errorStage = "execute"
		referenceRunStep(t, p.ctx, c, b.recorder, s)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id,s FROM sr_sink", []string{"id", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "sentinel"}}))
	})
}

func TestReferenceStringReplaceCancellation(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE sr_cancel(v text)", 0))
		q := "INSERT INTO sr_cancel VALUES(overlay(replace(translate('abc','b','X'),'a','Z'),'Q',2))"
		selectSQL := "SELECT overlay('abc','X',2) AS o,replace('abc','a','Z') AS r,translate('abc','a','Z') AS t,starts_with('abc','a') AS f,'abc' ^@ 'a' AS p"
		write, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
		read, err := b.db.PrepareContext(p.ctx, selectSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		for _, exec := range []func() (sql.Result, error){func() (sql.Result, error) { return b.db.ExecContext(ctx, q) }, func() (sql.Result, error) { return write.ExecContext(ctx) }} {
			result, err := exec()
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(result, err)
			}
		}
		for _, query := range []func() (*sql.Rows, error){func() (*sql.Rows, error) { return b.db.QueryContext(ctx, selectSQL) }, func() (*sql.Rows, error) { return read.QueryContext(ctx) }} {
			rows, err := query()
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled query published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT v FROM sr_cancel", []string{"v"}, []string{"TEXT"}, nil))
		stringReplaceBoth(t, p, b, c, referenceSelect(selectSQL, []string{"o", "r", "t", "f", "p"}, []string{"TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{"aXc", "Zbc", "Zbc", true, true}}))
	})
}
