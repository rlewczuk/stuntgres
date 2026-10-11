package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterAddConstraintExistingData(t *testing.T) {
	for _, kind := range addConstraintCases() {
		for _, prepared := range []bool{false, true} {
			t.Run(kind.name+map[bool]string{false: "/direct", true: "/held"}[prepared], func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					run(fixture.Exec("INSERT INTO t VALUES(1,'first'),(2,'middle'),($1,'bad')", 3, kind.bad))
					catalog, relations := addConstraintCatalog(t, p, c), addConstraintRelations(t, p, c)
					var dump []byte
					if !b.PG {
						dump = addConstraintDump(t, p)
					}
					q := "ALTER TABLE t ADD " + kind.clause
					if prepared {
						held := addConstraintHeld(t, p, b, c, q)
						// Prepare has not published definitions or acquired execution holds.
						addConstraintUnchanged(t, p, b, c, catalog, relations, dump)
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, kind.build))
					} else {
						run(fixture.ErrorDiagnostic(q, kind.build))
					}
					addConstraintUnchanged(t, p, b, c, catalog, relations, dump)
					want := [][]any{{int64(1), "first"}, {int64(2), "middle"}, {kind.bad, "bad"}}
					// Explicit bag preserves duplicate/NULL multiplicities without invented order.
					step := fixture.Select("SELECT a,note FROM t", []string{"a", "note"}, []string{"INT4", "TEXT"}, want)
					step.Bag = true
					run(step)
					run(fixture.Exec("UPDATE t SET a=3 WHERE note='bad'", 1))
					run(fixture.Prepared(fixture.Exec(q, 0)))
					run(addConstraintRows([]any{int64(1), "first"}, []any{int64(2), "middle"}, []any{int64(3), "bad"}))
				})
			})
		}
	}
	t.Run("primary_null_and_not_distinct", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("INSERT INTO t VALUES(1,'one'),(NULL,'bad')", 2))
			cat, rels := addConstraintCatalog(t, p, c), addConstraintRelations(t, p, c)
			var dump []byte
			if !b.PG {
				dump = addConstraintDump(t, p)
			}
			d := fixture.ControlsReferenceDiagnostic("23502", `column "a" of relation "t" contains null values`)
			d.Schema, d.Table, d.Column = "public", "t", "a"
			run(fixture.ErrorDiagnostic("ALTER TABLE t ADD CONSTRAINT pk PRIMARY KEY(a)", d))
			addConstraintUnchanged(t, p, b, c, cat, rels, dump)
			run(fixture.Exec("INSERT INTO t VALUES(NULL,'second')", 1))
			d = fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "uq"`)
			d.Detail, d.Schema, d.Table, d.Constraint = "Key (a)=(null) is duplicated.", "public", "t", "uq"
			run(fixture.ErrorDiagnostic("ALTER TABLE t ADD CONSTRAINT uq UNIQUE NULLS NOT DISTINCT(a)", d))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
		})
	})
	t.Run("live_versions_private_image_sparse_attributes", func(t *testing.T) {
		for _, kind := range addConstraintCases() {
			t.Run(kind.name, func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := addConstraintSetup(t, p, b, c)
					run(fixture.Exec("INSERT INTO t VALUES($1,'ended')", 1, kind.bad))
					run(fixture.Exec("UPDATE t SET a=1", 1))
					run(fixture.Exec("ALTER TABLE t ADD discarded INT, ADD stable INT DEFAULT 7", 0))
					run(fixture.Exec("ALTER TABLE t DROP COLUMN discarded", 0))
					tx, err := c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					trun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, b.Recorder, s) }
					trun(fixture.Exec("INSERT INTO t(a,note) VALUES($1,'deleted')", 1, kind.bad))
					trun(fixture.Exec("DELETE FROM t WHERE note='deleted'", 1))
					trun(fixture.Exec("UPDATE t SET a=2 WHERE note='ended'", 1))
					trun(fixture.Exec("INSERT INTO t(a,note) VALUES(3,'private')", 1))
					trun(fixture.Exec("ALTER TABLE t ADD "+kind.clause, 0))
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					run(fixture.Select("SELECT a,note,stable FROM t ORDER BY a", []string{"a", "note", "stable"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(2), "ended", int64(7)}, {int64(3), "private", int64(7)}}))
					if kind.kind == "u" || kind.kind == "p" {
						run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT stable_pair UNIQUE(stable,a)", 0))
						run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='t'::regclass::oid AND conname='stable_pair'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"UNIQUE (stable, a)"}}))
						if b.PG {
							run(fixture.Select("SELECT conkey::text FROM pg_constraint WHERE conrelid='t'::regclass AND conname='stable_pair'", []string{"conkey"}, []string{"TEXT"}, [][]any{{"{4,1}"}}))
						}
						run(fixture.ErrorDiagnostic("INSERT INTO t(a,note,stable) VALUES(4,'pair',7),(4,'pair2',7)", fixture.UniqueReferenceDuplicate("t", map[bool]string{true: "pk", false: "uq"}[kind.kind == "p"], "Key (a)=(4) already exists.")))
					}
				})
			})
		}
	})
}
