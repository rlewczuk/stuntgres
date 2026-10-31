package reference_test

import (
	"database/sql"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatisticsAlterIdentity(t *testing.T) {
	withStatisticsAlter(t, func(t *testing.T, s ndistinctScenario) {
		statisticsAlterSetup(s)
		var owner string
		if err := s.conn.QueryRowContext(s.pair.Ctx, "SELECT current_user").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		oid := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_before'")
		oidText := strconv.FormatInt(oid, 10)
		relation := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_class WHERE relname='sa_input'")
		readerSQL := "SELECT s.stxname,n.nspname,r.rolname,s.stxkeys,s.stxkind,s.stxstattarget FROM pg_statistic_ext s JOIN pg_namespace n ON n.oid=s.stxnamespace JOIN pg_roles r ON r.oid=s.stxowner WHERE s.oid=" + oidText
		held, err := s.conn.PrepareContext(s.pair.Ctx, readerSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var detached *sql.Rows
		var detachedMeta []*sql.ColumnType
		if !s.pg {
			detached, err = s.pair.Native.QueryContext(s.pair.Ctx, readerSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer detached.Close()
			detachedMeta, err = detached.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
		}
		s.run(fixture.Exec("CREATE STATISTICS sa_expression (ndistinct) ON a,(b+1) FROM sa_input", 0))
		s.run(fixture.Exec("ANALYZE sa_input", 0))
		s.run(statisticsAlterExpressionSummary("sa_expression"))
		expressionOID := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_expression'")
		s.run(fixture.Select("SELECT pg_get_expr(stxexprs,stxrelid) AS expression FROM pg_statistic_ext WHERE stxname='sa_expression'", []string{"expression"}, []string{"TEXT"}, [][]any{{"(b + 1)"}}))
		for _, step := range []struct {
			sql, name, schema, owner string
			target                   any
		}{
			{"ALTER STATISTICS public.sa_before RENAME TO sa_after", "sa_after", "public", owner, nil},
			{"ALTER STATISTICS public.sa_after SET SCHEMA sa_destination", "sa_after", "sa_destination", owner, nil},
			{"ALTER STATISTICS sa_destination.sa_after OWNER TO sa_recipient", "sa_after", "sa_destination", "sa_recipient", nil},
			{"ALTER STATISTICS sa_destination.sa_after SET STATISTICS 10000", "sa_after", "sa_destination", "sa_recipient", int64(10000)},
		} {
			s.run(fixture.Exec(step.sql, 0))
			s.run(statisticsAlterMetadata(step.name, step.schema, step.owner, step.target))
			if got := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='"+step.name+"'"); got != oid {
				t.Fatal("alter replaced OID", got, oid)
			}
			if got := fixture.DeparseDefinitionID(t, s.conn, "SELECT stxrelid FROM pg_statistic_ext WHERE stxname='"+step.name+"'"); got != relation {
				t.Fatal("alter moved input relation")
			}
			statisticsAlterFacts(t, s, step.name)
			want := statisticsAlterMetadata(step.name, step.schema, step.owner, step.target)
			want.Query = readerSQL
			backend := s.pair.Backends()[0]
			if s.pg {
				backend = s.pair.Backends()[1]
			}
			fixture.RunStep(t, s.pair.Ctx, fixture.HeldStatement{s.conn, held, readerSQL}, backend.Recorder, want)
		}
		if detached != nil {
			if !detached.Next() {
				t.Fatal("retained catalog row missing", detached.Err())
			}
			var name, schema, oldOwner, keys, kinds string
			var target sql.NullInt64
			if err = detached.Scan(&name, &schema, &oldOwner, &keys, &kinds, &target); err != nil {
				t.Fatal(err)
			}
			if name != "sa_before" || schema != "public" || oldOwner != owner || keys != "1 2" || kinds != "{d,f,m}" || target.Valid {
				t.Fatal("metadata mutation rewrote detached row")
			}
			if len(detachedMeta) != 6 || detachedMeta[5].DatabaseTypeName() != "INT2" {
				t.Fatal("detached metadata changed")
			}
			detached.Close()
		}
		s.run(statisticsError("ALTER STATISTICS public.sa_before SET STATISTICS 0", "42704", `statistics object "public.sa_before" does not exist`))
		// Exact derived namespace/column dependency bag, not just total counts.
		s.run(fixture.Select("SELECT d.refclassid::bigint,d.refobjsubid,d.deptype FROM pg_depend d WHERE d.classid=3381 AND d.objid="+oidText+" ORDER BY d.refclassid,d.refobjsubid,d.deptype", []string{"refclassid", "refobjsubid", "deptype"}, []string{"INT8", "INT4", "CHAR"}, [][]any{{int64(1259), int64(1), "a"}, {int64(1259), int64(2), "a"}, {int64(2615), int64(0), "n"}}))
		s.run(fixture.Select("SELECT n.nspname FROM pg_depend d JOIN pg_namespace n ON n.oid=d.refobjid WHERE d.classid=3381 AND d.objid="+oidText+" AND d.refclassid=2615", []string{"nspname"}, []string{"NAME"}, [][]any{{"sa_destination"}}))
		for _, q := range []string{"ALTER STATISTICS sa_expression RENAME TO sa_expression_after", "ALTER STATISTICS sa_expression_after SET SCHEMA sa_destination", "ALTER STATISTICS sa_destination.sa_expression_after OWNER TO sa_recipient", "ALTER STATISTICS sa_destination.sa_expression_after SET STATISTICS 0", "ALTER TABLE sa_input RENAME COLUMN b TO current_b", "ALTER TABLE sa_input RENAME TO sa_current_input"} {
			s.run(fixture.Exec(q, 0))
		}
		if got := fixture.DeparseDefinitionID(t, s.conn, "SELECT oid FROM pg_statistic_ext WHERE stxname='sa_expression_after'"); got != expressionOID {
			t.Fatal("expression identity changed")
		}
		s.run(fixture.Select("SELECT pg_get_expr(stxexprs,stxrelid) AS expression FROM pg_statistic_ext WHERE stxname='sa_expression_after'", []string{"expression"}, []string{"TEXT"}, [][]any{{"(current_b + 1)"}}))
		s.run(ndistinctRows(s.pg, []any{"sa_after", false, `{"1, 2": 2}`}, []any{"sa_expression_after", false, `{"1, -1": 2}`}))
		s.run(statisticsAlterExpressionSummary("sa_expression_after"))
	})
}
