package reference_test

import "testing"

func TestReferenceQueryCoreOrderPositionRegression(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT)", 0))
	for _, population := range []string{"empty", "populated"} {
		if population == "populated" {
			p.step(t, referenceExec("INSERT INTO t VALUES(1),(2)", 2))
		}
		t.Run(population, func(t *testing.T) {
			for _, tc := range []struct {
				name, query, position string
			}{
				{"ascii", "SELECT id FROM t ORDER BY missing", "27"},
				// The multibyte comment changes character positions by six.
				{"utf8", "SELECT /*é*/ id FROM t ORDER BY missing", "33"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					step := referenceImplicitError(tc.query, referenceDiagnostic{
						Code: "42703", Message: `column "missing" does not exist`, Position: tc.position,
					})
					step.selectRows = true
					step.errorStage = "execute"
					t.Run("query", func(t *testing.T) { p.step(t, step) })
					step.errorStage = "prepare"
					t.Run("prepare", func(t *testing.T) { p.step(t, referencePrepared(step)) })
				})
			}
		})
	}
}
func TestReferenceQueryCoreProjectionBagsAndMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	queryCoreSeed(t, p)
	for _, step := range []referenceStep{
		referenceSelect("SELECT id,7,NULL,f FROM t ORDER BY id", []string{"id", "?column?", "?column?", "f"}, []string{"INT4", "INT4", "TEXT", "BOOL"}, [][]any{{int64(1), int64(7), nil, true}, {int64(2), int64(7), nil, nil}, {int64(3), int64(7), nil, false}, {int64(4), int64(7), nil, true}}),
		referenceSelect("SELECT id,id FROM t ORDER BY id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}}),
		referenceSelect("SELECT v FROM t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {nil}}),
		referenceSelect("SELECT v FROM t ORDER BY v DESC", []string{"v"}, []string{"INT4"}, [][]any{{nil}, {int64(2)}, {int64(1)}, {int64(1)}}),
	} {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, step := range []referenceStep{
		referenceSelect("SELECT id,7,NULL,f FROM empty", []string{"id", "?column?", "?column?", "f"}, []string{"INT4", "INT4", "TEXT", "BOOL"}, nil),
		referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}),
		referenceSelect("SELECT 7 FROM t WHERE v=1", []string{"?column?"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}}),
		referenceSelect("SELECT v FROM t WHERE v=NULL", []string{"v"}, []string{"INT4"}, nil),
		referenceSelect("SELECT id FROM t WHERE v=$1", []string{"id"}, []string{"INT4"}, nil, nil),
		referenceSelect("SELECT id FROM t WHERE TRUE", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}}),
		referenceSelect("SELECT id FROM t WHERE FALSE", []string{"id"}, []string{"INT4"}, nil),
		referenceSelect("SELECT id FROM t WHERE NULL", []string{"id"}, []string{"INT4"}, nil),
		referenceSelect("SELECT id FROM t WHERE f", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}}),
		referenceSelect("SELECT $1 FROM t WHERE s=$1", []string{"?column?"}, []string{"TEXT"}, [][]any{{"x"}, {"x"}, {"x"}}, "x"),
	} {
		step.bag = true
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, backend := range p.backends() {
		rows, err := backend.db.QueryContext(p.ctx, "SELECT id,7,NULL,f FROM t")
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 4 {
			rows.Close()
			t.Fatal(types, err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for i, typ := range types {
			nullable, known := typ.Nullable()
			if backend.pg {
				if known {
					t.Fatal("pq nullable is unknown", i, nullable, known)
				}
			} else if i == 0 {
				if !known || nullable {
					t.Fatal("physical NOT NULL metadata", nullable, known)
				}
			} else if i == 3 {
				if !known || !nullable {
					t.Fatal("physical nullable metadata", nullable, known)
				}
			} else if known {
				t.Fatal("computed metadata claimed nullable", i)
			}
		}
	}
}

func queryCoreSeed(t *testing.T, p *referencePair) {
	t.Helper()
	p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL,v INT,s TEXT,f BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,1,'x',TRUE),(2,NULL,'x',NULL),(3,2,'y',FALSE),(4,1,'x',TRUE)", 4))
	p.step(t, referenceExec("CREATE TABLE empty(id INT NOT NULL,v INT,s TEXT,f BOOL)", 0))
}
