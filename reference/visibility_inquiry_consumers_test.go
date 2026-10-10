package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReferenceVisibilityInquiryBoundaries(t *testing.T) {
	for _, source := range []bool{false, true} {
		t.Run(strconv.FormatBool(source), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					var executor referenceExecutor = backend.db
					if !source {
						conn, err := backend.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						executor = conn
					}
					run := func(s referenceStep) {
						s.checkMetadata = visibilityBoolMetadata
						s.nonContext = source
						referenceRunStep(t, p.ctx, executor, backend.recorder, s)
					}
					run(referenceExec("CREATE TABLE sentinel(id INT)", 0))
					run(referenceExec("INSERT INTO sentinel VALUES(7)", 1))
					for _, q := range []string{"CREATE TYPE widget AS ENUM ('x')", "CREATE DOMAIN d AS integer", "CREATE COLLATION c FROM pg_catalog.\"C\"", "CREATE CONVERSION c FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1", "CREATE TEMP TABLE temp_t(id INT)", "CREATE MATERIALIZED VIEW mv AS SELECT id FROM sentinel", "CREATE FOREIGN TABLE ft(id integer) SERVER unavailable"} {
						if strings.HasPrefix(q, "CREATE TYPE") {
							run(referenceSelect(q, nil, nil, [][]any{}))
							witness := referenceSelect("SELECT 'x'::widget AS v", []string{"v"}, []string{""}, [][]any{{"x"}})
							witness.nonContext = source
							witness.checkMetadata = enumReferenceMetadata(backend.pg, false)
							enumReferenceRunStep(t, p.ctx, executor, backend.recorder, witness)
							quiet := "CREATE TYPE enum_quiet_exec AS ENUM ('x')"
							var held *sql.Stmt
							var err error
							if source {
								held, err = backend.db.Prepare(quiet)
							} else {
								held, err = executor.PrepareContext(p.ctx, quiet)
							}
							if err != nil {
								t.Fatal(err)
							}
							defer held.Close()
							missing := referenceImplicitError("CREATE TABLE enum_quiet_exec_rows(v enum_quiet_exec)", referenceDiagnostic{Code: "42704", Message: `type "enum_quiet_exec" does not exist`, Position: "37"})
							missing.selectRows, missing.errorStage = true, "execute"
							run(missing)
							if source {
								result, err := held.Exec()
								deleteReferenceCount(t, result, err, 0)
							} else {
								referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: executor, statement: held, query: quiet}, backend.recorder, referenceExec(quiet, 0))
							}
							if err := held.Close(); err != nil {
								t.Fatal(err)
							}
							for _, prepared := range []bool{false, true} {
								value := referenceSelect("SELECT 'x'::enum_quiet_exec AS v", []string{"v"}, []string{""}, [][]any{{"x"}})
								value.prepared, value.nonContext = prepared, source
								value.checkMetadata = enumReferenceMetadata(backend.pg, false)
								enumReferenceRunStep(t, p.ctx, executor, backend.recorder, value)
							}
							run(referenceSelect("SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4519)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
							run(referenceSelect("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
							continue
						}
						if q == "CREATE DOMAIN d AS integer" {
							// The incoming domain implementation supersedes only this
							// former rejection. Query still consumes the original SQL.
							run(referenceSelect(q, nil, nil, [][]any{}))
							witness := referenceSelect("SELECT id::d AS domain_value,pg_type_is_visible(23) AS builtin_visible FROM sentinel", []string{"domain_value", "builtin_visible"}, []string{"INT4", "BOOL"}, [][]any{{int64(7), true}})
							run(witness)
							run(referenceExec("DROP DOMAIN d", 0))
							var held *sql.Stmt
							var err error
							if source {
								held, err = backend.db.Prepare(q)
							} else {
								held, err = executor.PrepareContext(p.ctx, q)
							}
							if err != nil {
								t.Fatal(err)
							}
							defer held.Close()
							// Prepare must not publish the domain before execution.
							missing := referenceImplicitError("SELECT id::d FROM sentinel", referenceDiagnostic{Code: "42704", Message: `type "d" does not exist`, Position: "12"})
							missing.selectRows, missing.errorStage = true, "execute"
							run(missing)
							if source {
								result, err := held.Exec()
								deleteReferenceCount(t, result, err, 0)
							} else {
								referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: executor, statement: held, query: q}, backend.recorder, referenceExec(q, 0))
							}
							if err := held.Close(); err != nil {
								t.Fatal(err)
							}
							run(witness)
							run(referenceSelect("SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4519)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
							run(referenceSelect("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
							continue
						}
						if strings.HasPrefix(q, "CREATE TEMP TABLE") {
							run(referenceSelect(q, nil, nil, [][]any{}))
							run(referenceExec("INSERT INTO temp_t VALUES(42)", 1))
							run(referenceSelect("SELECT id FROM temp_t", []string{"id"}, []string{"INT4"}, [][]any{{int64(42)}}))
							run(referenceExec("DROP TABLE temp_t", 0))
							run(referenceSelect("SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4519)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
							run(referenceSelect("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
							continue
						}
						if strings.HasPrefix(q, "CREATE MATERIALIZED") {
							for _, prepared := range []bool{false, true} {
								creation := referenceExec(q, 1)
								creation.prepared = prepared
								run(creation)
								run(referenceSelect("SELECT id FROM mv", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
								run(referenceExec("DROP MATERIALIZED VIEW mv", 0))
							}
							run(referenceSelect("SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4519)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
							run(referenceSelect("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
							continue
						}
						if backend.pg {
							s := referenceExec(q, 0)
							if strings.HasPrefix(q, "CREATE FOREIGN") {
								s = referenceErrorDiagnostic(q, controlsReferenceDiagnostic("42704", `server "unavailable" does not exist`))
								s.errorStage = "execute"
							}
							run(s)
						} else {
							token := strings.Fields(q)[1]
							want := referenceDiagnostic{Code: "42601", Message: `syntax error at or near "` + token + `"`, Position: "8"}
							for _, prepared := range []bool{false, true} {
								s := referenceImplicitError(q, want)
								s.selectRows = true
								s.prepared = prepared
								if prepared {
									s.errorStage = "prepare"
								} else {
									s.errorStage = "execute"
								}
								run(s)
							}
						}
						run(referenceSelect("SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4519)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
						run(referenceSelect("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					}
					const q = "CREATE TABLE default_boundary(id BOOL DEFAULT pg_type_is_visible(23))"
					if backend.pg {
						run(referenceExec(q, 0))
						run(referenceExec("INSERT INTO default_boundary DEFAULT VALUES", 1))
						run(referenceSelect("SELECT id FROM default_boundary", []string{"id"}, []string{"BOOL"}, [][]any{{true}}))
					} else {
						for _, prepared := range []bool{false, true} {
							s := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "functions are unsupported", Position: strconv.Itoa(strings.Index(q, "pg_type_is_visible") + 1)})
							s.selectRows = true
							s.prepared = prepared
							if prepared {
								s.errorStage = "prepare"
							} else {
								s.errorStage = "execute"
							}
							run(s)
						}
					}
				})
			}
		})
	}
}

func TestReferenceVisibilityInquiryPrivateSequenceCatalog(t *testing.T) {
	deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, pg bool, run func(referenceStep) referenceObserved) {
		cleanup := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			exec := conn.ExecContext
			if pg {
				exec = referenceAdmin.ExecContext
			}
			for _, q := range []string{"SET search_path=public", "DROP SCHEMA IF EXISTS a CASCADE", "DROP SCHEMA IF EXISTS b CASCADE"} {
				if _, err := exec(ctx, q); err != nil {
					t.Error(q, err)
				}
			}
		}
		if pg {
			t.Cleanup(cleanup)
		} else {
			defer cleanup()
		}
		for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.same", "SET search_path=a,b"} {
			run(referenceExec(q, 0))
		}
		var b int64
		if err := conn.QueryRowContext(t.Context(), "SELECT oid FROM pg_catalog.pg_class WHERE relname='same'").Scan(&b); err != nil {
			t.Fatal(err)
		}
		tx, err := conn.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(t.Context(), "CREATE SEQUENCE a.same"); err != nil {
			t.Fatal(err)
		}
		rows, err := tx.QueryContext(t.Context(), "SELECT pg_table_is_visible($1)", b)
		visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{false})
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		rows, err = conn.QueryContext(t.Context(), "SELECT pg_table_is_visible($1)", b)
		visibilityInquiryRows(t, rows, err, "pg_table_is_visible", []any{true})
	})
}

func TestReferenceVisibilityInquiryOffPathCheck(t *testing.T) {
	deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, pg bool, run func(referenceStep) referenceObserved) {
		cleanup := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			exec := conn.ExecContext
			if pg {
				exec = referenceAdmin.ExecContext
			}
			if _, err := exec(ctx, "DROP SCHEMA IF EXISTS off_path CASCADE"); err != nil {
				t.Error(err)
			}
		}
		if pg {
			t.Cleanup(cleanup)
		} else {
			defer cleanup()
		}
		for _, q := range []string{"CREATE SCHEMA off_path", "CREATE TABLE off_path.subject(id INT)", "CREATE TABLE relation_checks(id OID CONSTRAINT known CHECK(pg_table_is_visible(id) IS NOT NULL))"} {
			run(referenceExec(q, 0))
		}
		var oid int64
		if err := conn.QueryRowContext(t.Context(), "SELECT oid FROM pg_catalog.pg_class WHERE relname='subject'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		run(referenceExec("INSERT INTO relation_checks VALUES($1)", 1, oid))
		s := referenceSelect("SELECT pg_table_is_visible(id) FROM relation_checks", []string{"pg_table_is_visible"}, []string{"BOOL"}, [][]any{{false}})
		s.checkMetadata = visibilityBoolMetadata
		run(s)
		run(referenceImplicitError("INSERT INTO relation_checks VALUES(0)", referenceDiagnostic{Code: "23514", Message: `new row for relation "relation_checks" violates check constraint "known"`, Detail: "Failing row contains (0).", Schema: "public", Table: "relation_checks", Constraint: "known"}))
		run(referenceSelect("SELECT count(*) FROM relation_checks", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}

func TestReferenceVisibilityInquiryCatalogPlacement(t *testing.T) {
	deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, pg bool, run func(referenceStep) referenceObserved) {
		cleanup := func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			exec := conn.ExecContext
			if pg {
				exec = referenceAdmin.ExecContext
			}
			if _, err := exec(ctx, "DROP SCHEMA IF EXISTS a CASCADE"); err != nil {
				t.Error(err)
			}
		}
		if pg {
			t.Cleanup(cleanup)
		} else {
			defer cleanup()
		}
		run(referenceExec("CREATE SCHEMA a", 0))
		run(referenceExec("CREATE TABLE a.pg_class(id INT)", 0))
		for _, tc := range []struct {
			path          string
			catalog, user bool
		}{{"a", true, false}, {"a,pg_catalog", false, true}, {"pg_catalog,a", true, false}, {"''", true, false}, {"missing,a", true, false}} {
			run(referenceExec("SET search_path="+tc.path, 0))
			s := referenceSelect("SELECT pg_table_is_visible('pg_catalog.pg_class'::regclass),pg_table_is_visible('a.pg_class'::regclass)", []string{"pg_table_is_visible", "pg_table_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{tc.catalog, tc.user}})
			s.checkMetadata = visibilityBoolMetadata
			run(s)
			run(referenceSelect("SELECT pg_type_is_visible(23),pg_collation_is_visible(811),pg_conversion_is_visible(4402)", []string{"pg_type_is_visible", "pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
		}
	})
}
