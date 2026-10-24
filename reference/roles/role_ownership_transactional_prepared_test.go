package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipTransactionalPrepared(t *testing.T) {
	for _, family := range ownershipTransactionalFamilies {
		t.Run(family.name, func(t *testing.T) {
			ownershipCases(t, func(s *membershipScenario) {
				q, tag := family.prefix+" OWNER TO own_absent", "ALTER TABLE"
				switch family.name {
				case "database":
					q = "ALTER DATABASE own_missing_database OWNER TO own_absent"
					tag = "ALTER DATABASE"
				case "enum", "composite":
					tag = "ALTER TYPE"
				case "domain":
					tag = "ALTER DOMAIN"
				case "view":
					tag = "ALTER VIEW"
				case "materialized":
					tag = "ALTER MATERIALIZED VIEW"
				case "schema":
					tag = "ALTER SCHEMA"
				case "sequence":
					tag = "ALTER SEQUENCE"
				case "reassign":
					q = "REASSIGN OWNED BY own_absent TO own_target"
					tag = "REASSIGN OWNED"
				case "restrict", "cascade":
					q = "DROP OWNED BY own_absent CASCADE"
					tag = "DROP OWNED"
				}
				for _, goTx := range []bool{false, true} {
					for _, outside := range []bool{false, true} {
						var stmt *sql.Stmt
						var err error
						if outside {
							stmt, err = s.c.PrepareContext(s.p.Ctx, q)
							if err != nil {
								s.t.Fatal("quiet Prepare before object/role exists", err)
							}
						}
						var e fixture.Executor = s.c
						var tx *sql.Tx
						if goTx {
							tx, err = s.c.BeginTx(s.p.Ctx, &sql.TxOptions{ReadOnly: true})
							if err != nil {
								s.t.Fatal(err)
							}
							defer tx.Rollback()
							e = tx
						} else {
							s.exec("BEGIN READ ONLY")
						}
						if !outside {
							if tx != nil {
								stmt, err = tx.PrepareContext(s.p.Ctx, q)
							} else {
								stmt, err = s.c.PrepareContext(s.p.Ctx, q)
							}
							if err != nil {
								s.t.Fatal("quiet read-only Prepare", err)
							}
						} else if tx != nil {
							old := stmt
							stmt = tx.StmtContext(s.p.Ctx, old)
							defer old.Close()
						}
						if result, err := stmt.ExecContext(s.p.Ctx, 1); result != nil || err == nil || err.Error() != "sql: expected 0 arguments, got 1" {
							s.t.Fatal("zero utility inputs", result, err)
						}
						var statement fixture.Executor = e
						if s.prepared {
							statement = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
						}
						step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute " + tag + " in a read-only transaction"})
						step.ErrorStage = "execute"
						fixture.RunStep(s.t, s.p.Ctx, statement, s.record, step)
						fixture.RunStep(s.t, s.p.Ctx, e, s.record, fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
						stmt.Close()
						if tx != nil {
							if err := tx.Rollback(); err != nil {
								s.t.Fatal(err)
							}
						} else {
							s.exec("ROLLBACK")
						}
						ownershipBool(s, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname='own_absent')", false)
					}
				}
			})
		})
	}
}

func TestReferenceRoleOwnershipTransactionalPreparedFreshness(t *testing.T) {
	for _, family := range ownershipTransactionalFamilies[:6] {
		t.Run(family.name, func(t *testing.T) {
			ownershipTransactionCases(t, func(s *membershipScenario, route string) {
				q := family.prefix + ` OWNER TO "own Quoted"`
				s.exec(`DROP ROLE "own Quoted"`)
				stmt, err := s.c.PrepareContext(s.p.Ctx, q)
				if err != nil {
					s.t.Fatal("Prepare before either name exists", err)
				}
				defer stmt.Close()
				ownershipDefinition(s, family.create)
				s.exec(family.prefix + " OWNER TO own_source")
				if family.name == "table" {
					s.run(fixture.Exec("INSERT INTO tx_own_table VALUES(7)", 1))
				}
				for range 2 {
					x := ownershipBegin(s, route)
					defer x.abort()
					use := stmt
					if x.tx != nil {
						use = x.tx.StmtContext(s.p.Ctx, stmt)
					}
					x.held[q] = use
					// A healthy held definition resolves the now-existing object and
					// still-missing successor only when executed.
					if route != "auto" {
						x.exec("SAVEPOINT missing_successor")
					}
					failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42704", Message: `role "own Quoted" does not exist`})
					failure.ErrorStage = "execute"
					x.run(failure)
					if route != "auto" {
						x.exec("ROLLBACK TO missing_successor")
					}
					// The retained Conn statement must survive transaction recovery.
					delete(x.held, q)
					if use != stmt {
						use.Close()
					}
					x.end(false)
					s.exec(`CREATE ROLE "own Quoted"`)
					y := ownershipBegin(s, route)
					defer y.abort()
					use = stmt
					if y.tx != nil {
						use = y.tx.StmtContext(s.p.Ctx, stmt)
					}
					y.held[q] = use
					y.exec(q)
					delete(y.held, q)
					if use != stmt {
						use.Close()
					}
					y.end(true)
					ownershipSelect(s, "SELECT r.rolname FROM "+family.catalog+" c JOIN pg_roles r ON r.oid=c."+family.ownerColumn+" WHERE c."+family.key, []string{"rolname"}, []string{"NAME"}, [][]any{{"own Quoted"}})
					s.exec(family.prefix + " OWNER TO own_source")
					s.exec(`DROP ROLE "own Quoted"`)
				}
				s.exec(`CREATE ROLE "own Quoted"`)
			})
		})
	}
}
