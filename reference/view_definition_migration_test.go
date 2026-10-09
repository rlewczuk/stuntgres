package reference_test

import "testing"

func TestReferenceViewCanonicalLabels(t *testing.T) {
	for _, aliased := range []bool{false, true} {
		name := "unlabeled"
		if aliased {
			name = "inner_aliases"
		}
		t.Run(name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(id INT,txt TEXT)", 0))
					run(referenceExec("INSERT INTO t VALUES(7,'seven')", 1))
					run(referenceSelect("SELECT id AS k,txt AS label FROM t", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seven"}}))
					run(referencePrepared(referenceSelect("SELECT id AS k FROM t WHERE id=$1", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}}, 7)))
					body := "SELECT id,COALESCE(txt,'x') FROM t"
					if aliased {
						body = "SELECT id AS discarded,COALESCE(txt,'x') AS ignored FROM t"
					}
					run(referenceExec("CREATE VIEW v(k,label) AS "+body, 0))
					run(referenceExec("ALTER TABLE t RENAME COLUMN id TO renamed", 0))
					referenceViewCopy(t, p, b, "v", "reconstructed")
					s := referenceSelect("SELECT k,label FROM reconstructed", []string{"k", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seven"}})
					run(s)
					run(referencePrepared(s))
				})
			}
		})
	}
	t.Run("projection_labels", func(t *testing.T) {
		p := newReferenceScenario(t)
		for _, b := range p.backends() {
			t.Run(b.name, func(t *testing.T) {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
				run(referenceExec("CREATE TABLE t(id INT)", 0))
				run(referenceExec("INSERT INTO t VALUES(7)", 1))
				run(referenceSelect("SELECT id AS named FROM t", []string{"named"}, []string{"INT4"}, [][]any{{int64(7)}}))
				run(referencePrepared(referenceSelect("SELECT id AS named FROM t WHERE id=$1", []string{"named"}, []string{"INT4"}, [][]any{{int64(7)}}, 7)))
				run(referenceExec("CREATE VIEW v(k,label) AS SELECT id AS repeated,id AS repeated FROM t", 0))
				run(referenceSelect("SELECT k,label FROM v", []string{"k", "label"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}))
				s := referenceImplicitError("CREATE VIEW duplicate AS SELECT id AS repeated,id AS repeated FROM t", referenceDiagnostic{Code: "42701", Message: `column "repeated" specified more than once`})
				s.errorStage = "execute"
				run(s)
			})
		}
	})
}

func TestReferenceViewIntegratedOIDNativeRoundTrip(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, s := range []referenceStep{
				referenceExec("CREATE TABLE t(id OID)", 0), referenceExec("INSERT INTO t VALUES(1),('4294967295')", 2),
				referenceExec("CREATE VIEW v(id,high) AS SELECT id,CAST('-1' AS oid) FROM t WHERE id BETWEEN 1 AND CAST('-1' AS oid)", 0), referenceExec("CREATE VIEW w AS SELECT id,high FROM v", 0),
			} {
				run(s)
			}
			want := [][]any{{int64(1), int64(4294967295)}, {int64(4294967295), int64(4294967295)}}
			parameter := [][]any{{int64(4294967295)}}
			if b.pg {
				want = [][]any{{"1", "4294967295"}, {"4294967295", "4294967295"}}
				parameter = [][]any{{"4294967295"}}
			}
			for phase := range 2 {
				if phase == 1 && !b.pg {
					run(referenceExec("CREATE TEMPORARY SEQUENCE retained", 0))
					referenceViewRestore(t, p, referenceViewDump(t, p))
				}
				run(referenceSelect("SELECT id,high FROM w ORDER BY id", []string{"id", "high"}, []string{"OID", "OID"}, want))
				run(referenceSelect("SELECT id FROM w WHERE id=$1", []string{"id"}, []string{"OID"}, parameter, int64(-1)))
			}
		})
	}
}

func TestReferenceViewIntegratedBetweenDefinition(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "true_left"
		if legacy {
			name = "symmetric_null"
		}
		t.Run(name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(id INT)", 0))
					body := "CREATE VIEW v(matches) AS SELECT TRUE BETWEEN (id BETWEEN 1 AND 3) AND TRUE FROM t"
					columns, types, want := []string{"matches"}, []string{"BOOL"}, [][]any{{true}}
					insert, count, projection := "INSERT INTO t VALUES(2)", int64(1), "matches"
					if legacy {
						insert, count, projection = "INSERT INTO t VALUES(2),(4)", 2, "id,inclusive,nested,unknown_value"
						body = "CREATE VIEW v(id,inclusive,nested,unknown_value) AS SELECT id,id BETWEEN SYMMETRIC 3 AND 1,(id BETWEEN 1 AND 3) BETWEEN TRUE AND TRUE,NULL BETWEEN (TRUE AND TRUE) AND TRUE FROM t"
						columns = []string{"id", "inclusive", "nested", "unknown_value"}
						types = []string{"INT4", "BOOL", "BOOL", "BOOL"}
						want = [][]any{{int64(2), true, true, nil}, {int64(4), false, false, nil}}
					}
					run(referenceExec(insert, count))
					run(referenceExec(body, 0))
					for phase := range 3 {
						if phase == 1 {
							referenceViewCopy(t, p, b, "v", "reconstructed")
						}
						if phase == 2 && !b.pg {
							referenceViewRestore(t, p, referenceViewDump(t, p))
						}
						names := []string{"v"}
						if phase > 0 {
							names = append(names, "reconstructed")
						}
						for _, view := range names {
							query := "SELECT " + projection + " FROM " + view
							if legacy {
								query += " ORDER BY id"
							}
							run(referenceSelect(query, columns, types, want))
						}
					}
				})
			}
		})
	}
}

func TestReferenceViewCatalogCHARLiteralReconstruction(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			run(referenceExec("CREATE TABLE char_source(id INT)", 0))
			run(referenceSelect("SELECT relkind < 'é' AS below FROM pg_catalog.pg_class WHERE relname='char_source'", []string{"below"}, []string{"BOOL"}, [][]any{{true}}))
			run(referenceExec("CREATE VIEW char_literal AS SELECT relkind < 'é' AS below FROM pg_catalog.pg_class WHERE relname='char_source'", 0))
			original := referenceViewHold(t, p, b, "SELECT below FROM char_literal")
			s := referenceSelect(original.query, []string{"below"}, []string{"BOOL"}, [][]any{{true}})
			run(s)
			referenceRunStep(t, p.ctx, original, b.recorder, s)
			referenceViewCopy(t, p, b, "char_literal", "reconstructed")
			copyStmt := referenceViewHold(t, p, b, "SELECT below FROM reconstructed")
			for phase := range 2 {
				if phase == 1 && !b.pg {
					referenceViewRestore(t, p, referenceViewDump(t, p))
				}
				for _, held := range []referenceHeldStatement{original, copyStmt} {
					s := referenceSelect(held.query, []string{"below"}, []string{"BOOL"}, [][]any{{true}})
					run(s)
					referenceRunStep(t, p.ctx, held, b.recorder, s)
				}
			}
		})
	}
}
