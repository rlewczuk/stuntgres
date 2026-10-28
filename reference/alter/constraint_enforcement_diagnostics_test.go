package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceConstraintEnforcementDiagnostics(t *testing.T) {
	for _, attribute := range []string{"ENFORCED", "NOT ENFORCED"} {
		for _, kind := range []struct{ name, column, table string }{
			{"UNIQUE", "UNIQUE", "UNIQUE(a)"},
			{"PRIMARY KEY", "PRIMARY KEY", "PRIMARY KEY(a)"},
			{"NOT NULL", "NOT NULL", "NOT NULL a"},
			{"EXCLUDE", "", "EXCLUDE USING btree(a WITH =)"},
		} {
			for _, name := range []string{"t", "é"} {
				for _, form := range []string{"column", "table", "add"} {
					if kind.column == "" && form == "column" {
						continue
					}
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/%s/%s/%s/prepare_%v", attribute, kind.name, name, form, prepared), func(t *testing.T) {
							addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
								run := enforcementRun(t, p, b, c)
								q := `CREATE TABLE "` + name + `"(a INT,CONSTRAINT fact ` + kind.table + " " + attribute + ")"
								d := fixture.ControlsReferenceDiagnostic("0A000", kind.name+" constraints cannot be marked "+attribute)
								if form == "column" {
									q = `CREATE TABLE "` + name + `"(a INT ` + kind.column + " " + attribute + ")"
									d = fixture.ControlsReferenceDiagnostic("42601", "misplaced "+attribute+" clause")
								}
								if form == "add" {
									run(fixture.Exec(`CREATE TABLE "`+name+`"(a INT)`, 0))
									q = `ALTER TABLE "` + name + `" ADD CONSTRAINT fact ` + kind.table + " " + attribute
								}
								d.Position = enforcementPosition(q, attribute, true)
								phase := "execute"
								if prepared && form != "column" {
									phase = "prepare"
								}
								addConstraintError(t, p, c, q, d, prepared, phase)
								if form == "add" {
									run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid=$1::regclass", []string{"conname"}, []string{"NAME"}, nil, `"`+name+`"`))
								}
							})
						})
					}
				}
			}
		}
	}
	for _, tc := range []struct {
		name, query, code, message, token string
		last                              bool
	}{
		{"repeat_column", "CREATE TABLE t(a INT CHECK(a>0) ENFORCED ENFORCED)", "42601", "multiple ENFORCED/NOT ENFORCED clauses not allowed", "ENFORCED", true},
		{"repeat_column_negative", "CREATE TABLE t(a INT CHECK(a>0) NOT ENFORCED NOT ENFORCED)", "42601", "multiple ENFORCED/NOT ENFORCED clauses not allowed", "NOT ENFORCED", true},
		{"conflict_column", "CREATE TABLE t(a INT CHECK(a>0) ENFORCED NOT ENFORCED)", "42601", "multiple ENFORCED/NOT ENFORCED clauses not allowed", "NOT ENFORCED", true},
		{"conflict_table", "CREATE TABLE t(a INT,CONSTRAINT ck CHECK(a>0) ENFORCED NOT ENFORCED)", "42601", "conflicting constraint properties", "NOT ENFORCED", true},
		{"conflict_reverse", "CREATE TABLE t(a INT,CONSTRAINT ck CHECK(a>0) NOT ENFORCED ENFORCED)", "42601", "conflicting constraint properties", "ENFORCED", true},
		{"conflict_alter", "ALTER TABLE t ALTER CONSTRAINT fk ENFORCED NOT ENFORCED", "42601", "conflicting constraint properties", "NOT ENFORCED", true},
		{"not_valid_alter", "ALTER TABLE t ALTER CONSTRAINT fk NOT VALID", "0A000", "constraints cannot be altered to be NOT VALID", "NOT VALID", true},
		{"trailing", `ALTER TABLE "é" ADD CHECK(a>0) NOT ENFORCED garbage`, "42601", `syntax error at or near "garbage"`, "garbage", true},
		{"full_input", `ALTER TABLE "é" ADD CHECK(a>0) NOT ENFORCED; INSERT INTO keeper VALUES(92); nonsense`, "42601", `syntax error at or near "nonsense"`, "nonsense", true},
		{"domain", "CREATE DOMAIN positive AS INT CHECK(VALUE>0) NOT ENFORCED", "42P17", "specifying constraint enforceability not supported for domains", "NOT ENFORCED", true},
		{"domain_positive", "CREATE DOMAIN positive AS INT CHECK(VALUE>0) ENFORCED", "42P17", "specifying constraint enforceability not supported for domains", "ENFORCED", true},
		{"alter_domain", "ALTER DOMAIN positive ADD CHECK(VALUE>0) NOT ENFORCED", "0A000", "CHECK constraints cannot be marked NOT ENFORCED", "NOT ENFORCED", true},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepare_%v", tc.name, prepared), func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := enforcementRun(t, p, b, c)
					for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE t(a INT,CONSTRAINT fk FOREIGN KEY(a) REFERENCES p)", `CREATE TABLE "é"(a INT)`, "CREATE TABLE keeper(v INT)", "INSERT INTO keeper VALUES(91)"} {
						n := int64(0)
						if q == "INSERT INTO keeper VALUES(91)" {
							n = 1
						}
						run(fixture.Exec(q, n))
					}
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Position = enforcementPosition(tc.query, tc.token, tc.last)
					phase := "execute"
					if prepared && tc.name != "domain" && tc.name != "domain_positive" && tc.name != "repeat_column" && tc.name != "repeat_column_negative" && tc.name != "conflict_column" {
						phase = "prepare"
					}
					addConstraintError(t, p, c, tc.query, d, prepared, phase)
					run(fixture.Select("SELECT v FROM keeper", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
					run(fixture.Select(`SELECT conname FROM pg_constraint WHERE conrelid='"é"'::regclass`, []string{"conname"}, []string{"NAME"}, nil))
				})
			})
		}
	}
	for _, tc := range []struct{ name, clause string }{{"ck", "CHECK(a>0)"}, {"uq", "UNIQUE(a)"}, {"pk", "PRIMARY KEY(a)"}, {"nn", "NOT NULL a"}, {"ex", "EXCLUDE USING btree(a WITH =)"}, {"missing", ""}} {
		for _, attribute := range []string{"ENFORCED", "NOT ENFORCED"} {
			t.Run(tc.name+"/"+attribute, func(t *testing.T) {
				addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := enforcementRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE t(a INT)", 0))
					if tc.clause != "" {
						run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT "+tc.name+" "+tc.clause, 0))
					}
					d := fixture.ControlsReferenceDiagnostic("42809", `cannot alter enforceability of constraint "`+tc.name+`" of relation "t"`)
					if tc.name == "missing" {
						d = fixture.ControlsReferenceDiagnostic("42704", `constraint "missing" of relation "t" does not exist`)
					}
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("ALTER TABLE t ALTER CONSTRAINT "+tc.name+" "+attribute, d)
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
					}
				})
			})
		}
	}
	t.Run("held_authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"enforcement_guest"})
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			for _, q := range []string{"CREATE ROLE enforcement_guest", "GRANT USAGE ON SCHEMA public TO enforcement_guest", "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED"} {
				run(fixture.Exec(q, 0))
			}
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT fk ENFORCED")
			before := enforcementSnapshot(t, p, b, c)
			run(fixture.Exec("SET ROLE enforcement_guest", 0))
			validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, fixture.ControlsReferenceDiagnostic("42501", "must be owner of table t")))
			run(fixture.Exec("RESET ROLE", 0))
			enforcementUnchanged(t, p, b, c, before)
			validateConstraintHeldRun(t, p, b, held, fixture.Exec(held.SQL, 0))
		})
	})
	t.Run("read_only", func(t *testing.T) {
		addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := addConstraintSetup(t, p, b, c)
			run(fixture.Exec("ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT ENFORCED", 0))
			held := addConstraintHeld(t, p, b, c, "ALTER TABLE t ALTER CONSTRAINT fk ENFORCED")
			before := enforcementSnapshot(t, p, b, c)
			run(fixture.Exec("BEGIN READ ONLY", 0))
			validateConstraintHeldRun(t, p, b, held, fixture.ErrorDiagnostic(held.SQL, fixture.ControlsReferenceDiagnostic("25006", "cannot execute ALTER TABLE in a read-only transaction")))
			run(fixture.Exec("ROLLBACK", 0))
			enforcementUnchanged(t, p, b, c, before)
		})
	})
}

// REL_18_6 parse_utilcmd.c transforms unattached attributes at Execute.
// transformCreateStmt skips an existing IF NOT EXISTS target before that pass.
func TestReferenceConstraintEnforcementUnattachedDiagnostics(t *testing.T) {
	for _, tc := range []struct{ attribute, suffix string }{
		{"ENFORCED", ""}, {"NOT ENFORCED", ""},
		{"ENFORCED", " CHECK(a>0)"}, {"NOT ENFORCED", " CHECK(a>0)"},
	} {
		attribute := tc.attribute
		for _, name := range []string{"orphan", "é"} {
			for _, existing := range []bool{false, true} {
				for _, ifNotExists := range []bool{false, true} {
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/%s/later_%s/existing_%v/if_not_exists_%v/prepared_%v", attribute, name, tc.suffix, existing, ifNotExists, prepared), func(t *testing.T) {
							addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
								run := enforcementRun(t, p, b, c)
								run(fixture.Exec("CREATE TABLE t(a INT)", 0))
								run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
								if existing {
									run(fixture.Exec(`CREATE TABLE "`+name+`"(kept INT)`, 0))
									run(fixture.Exec(`INSERT INTO "`+name+`" VALUES(42)`, 1))
								}
								prefix := "CREATE TABLE "
								if ifNotExists {
									prefix += "IF NOT EXISTS "
								}
								q := prefix + `"` + name + `"(a INT ` + attribute + tc.suffix + ")"
								before := enforcementSnapshot(t, p, b, c)
								if existing && ifNotExists {
									step := fixture.Exec(q, 0)
									if prepared {
										step = fixture.Prepared(step)
									}
									run(step)
								} else {
									d := fixture.ControlsReferenceDiagnostic("42601", "misplaced "+attribute+" clause")
									d.Position = enforcementPosition(q, attribute, true)
									addConstraintError(t, p, c, q, d, prepared, "execute")
								}
								enforcementUnchanged(t, p, b, c, before)
								var relations [][]any
								if existing {
									relations = [][]any{{name}}
								}
								run(fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname=$1", []string{"relname"}, []string{"NAME"}, relations, name))
								if existing {
									run(fixture.Select(`SELECT kept FROM "`+name+`"`, []string{"kept"}, []string{"INT4"}, [][]any{{int64(42)}}))
								}
							})
						})
					}
				}
			}
		}
	}
}

// Released PG18.6 PRIMARY KEY/UNIQUE ExistingIndex productions reject
// enforceability in processCASbits, before any attachment or NN promotion.
func TestReferenceConstraintEnforcementUsingIndexDiagnostics(t *testing.T) {
	for _, kind := range []string{"PRIMARY KEY", "UNIQUE"} {
		for _, attribute := range []string{"ENFORCED", "NOT ENFORCED"} {
			for _, named := range []bool{false, true} {
				for _, index := range []string{"t_key", "index é"} {
					for _, prepared := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/%s/named_%v/%s/prepared_%v", kind, attribute, named, index, prepared), func(t *testing.T) {
							addConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
								run := enforcementRun(t, p, b, c)
								run(fixture.Exec("CREATE TABLE t(a INT)", 0))
								run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
								run(fixture.Exec(`CREATE UNIQUE INDEX "`+index+`" ON t(a)`, 0))
								var oid int64
								if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relnamespace='public'::regnamespace AND relname=$1", index).Scan(&oid); err != nil || oid <= 0 {
									t.Fatal("missing standalone index", oid, err)
								}
								indexQuery := "SELECT indexrelid,indrelid,indisunique,indisprimary,indisvalid,indisready,indislive,indkey,pg_get_indexdef(indexrelid) FROM pg_index WHERE indexrelid=$1::oid"
								dependencyQuery := "SELECT classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype FROM pg_depend WHERE (classid='pg_class'::regclass AND objid=$1::oid) OR (refclassid='pg_class'::regclass AND refobjid=$1::oid) ORDER BY classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype"
								before := enforcementSnapshot(t, p, b, c)
								indexBefore := enforcementQuery(t, p, c, indexQuery, oid)
								dependenciesBefore := enforcementQuery(t, p, c, dependencyQuery, oid)
								prefix := ""
								if named {
									name := "pk é"
									if kind == "UNIQUE" {
										name = "uq é"
									}
									prefix = `CONSTRAINT "` + name + `" `
								}
								q := "ALTER TABLE t ADD " + prefix + kind + ` USING INDEX "` + index + `" ` + attribute
								diagnostic := fixture.ControlsReferenceDiagnostic("0A000", kind+" constraints cannot be marked "+attribute)
								diagnostic.Position = enforcementPosition(q, attribute, true)
								phase := "execute"
								if prepared {
									phase = "prepare"
								}
								addConstraintError(t, p, c, q, diagnostic, prepared, phase)
								enforcementUnchanged(t, p, b, c, before)
								if !reflect.DeepEqual(enforcementQuery(t, p, c, indexQuery, oid), indexBefore) || !reflect.DeepEqual(enforcementQuery(t, p, c, dependencyQuery, oid), dependenciesBefore) {
									t.Fatal("rejected enforcement changed standalone index identity/definition/dependencies")
								}
								run(fixture.Select("SELECT indexrelid=$1::oid AS same,indrelid='t'::regclass AS parent,indisunique,indisprimary,indisvalid,indisready,indislive FROM pg_index WHERE indexrelid=$1::oid",
									[]string{"same", "parent", "indisunique", "indisprimary", "indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, false, true, true, true}}, oid))
								run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass OR conindid=$1::oid", []string{"conname"}, []string{"NAME"}, nil, oid))
								run(fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='t'::regclass AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{false}}))
								run(fixture.Select("SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
								fixture.TableDDLMetadata(t, p.Ctx, c, b.PG, prepared, "SELECT a FROM t ORDER BY a", []string{"a"}, []string{"INT4"}, []bool{true}, 2)
							})
						})
					}
				}
			}
		}
	}
}
