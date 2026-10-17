package reference_test

import (
	"context"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// REL_18_6 DefineDomain inherits the immediate base's actual output/send and
// physical facts, replacing input/receive with domain_in/domain_recv. Installation
// addresses come independently from REGPROC, never from the projected pg_type row.
func pgTypeInstalledDomainGoldens(t *testing.T, p *fixture.Pair, b fixture.Backend, schema string) [][]any {
	t.Helper()
	qualified := schema + "."
	baseID := pgTypeID(t, p, b, qualified+"query_int")
	baseArray := pgTypeID(t, p, b, qualified+"query_int[]")
	domainID := pgTypeID(t, p, b, qualified+"pt18_query_domain")
	domainArray := pgTypeID(t, p, b, qualified+"pt18_query_domain[]")
	nestedID := pgTypeID(t, p, b, qualified+"pt18_query_nested")
	nestedArray := pgTypeID(t, p, b, qualified+"pt18_query_nested[]")
	var owner, namespace, input, output int64
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=current_user").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname=$1", schema).Scan(&namespace); err != nil {
		t.Fatal(err)
	}
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT $1::regproc::oid,$2::regproc::oid", qualified+"bqarr_in", qualified+"bqarr_out").Scan(&input, &output); err != nil {
		t.Fatal(err)
	}
	base := []any{baseID, "query_int", namespace, owner, int64(-1), false, "b", "U", false, true, ",", int64(0), int64(0), int64(0), baseArray, input, output, int64(0), int64(0), int64(0), int64(0), int64(0), "i", "p", false, int64(0), int64(-1), int64(0), int64(0), nil, nil, nil}
	domain := pgTypeDomainGolden(base, domainID, "pt18_query_domain", owner, namespace, domainArray, baseID, -1, 0)
	nested := pgTypeDomainGolden(domain, nestedID, "pt18_query_nested", owner, namespace, nestedArray, domainID, -1, 0)
	return [][]any{
		base, pgTypeDynamicArrayGolden(base, baseArray, "_query_int"),
		domain, pgTypeDynamicArrayGolden(domain, domainArray, "_pt18_query_domain"),
		nested, pgTypeDynamicArrayGolden(nested, nestedArray, "_pt18_query_nested"),
	}
}

func TestReferencePGTypeInstalledDomains(t *testing.T) {
	pgTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		schema := "public"
		defer func() {
			if schema != "public" {
				if _, err := b.DB.ExecContext(p.Ctx, "ALTER SCHEMA pt18_query_moved RENAME TO public"); err != nil {
					t.Error("restore public schema name", err)
				}
			}
			if !b.PG {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Error(err)
				}
				return
			}
			if _, err := b.DB.ExecContext(p.Ctx, "DROP EXTENSION IF EXISTS intarray CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		exec := func(q string) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		numericQuery := "SELECT " + pgTypeNumericProjection + " FROM pg_type WHERE oid=$1::oid"
		rawQuery := "SELECT typinput,typoutput,typreceive,typsend FROM pg_type WHERE oid=$1::oid"
		// Actual statements predate domain creation and remain the same across
		// schema publication, teardown and reinstallation.
		numericHeld := fixture.ViewHold(t, p, b, numericQuery)
		// Symbolic REGPROC qualification is checked under an explicit path on
		// one actual physical session, not whichever pool borrower SET selected.
		rawConn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := rawConn.ExecContext(p.Ctx, "RESET search_path"); err != nil {
				t.Error(err)
			}
			if err := rawConn.Close(); err != nil {
				t.Error(err)
			}
		}()
		fixture.RunStep(t, p.Ctx, rawConn, b.Recorder, fixture.Exec("SET search_path=public", 0))
		rawStatement, err := rawConn.PrepareContext(p.Ctx, rawQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer rawStatement.Close()
		rawHeld := fixture.HeldStatement{Executor: rawConn, Statement: rawStatement, SQL: rawQuery}
		install := func() {
			exec("CREATE EXTENSION intarray")
			exec("CREATE DOMAIN pt18_query_domain AS query_int")
			exec("CREATE DOMAIN pt18_query_nested AS pt18_query_domain")
		}
		check := func(t *testing.T, rows [][]any, routinePrefix string) {
			t.Helper()
			routines := [][]any{
				{routinePrefix + "bqarr_in", routinePrefix + "bqarr_out", "-", "-"},
				{"array_in", "array_out", "array_recv", "array_send"},
				{"domain_in", routinePrefix + "bqarr_out", "domain_recv", "-"},
				{"array_in", "array_out", "array_recv", "array_send"},
				{"domain_in", routinePrefix + "bqarr_out", "domain_recv", "-"},
				{"array_in", "array_out", "array_recv", "array_send"},
			}
			for i, row := range rows {
				// Full32 literals include domain input2597/receive2598/send0,
				// immediate base identity at both levels, and canonical companions.
				pgTypeFamilyRow(t, p, b, row)
				step := fixture.Select(numericQuery, pgTypeColumns, pgTypeNumericTypes(), [][]any{row}, row[0])
				fixture.PgDescriptionExecutorRows(t, p, b, numericHeld, step)
				raw := fixture.Select(rawQuery, []string{"typinput", "typoutput", "typreceive", "typsend"}, []string{"REGPROC", "REGPROC", "REGPROC", "REGPROC"}, [][]any{routines[i]}, row[0])
				fixture.PgDescriptionExecutorRows(t, p, b, rawConn, raw)
				fixture.PgDescriptionExecutorRows(t, p, b, rawConn, fixture.Prepared(raw))
				fixture.PgDescriptionExecutorRows(t, p, b, rawHeld, raw)
			}
		}
		install()
		initial := pgTypeInstalledDomainGoldens(t, p, b, schema)
		// Hold a numeric full32 nested-domain cursor across actual replacement;
		// a unique query keeps its recorder observation distinct from held reads.
		snapshotQuery := numericQuery + " /* retained installed domain */"
		snapshotContext := context.WithValue(p.Ctx, fixture.RecorderKey{}, b.Recorder)
		retained, err := b.DB.QueryContext(snapshotContext, snapshotQuery, initial[4][0])
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		saved, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		t.Run("initial", func(t *testing.T) { check(t, initial, "") })
		exec("ALTER SCHEMA public RENAME TO pt18_query_moved")
		schema = "pt18_query_moved"
		moved := pgTypeInstalledDomainGoldens(t, p, b, schema)
		for i := range initial {
			if moved[i][0] != initial[i][0] || moved[i][2] != initial[i][2] || moved[i][16] != initial[i][16] {
				t.Fatal("schema rename changed type, namespace or output identity", initial[i], moved[i])
			}
		}
		t.Run("schema_publication", func(t *testing.T) { check(t, moved, "pt18_query_moved.") })
		// Restore fixture-owned namespace spelling before actual native Reset or
		// PostgreSQL installation teardown, including failure cleanup paths.
		exec("ALTER SCHEMA pt18_query_moved RENAME TO public")
		schema = "public"
		if b.PG {
			exec("DROP EXTENSION intarray CASCADE")
		} else if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		t.Run("removed", func(t *testing.T) {
			for _, row := range initial {
				fixture.PgDescriptionExecutorRows(t, p, b, numericHeld, fixture.Select(numericQuery, pgTypeColumns, pgTypeNumericTypes(), nil, row[0]))
				fixture.PgDescriptionExecutorRows(t, p, b, rawHeld, fixture.Select(rawQuery, []string{"typinput", "typoutput", "typreceive", "typsend"}, []string{"REGPROC", "REGPROC", "REGPROC", "REGPROC"}, nil, row[0]))
			}
		})
		install()
		reinstalled := pgTypeInstalledDomainGoldens(t, p, b, schema)
		t.Run("reinstalled", func(t *testing.T) { check(t, reinstalled, "") })
		t.Run("retained", func(t *testing.T) {
			frozen := fixture.SequenceFrozenRows{Executor: b.DB, Rows: retained, Query: snapshotQuery}
			fixture.PgDescriptionExecutorRows(t, p, b, frozen, fixture.Select(snapshotQuery, pgTypeColumns, pgTypeNumericTypes(), [][]any{initial[4]}, initial[4][0]))
			types := pgTypeNumericTypes()
			if len(saved) != len(pgTypeColumns) {
				t.Fatal("retained column count changed", saved)
			}
			for i, column := range saved {
				if column.Name() != pgTypeColumns[i] || column.DatabaseTypeName() != types[i] {
					t.Fatal("retained description changed", i, column)
				}
			}
		})
	})
}
