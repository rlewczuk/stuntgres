package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Released individual build contracts plus pinned PostgreSQL utility-pass
// precedence: index builds precede CHECK, preserving index declaration order.
func TestReferenceAlterAddConstraintOrdering(t *testing.T) {
	for _, keyKind := range []string{"unique", "primary_key"} {
		key := "CONSTRAINT key UNIQUE(a)"
		contype, definition := "u", "UNIQUE (a)"
		if keyKind == "primary_key" {
			key, contype, definition = "CONSTRAINT key PRIMARY KEY(a)", "p", "PRIMARY KEY (a)"
		}
		for _, target := range []string{"self", "external"} {
			parent, reference := "t", "t(a)"
			if target == "external" {
				parent, reference = "p", "p(id)"
			}
			fk := "CONSTRAINT fk FOREIGN KEY(b) REFERENCES " + reference
			for _, first := range []bool{false, true} {
				clauses := []string{key, fk}
				order := "key_before_fk"
				if first {
					clauses[0], clauses[1] = fk, key
					order = "fk_before_key"
				}
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("foreign/%s/%s/%s/%s", target, keyKind, order, orderingRoute(prepared)), func(t *testing.T) {
						addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
							for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "INSERT INTO p VALUES(1)", "CREATE TABLE t(a INT,b INT,note TEXT)", "INSERT INTO t VALUES(1,1,'keep')"} {
								count := int64(0)
								if strings.HasPrefix(q, "INSERT") {
									count = 1
								}
								run(fixture.Exec(q, count))
							}
							q := "ALTER TABLE t ADD " + strings.Join(clauses, ", ADD ")
							step := fixture.Exec(q, 0)
							step.Prepared = prepared
							run(step)
							run(fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid='t'::regclass::oid AND (conname='fk' OR conname='key') ORDER BY conname", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{"fk", "f"}, {"key", contype}}))
							run(fixture.Select("SELECT conname,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='t'::regclass::oid AND (conname='fk' OR conname='key') ORDER BY conname", []string{"conname", "pg_get_constraintdef"}, []string{"NAME", "TEXT"}, [][]any{{"fk", "FOREIGN KEY (b) REFERENCES " + reference}, {"key", definition}}))
							run(fixture.Select("SELECT relkind FROM pg_class WHERE relname='key'", []string{"relkind"}, []string{"CHAR"}, [][]any{{"i"}}))
							insertSQL := "INSERT INTO t VALUES($1,$2,$3)"
							insert := addConstraintHeld(t, p, b, c, insertSQL)
							fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 2, 1, "ok"))
							fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.ErrorDiagnostic(insertSQL, fixture.UniqueReferenceDuplicate("t", "key", "Key (a)=(1) already exists."), 1, 1, "duplicate"))
							fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.ErrorDiagnostic(insertSQL, addConstraintFK("t", "fk", "b", "9", parent), 3, 9, "orphan"))
							fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 3, 1, "again"))
							updateSQL := "UPDATE t SET b=$1,note=$2 WHERE a=$3"
							update := addConstraintHeld(t, p, b, c, updateSQL)
							fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.ErrorDiagnostic(updateSQL, addConstraintFK("t", "fk", "b", "9", parent), 9, "bad", 2))
							fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.Exec(updateSQL, 1, 1, "changed", 2))
							run(fixture.Select("SELECT a,b,note FROM t ORDER BY a", []string{"a", "b", "note"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(1), "keep"}, {int64(2), int64(1), "changed"}, {int64(3), int64(1), "again"}}))
						})
					})
				}
			}
		}
	}

	orders := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, keyKind := range []string{"unique", "primary_key"} {
		key := "CONSTRAINT key UNIQUE(a)"
		if keyKind == "primary_key" {
			key = "CONSTRAINT key PRIMARY KEY(a)"
		}
		for _, bad := range []string{"check_only", "key_only", "exclude_only", "all_three"} {
			check := "CONSTRAINT ck CHECK(a>0)"
			rows := [][]any{{int64(1), int64(1), "one"}, {int64(2), int64(2), "two"}}
			want := fixture.ControlsReferenceDiagnostic("23514", `check constraint "ck" of relation "t" is violated by some row`)
			want.Schema, want.Table, want.Constraint = "public", "t", "ck"
			switch bad {
			case "check_only":
				check = "CONSTRAINT ck CHECK(a<0)"
			case "key_only":
				rows[1][0] = int64(1)
				want = fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "key"`)
				want.Detail, want.Schema, want.Table, want.Constraint = "Key (a)=(1) is duplicated.", "public", "t", "key"
			case "exclude_only":
				rows[1][1] = int64(1)
				want = fixture.ControlsReferenceDiagnostic("23P01", `could not create exclusion constraint "ex"`)
				want.Detail, want.Schema, want.Table, want.Constraint = "Key (b)=(1) conflicts with key (b)=(1).", "public", "t", "ex"
			case "all_three":
				check = "CONSTRAINT ck CHECK(a<0)"
				rows[1][0], rows[1][1] = int64(1), int64(1)
			}
			clauses := []string{check, key, "CONSTRAINT ex EXCLUDE USING btree(b WITH =)"}
			for _, order := range orders {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("mixed/%s/%s/%d%d%d/%s", keyKind, bad, order[0], order[1], order[2], orderingRoute(prepared)), func(t *testing.T) {
						addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							run := orderingSetup(t, p, b, c, rows)
							q := "ALTER TABLE t ADD " + clauses[order[0]] + ", ADD " + clauses[order[1]] + ", ADD " + clauses[order[2]]
							expected := want
							if bad == "all_three" {
								firstIndex := 1
								for _, item := range order {
									if item != 0 {
										firstIndex = item
										break
									}
								}
								if firstIndex == 1 {
									expected = fixture.ControlsReferenceDiagnostic("23505", `could not create unique index "key"`)
									expected.Detail, expected.Schema, expected.Table, expected.Constraint = "Key (a)=(1) is duplicated.", "public", "t", "key"
								} else {
									expected = fixture.ControlsReferenceDiagnostic("23P01", `could not create exclusion constraint "ex"`)
									expected.Detail, expected.Schema, expected.Table, expected.Constraint = "Key (b)=(1) conflicts with key (b)=(1).", "public", "t", "ex"
								}
							}
							addConstraintError(t, p, c, q, expected, prepared, "execute")
							orderingUnchanged(t, p, b, c, run, rows)
						})
					})
				}
			}
		}
	}

	for _, keyKind := range []string{"unique", "primary_key"} {
		key := "CONSTRAINT occupied UNIQUE(a)"
		if keyKind == "primary_key" {
			key = "CONSTRAINT occupied PRIMARY KEY(a)"
		}
		clauses := []string{"CONSTRAINT occupied CHECK(a>0)", key, "CONSTRAINT occupied EXCLUDE USING btree(b WITH =)"}
		for _, pair := range [][2]int{{0, 1}, {0, 2}, {1, 2}} {
			for _, reverse := range []bool{false, true} {
				first, last := pair[0], pair[1]
				if reverse {
					first, last = last, first
				}
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("names/%s/%d%d/%s", keyKind, first, last, orderingRoute(prepared)), func(t *testing.T) {
						addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							rows := [][]any{{int64(1), int64(1), "keep"}}
							run := orderingSetup(t, p, b, c, rows)
							q := "ALTER TABLE t ADD " + clauses[first] + ", ADD " + clauses[last]
							expected := fixture.ControlsReferenceDiagnostic("42710", `constraint "occupied" for relation "t" already exists`)
							if pair[0] != 0 {
								expected = fixture.ControlsReferenceDiagnostic("42P07", `relation "occupied" already exists`)
							}
							addConstraintError(t, p, c, q, expected, prepared, "execute")
							orderingUnchanged(t, p, b, c, run, rows)
						})
					})
				}
			}
		}
	}
}

func orderingRoute(prepared bool) string {
	if prepared {
		return "prepared"
	}
	return "direct"
}

func orderingSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, rows [][]any) func(fixture.Step) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE t(a INT,b INT,note TEXT)", 0))
	for _, row := range rows {
		run(fixture.Exec("INSERT INTO t VALUES($1,$2,$3)", 1, row...))
	}
	return run
}

func orderingUnchanged(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), rows [][]any) {
	t.Helper()
	run(fixture.Select("SELECT a,b,note FROM t ORDER BY a,b,note", []string{"a", "b", "note"}, []string{"INT4", "INT4", "TEXT"}, rows))
	run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname"}, []string{"NAME"}, nil))
	run(fixture.Select("SELECT relname FROM pg_class WHERE relname='key' OR relname='ex' OR relname='occupied'", []string{"relname"}, []string{"NAME"}, nil))
	// The failed declaration must leave all rows writable and every proposed name reusable.
	run(fixture.Exec("UPDATE t SET note=note", int64(len(rows))))
	run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT occupied CHECK(a>0)", 0))
	run(fixture.Select("SELECT conname,contype FROM pg_constraint WHERE conrelid='t'::regclass::oid", []string{"conname", "contype"}, []string{"NAME", "CHAR"}, [][]any{{"occupied", "c"}}))
}
