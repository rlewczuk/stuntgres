package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterConstraintInheritPropagation(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := enforcementRun(t, p, b, c)
				constraintModeGraph(run)
				root := constraintModeOID(t, p, c, "t", "nn")
				child := constraintModeOID(t, p, c, "child", "nn")
				grand := constraintModeOID(t, p, c, "grandchild", "nn")
				change := func(q string) {
					if held {
						h := addConstraintHeld(t, p, b, c, q)
						validateConstraintHeldRun(t, p, b, h, fixture.Exec(q, 0))
					} else {
						run(fixture.Exec(q, 0))
					}
				}
				run(constraintModeNN("t", "nn", false, true, 0, true))
				run(constraintModeNN("child", "nn", false, false, 1, true))
				run(constraintModeNN("grandchild", "nn", false, false, 1, true))
				// ONLY on ordinary inheritance still edits the direct child's origin.
				change("ALTER TABLE ONLY t ALTER CONSTRAINT nn NO INHERIT")
				run(constraintModeNN("t", "nn", true, true, 0, true))
				run(constraintModeNN("child", "nn", false, true, 0, true))
				run(constraintModeNN("grandchild", "nn", false, false, 1, true))
				change("ALTER TABLE ONLY t ALTER CONSTRAINT nn NO INHERIT")
				run(fixture.ErrorDiagnostic("INSERT INTO child VALUES(NULL)", fixture.PrimaryKeyReferenceNull("child", "a", "null")))
				run(fixture.Exec("CREATE TABLE fresh() INHERITS(t)", 0))
				run(fixture.Exec("CREATE TABLE fresh_leaf() INHERITS(fresh)", 0))
				run(fixture.Exec("INSERT INTO fresh VALUES(4)", 1))
				run(fixture.Exec("INSERT INTO fresh_leaf VALUES(5)", 1))
				run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='fresh'::regclass", []string{"conname"}, []string{"NAME"}, nil))
				change("ALTER TABLE ONLY t ALTER CONSTRAINT nn INHERIT")
				for _, table := range []string{"t", "child", "grandchild", "fresh", "fresh_leaf"} {
					local, count := false, int64(1)
					if table == "t" {
						local, count = true, 0
					}
					if table == "child" {
						local = true
					}
					run(constraintModeNN(table, "nn", false, local, count, true))
				}
				constraintModeSameOID(t, p, c, "t", "nn", root)
				constraintModeSameOID(t, p, c, "child", "nn", child)
				constraintModeSameOID(t, p, c, "grandchild", "nn", grand)
				run(fixture.ErrorDiagnostic("INSERT INTO fresh_leaf VALUES(NULL)", fixture.PrimaryKeyReferenceNull("fresh_leaf", "a", "null")))
				run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}))
			})
		})
	}
	t.Run("existing_invalid_different_name_and_invalid_root", func(t *testing.T) {
		constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(a INT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(NULL)", 1))
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT parent_nn NOT NULL a NOT VALID", 0))
			id := constraintModeOID(t, p, c, "t", "parent_nn")
			run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT parent_nn NO INHERIT", 0))
			run(fixture.Exec("CREATE TABLE child() INHERITS(t)", 0))
			run(fixture.Exec("INSERT INTO child VALUES(NULL)", 1))
			run(fixture.Exec("ALTER TABLE child ADD CONSTRAINT child_nn NOT NULL a NOT VALID", 0))
			// This existing invalid fact has historical NULL; INHERIT must
			// merge it without validation, independently of new-table validity.
			run(constraintModeNN("child", "child_nn", false, true, 0, false))
			run(fixture.Exec("CREATE TABLE grandchild() INHERITS(child)", 0))
			child := constraintModeOID(t, p, c, "child", "child_nn")
			grand := constraintModeOID(t, p, c, "grandchild", "child_nn")
			// CREATE TABLE has no old rows to validate: its inherited NN is
			// valid even though the source parent's fact is NOT VALID.
			run(constraintModeNN("grandchild", "child_nn", false, false, 1, true))
			run(fixture.Prepared(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT parent_nn INHERIT", 0)))
			run(constraintModeNN("t", "parent_nn", false, true, 0, false))
			run(constraintModeNN("child", "child_nn", false, true, 1, false))
			run(constraintModeNN("grandchild", "child_nn", false, false, 1, true))
			constraintModeSameOID(t, p, c, "child", "child_nn", child)
			constraintModeSameOID(t, p, c, "grandchild", "child_nn", grand)
			run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT parent_nn NO INHERIT", 0))
			run(constraintModeNN("t", "parent_nn", true, true, 0, false))
			run(constraintModeNN("child", "child_nn", false, true, 0, false))
			run(constraintModeNN("grandchild", "child_nn", false, false, 1, true))
			constraintModeSameOID(t, p, c, "t", "parent_nn", id)
			run(fixture.Select("SELECT a FROM ONLY t", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
			run(fixture.Select("SELECT a FROM ONLY child", []string{"a"}, []string{"INT4"}, [][]any{{nil}}))
		})
	})
	t.Run("multiple_parent_contributions", func(t *testing.T) {
		constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			for _, q := range []string{"CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)", "CREATE TABLE other(a INT CONSTRAINT other_nn NOT NULL)", "CREATE TABLE child() INHERITS(t,other)", "CREATE TABLE grandchild() INHERITS(child)"} {
				run(fixture.Exec(q, 0))
			}
			child := constraintModeOID(t, p, c, "child", "nn")
			grand := constraintModeOID(t, p, c, "grandchild", "nn")
			run(constraintModeNN("child", "nn", false, false, 2, true))
			run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT", 0))
			run(constraintModeNN("child", "nn", false, true, 1, true))
			run(constraintModeNN("grandchild", "nn", false, false, 1, true))
			run(fixture.ErrorDiagnostic("ALTER TABLE child ALTER CONSTRAINT nn NO INHERIT", fixture.ControlsReferenceDiagnostic("55000", `cannot alter inherited constraint "nn" on relation "child"`)))
			run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT nn INHERIT", 0))
			run(constraintModeNN("child", "nn", false, true, 2, true))
			constraintModeSameOID(t, p, c, "child", "nn", child)
			constraintModeSameOID(t, p, c, "grandchild", "nn", grand)
		})
	})
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "diamond/direct", true: "diamond/held"}[held], func(t *testing.T) {
			constraintModeDiamond(t, held)
		})
	}
	t.Run("visited_child_noinherit", func(t *testing.T) {
		constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := enforcementRun(t, p, b, c)
			run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)", 0))
			run(fixture.Exec("ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT", 0))
			run(fixture.Exec("CREATE TABLE child(a INT CONSTRAINT child_nn NOT NULL) INHERITS(t)", 0))
			run(fixture.Exec("ALTER TABLE child ALTER CONSTRAINT child_nn NO INHERIT", 0))
			before := constraintModeSnapshot(t, p, b, c)
			run(fixture.Prepared(fixture.ErrorDiagnostic("ALTER TABLE t ALTER CONSTRAINT nn INHERIT", fixture.ControlsReferenceDiagnostic("0A000", `cannot change NO INHERIT status of NOT NULL constraint "child_nn" on relation "child"`))))
			constraintModeUnchanged(t, p, b, c, before)
		})
	})
}

// One path stops at an existing compatible fact while another constructs a
// missing fact and must still reach their shared descendant. The shared
// descendant's independent owner first proves actual reach and atomic failure;
// its independently owned leaf then proves that the successful merge stops.
func constraintModeDiamond(t *testing.T, held bool) {
	fixture.RoleReferenceCleanup(t, []string{"mode_diamond_owner", "mode_diamond_leaf_owner"})
	constraintModeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := enforcementRun(t, p, b, c)
		for _, q := range []string{
			"CREATE ROLE mode_diamond_owner",
			"CREATE ROLE mode_diamond_leaf_owner",
			"GRANT USAGE ON SCHEMA public TO mode_diamond_owner",
			"CREATE TABLE t(a INT CONSTRAINT nn NOT NULL)",
			"ALTER TABLE t ALTER CONSTRAINT nn NO INHERIT",
			"CREATE TABLE left_child(a INT CONSTRAINT left_nn NOT NULL) INHERITS(t)",
			"CREATE TABLE right_child() INHERITS(t)",
			"CREATE TABLE shared_child() INHERITS(left_child,right_child)",
			"CREATE TABLE leaf() INHERITS(shared_child)",
			"ALTER TABLE t OWNER TO mode_diamond_owner",
			"ALTER TABLE left_child OWNER TO mode_diamond_owner",
			"ALTER TABLE right_child OWNER TO mode_diamond_owner",
			"ALTER TABLE leaf OWNER TO mode_diamond_leaf_owner",
		} {
			run(fixture.Exec(q, 0))
		}
		for i, name := range []string{"t", "left_child", "right_child", "shared_child", "leaf"} {
			run(fixture.Exec("INSERT INTO "+name+" VALUES($1)", 1, i+1))
		}
		root := constraintModeOID(t, p, c, "t", "nn")
		left := constraintModeOID(t, p, c, "left_child", "left_nn")
		shared := constraintModeOID(t, p, c, "shared_child", "left_nn")
		leaf := constraintModeOID(t, p, c, "leaf", "left_nn")
		run(constraintModeNN("t", "nn", true, true, 0, true))
		run(constraintModeNN("left_child", "left_nn", false, true, 0, true))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='right_child'::regclass", []string{"conname"}, []string{"NAME"}, nil))
		run(constraintModeNN("shared_child", "left_nn", false, false, 1, true))
		run(constraintModeNN("leaf", "left_nn", false, false, 1, true))
		const q = "ALTER TABLE t ALTER CONSTRAINT nn INHERIT"
		change := func(s fixture.Step) { run(s) }
		if held {
			h := addConstraintHeld(t, p, b, c, q)
			change = func(s fixture.Step) { validateConstraintHeldRun(t, p, b, h, s) }
		}
		before := constraintModeSnapshot(t, p, b, c)
		run(fixture.Exec("SET ROLE mode_diamond_owner", 0))
		change(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "must be owner of table shared_child")))
		run(fixture.Exec("RESET ROLE", 0))
		constraintModeUnchanged(t, p, b, c, before)
		run(fixture.Exec("ALTER TABLE shared_child OWNER TO mode_diamond_owner", 0))
		run(fixture.Exec("SET ROLE mode_diamond_owner", 0))
		change(fixture.Exec(q, 0))
		run(fixture.Exec("RESET ROLE", 0))
		run(constraintModeNN("t", "nn", false, true, 0, true))
		run(constraintModeNN("left_child", "left_nn", false, true, 1, true))
		run(constraintModeNN("right_child", "nn", false, false, 1, true))
		run(constraintModeNN("shared_child", "left_nn", false, false, 2, true))
		run(constraintModeNN("leaf", "left_nn", false, false, 1, true))
		constraintModeSameOID(t, p, c, "t", "nn", root)
		constraintModeSameOID(t, p, c, "left_child", "left_nn", left)
		constraintModeSameOID(t, p, c, "shared_child", "left_nn", shared)
		constraintModeSameOID(t, p, c, "leaf", "left_nn", leaf)
		right := constraintModeOID(t, p, c, "right_child", "nn")
		for _, id := range []int64{root, left, shared, leaf} {
			if right == id {
				t.Fatal("constructed branch reused another NN identity", right)
			}
		}
		run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}))
		run(fixture.ErrorDiagnostic("INSERT INTO right_child VALUES(NULL)", fixture.PrimaryKeyReferenceNull("right_child", "a", "null")))
		run(fixture.ErrorDiagnostic("INSERT INTO shared_child VALUES(NULL)", fixture.PrimaryKeyReferenceNull("shared_child", "a", "null")))
	})
}
