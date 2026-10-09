package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceUniqueBtreeDiagnosticsAndNameBinding(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, q := range []string{"CREATE TABLE t(n INT)", "CREATE SEQUENCE seq", "CREATE VIEW v AS SELECT n FROM t", "CREATE INDEX idx ON t(n)"} {
				run(referenceExec(q, 0))
			}
			// Complete records are retained from the existing pinned B-tree reference
			// witness; the root-only duplicate additionally supplied the held-name route.
			for _, c := range []struct{ query, code, message, detail string }{
				{"CREATE INDEX retry ON public.missing(n)", "42P01", `relation "public.missing" does not exist`, ""},
				{"CREATE INDEX retry ON absent.t(n)", "3F000", `schema "absent" does not exist`, ""},
				{"CREATE INDEX idx ON t(missing)", "42703", `column "missing" does not exist`, ""},
				{"CREATE INDEX IF NOT EXISTS idx ON t(missing)", "42703", `column "missing" does not exist`, ""},
				{"CREATE INDEX retry ON seq(n)", "42809", `cannot create index on relation "seq"`, "This operation is not supported for sequences."},
				{"CREATE INDEX retry ON v(n)", "42809", `cannot create index on relation "v"`, "This operation is not supported for views."},
				{"CREATE INDEX retry ON idx(n)", "42809", `cannot open relation "idx"`, "This operation is not supported for indexes."},
				{"CREATE INDEX retry ON t USING absent(n)", "42704", `access method "absent" does not exist`, ""},
				{"CREATE INDEX retry ON t(ctid)", "0A000", "index creation on system columns is not supported", ""},
				{"CREATE INDEX retry ON t(" + strings.TrimSuffix(strings.Repeat("n,", 33), ",") + ")", "54011", "cannot use more than 32 columns in an index", ""},
			} {
				s := referenceImplicitError(c.query, referenceDiagnostic{Code: c.code, Message: c.message, Detail: c.detail})
				s.errorStage = "execute"
				run(s)
				run(referencePrepared(s))
				run(referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
			}
			run(referenceExec("CREATE INDEX retry ON t(n)", 0))
			run(referenceExec("CREATE INDEX ON t(n)", 0))
			run(referenceExec("CREATE INDEX ON t(n)", 0))
			run(referenceSelect("SELECT relname,relkind,relnatts FROM pg_catalog.pg_class WHERE relname='t_n_idx' OR relname='t_n_idx1' ORDER BY relname", []string{"relname", "relkind", "relnatts"}, []string{"NAME", "CHAR", "INT2"}, [][]any{{"t_n_idx", "i", int64(1)}, {"t_n_idx1", "i", int64(1)}}))
			held, err := b.db.PrepareContext(p.ctx, "CREATE INDEX held ON later(n)")
			if err != nil {
				t.Fatal("held utility must prepare before target exists", err)
			}
			defer held.Close()
			_, err = held.ExecContext(p.ctx)
			want := controlsReferenceDiagnostic("42P01", `relation "later" does not exist`)
			referenceAssertDiagnostic(t, err, &want)
			run(referenceExec("CREATE TABLE later(n INT)", 0))
			result, err := held.ExecContext(p.ctx)
			if err != nil {
				t.Fatal("prepared utility did not rebind", err)
			}
			referenceLockAffected(t, result, 0)
			run(referenceSelect("SELECT relname,relnatts FROM pg_class WHERE relname='held'", []string{"relname", "relnatts"}, []string{"NAME", "INT2"}, [][]any{{"held", int64(1)}}))
		})
	}
}

func TestReferenceUniqueBtreeUtilityBoundary(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			name := map[bool]string{false: "writable", true: "readonly"}[readOnly] + map[bool]string{false: "/direct", true: "/prepared"}[prepared]
			t.Run(name, func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("CREATE TABLE t(n INT)", 0))
						tx, err := b.db.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, b.recorder, s) }
						if readOnly {
							run(referenceExec("SET TRANSACTION READ ONLY", 0))
						}
						query := "CREATE INDEX idx ON t(n)"
						s := referenceExec(query, 0)
						if readOnly {
							query = "CREATE INDEX idx ON missing(n)"
							s = referenceImplicitError(query, referenceDiagnostic{Code: "25006", Message: "cannot execute CREATE INDEX in a read-only transaction"})
							s.errorStage = "execute"
						} else if !b.pg {
							s = referenceImplicitError(query, referenceDiagnostic{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
							s.errorStage = "execute"
						}
						if prepared {
							s = referencePrepared(s)
						}
						run(s)
						if b.pg && !readOnly {
							run(referenceSelect("SELECT relname FROM pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, [][]any{{"idx"}}))
						}
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT relname FROM pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil))
						referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT n FROM t", []string{"n"}, []string{"INT4"}, nil))
					})
				}
			})
		}
	}
}
