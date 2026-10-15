package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceHypotheticalAggregateLifecycle(t *testing.T) {
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		hypotheticalSetup(t, p, c, b)
		q := "SELECT " + hypotheticalProjection("$1", "v", "") + " FROM hs_input"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		old, err := stmt.QueryContext(p.Ctx, 20)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata := func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil || len(columns) != 4 {
				t.Fatal(columns, err)
			}
			for i, column := range columns {
				want := reflect.TypeOf(int64(0))
				if i >= 2 {
					want = reflect.TypeOf(float64(0))
				}
				if column.ScanType() != want {
					t.Fatal("scan authority", i, column.ScanType(), want)
				}
				if _, known := column.Length(); known {
					t.Fatal("unexpected length")
				}
				if _, _, known := column.DecimalSize(); known {
					t.Fatal("unexpected precision")
				}
				nullable, known := column.Nullable()
				if b.PG && known || !b.PG && (!known || nullable) {
					t.Fatal("hypothetical nonnull metadata", nullable, known)
				}
			}
		}
		metadata(t, old)
		writer := c
		if b.PG {
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			writer = peer
		}
		fixture.RunStep(t, p.Ctx, writer, b.Recorder, fixture.Exec("INSERT INTO hs_input VALUES(5)", 1))
		if !old.Next() {
			t.Fatal("retained output missing", old.Err())
		}
		var rank, dense int64
		var percent, cume float64
		if err := old.Scan(&rank, &dense, &percent, &cume); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual([]any{rank, dense, percent, cume}, hypotheticalRow(2, 2, .25, .8)) || old.Next() || old.Err() != nil {
			t.Fatal("retained output changed", rank, dense, percent, cume, old.Err())
		}
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		s := hypotheticalResult(q, hypotheticalRow(3, 3, .4, 5.0/6))
		s.Args = []any{20}
		s.CheckMetadata = metadata
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		for _, tail := range []string{" WHERE FALSE", " WHERE FALSE GROUP BY v"} {
			s := hypotheticalResult("SELECT " + hypotheticalProjection("20", "v", "") + " FROM hs_input" + tail)
			if tail == " WHERE FALSE" {
				s.Rows = [][]any{hypotheticalRow(1, 1, 0, 1)}
			}
			s.CheckMetadata = metadata
			orderedSetBoth(t, p, c, b, s)
		}
	})
}

func TestReferenceHypotheticalAggregateControls(t *testing.T) {
	var emitted string
	hypotheticalScenario(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend) {
		q := "SELECT rank,dense_rank,percent_rank,cume_dist FROM hs_control_view"
		want := hypotheticalResult(q, hypotheticalRow(2, 2, .2, 4.0/6))
		unknown := hypotheticalResult("SELECT rank,dense_rank,percent_rank,cume_dist FROM hs_control_unknown", hypotheticalRow(2, 2, .2, 4.0/6))
		extras := []fixture.Step{
			hypotheticalResult("SELECT rank,dense_rank,percent_rank,cume_dist FROM hs_control_null", hypotheticalRow(4, 3, .6, 4.0/6)),
			hypotheticalResult("SELECT rank,dense_rank,percent_rank,cume_dist FROM hs_control_collated", hypotheticalRow(2, 2, .2, 4.0/6)),
			hypotheticalResult("SELECT rank,dense_rank,percent_rank,cume_dist FROM hs_control_hidden", hypotheticalRow(2, 2, .2, 4.0/6)),
			hypotheticalResult("SELECT rank,dense_rank,percent_rank,cume_dist FROM hs_control_casts", hypotheticalRow(2, 2, .2, 4.0/6)),
		}
		if b.PG {
			if emitted == "" {
				t.Fatal("native emitted SQL missing")
			}
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal(err)
			}
			orderedSetBoth(t, p, c, b, want)
			orderedSetBoth(t, p, c, b, unknown)
			for _, s := range extras {
				orderedSetBoth(t, p, c, b, s)
			}
			return
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN hs_control_domain AS INT", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TYPE hs_control_enum AS ENUM('a','b','z')", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE hs_control(v hs_control_domain,s hs_control_enum,h hs_control_enum)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO hs_control VALUES(1,'a','b'),(1,'b','b'),(1,'b','b'),(2,'a','b'),(NULL,'z','b')", 5))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW hs_control_view AS SELECT "+hypotheticalProjection("1,h", "v,s", "")+" FROM hs_control GROUP BY h", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW hs_control_unknown AS SELECT "+hypotheticalProjection("'1','b'", "v,s", "")+" FROM hs_control", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW hs_control_null AS SELECT "+hypotheticalProjection("'1',NULL::hs_control_enum", "v,s", "")+" FROM hs_control", 0))
		// Contextual enum literals own one leaf annotation. Explicit outer
		// enum and domain/base conversions must survive without cast growth.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW hs_control_casts AS SELECT "+hypotheticalProjection("CAST('1'::hs_control_domain AS int),CAST(CAST('b' AS hs_control_enum) AS hs_control_enum)", "v,s", "")+" FROM hs_control", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW hs_control_collated AS SELECT "+hypotheticalProjection("'1' COLLATE \"C\",'b' COLLATE \"POSIX\"", "v::text,s::text", "")+" FROM hs_control", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE hs_control_secret(h TEXT)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO hs_control_secret VALUES('b')", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW hs_control_hidden AS SELECT "+hypotheticalProjection("1,(SELECT h FROM hs_control_secret)", "v,s::text", "")+" FROM hs_control", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE hs_control_secret RENAME COLUMN h TO hidden_key", 0))
		// The second direct column is not otherwise selected or used by the
		// sort/filter; renaming must remap its dependency and stored expression.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER TABLE hs_control RENAME COLUMN h TO direct_key", 0))
		orderedSetBoth(t, p, c, b, want)
		orderedSetBoth(t, p, c, b, unknown)
		for _, s := range extras {
			orderedSetBoth(t, p, c, b, s)
		}
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		old, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = dump.String()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO hs_control VALUES(0,'a','b')", 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, hypotheticalResult(q, hypotheticalRow(3, 3, 2.0/6, 5.0/7)))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		orderedSetBoth(t, p, c, b, unknown)
		for _, s := range extras {
			orderedSetBoth(t, p, c, b, s)
		}
		if !old.Next() {
			t.Fatal("retained control output missing", old.Err())
		}
		var r, d int64
		var f, g float64
		if err := old.Scan(&r, &d, &f, &g); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual([]any{r, d, f, g}, want.Rows[0]) || old.Next() || old.Err() != nil {
			t.Fatal("retained control output changed")
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if emitted != redump.String() {
			t.Fatalf("unstable control SQL\n%s\n%s", emitted, redump.String())
		}
		connector, err := stuntgres.NewDriver().OpenConnector("hypothetical-controls/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		if err := stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, fresh, b.Recorder, want)
		fixture.RunStep(t, p.Ctx, fresh, b.Recorder, unknown)
		for _, s := range extras {
			fixture.RunStep(t, p.Ctx, fresh, b.Recorder, s)
		}
	})
}
