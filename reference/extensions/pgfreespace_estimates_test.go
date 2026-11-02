package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgFreespaceEstimates(t *testing.T) {
	t.Run("LivePopulation", func(t *testing.T) {
		fsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fsEmpty(t, p, b, c, "fs_part.heap")
			fsProbe(t, p, b, c, "fs_part.heap", 0, int64(0))
			fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.heap VALUES(1)", 1))
			fsCheck(t, p, b, c, "fs_part.heap", []int64{8096})
			fsRun(t, p, b, c, fixture.Prepared(fixture.Exec("INSERT INTO fs_part.heap VALUES($1)", 1, int64(2))))
			fsCheck(t, p, b, c, "fs_part.heap", []int64{8064})
			fsRun(t, p, b, c, fixture.Exec("DELETE FROM fs_part.heap WHERE k=2", 1))
			fsCheck(t, p, b, c, "fs_part.heap", []int64{8096})
			fsRun(t, p, b, c, fixture.Exec("UPDATE fs_part.heap SET k=3 WHERE k=1", 1))
			fsCheck(t, p, b, c, "fs_part.heap", []int64{8096})
			defer pvRollback(c)
			fsRun(t, p, b, c, fixture.Exec("BEGIN", 0))
			fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.heap VALUES(4)", 1))
			fsCheck(t, p, b, c, "fs_part.heap", []int64{8064})
			fsRun(t, p, b, c, fixture.Exec("DELETE FROM fs_part.heap WHERE k=3", 1))
			fsCheck(t, p, b, c, "fs_part.heap", []int64{8096})
			fsRun(t, p, b, c, fixture.Exec("ROLLBACK", 0))
			fsCheck(t, p, b, c, "fs_part.heap", []int64{8096})
			fsRun(t, p, b, c, fixture.Exec("DELETE FROM fs_part.heap", 1))
			fsCheck(t, p, b, c, "fs_part.heap", nil)
			fsRun(t, p, b, c, fixture.Exec("TRUNCATE fs_part.heap", 0))
			fsEmpty(t, p, b, c, "fs_part.heap")
			fsProbe(t, p, b, c, "fs_part.heap", 0, int64(0))
			fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.heap VALUES(NULL)", 1))
			fsCheck(t, p, b, c, "fs_part.heap", []int64{8128})
			fsRun(t, p, b, c, fixture.Exec("UPDATE fs_part.heap SET k=1", 1))
			fsCheck(t, p, b, c, "fs_part.heap", []int64{8096})
		})
	})
	for _, tc := range []struct {
		n     int
		avail []int64
	}{
		{184, []int64{64}}, {185, []int64{0}}, {186, []int64{0, 8128}}, {187, []int64{0, 8096}},
	} {
		t.Run(fmt.Sprintf("Boundary%d", tc.n), func(t *testing.T) {
			fsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				values := make([]string, tc.n)
				for i := range values {
					values[i] = fmt.Sprintf("(%d)", i)
				}
				fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.heap VALUES"+strings.Join(values, ","), int64(tc.n)))
				fsCheck(t, p, b, c, "fs_part.heap", tc.avail)
			})
		})
	}
	t.Run("Kinds", func(t *testing.T) {
		fsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{
				"CREATE SEQUENCE fs_part.seq START 17",
				"CREATE UNLOGGED TABLE fs_part.unlogged(k integer)",
				"CREATE TEMP TABLE fs_temp(k integer)",
				"INSERT INTO fs_part.heap VALUES(1)",
				"CREATE MATERIALIZED VIEW fs_part.mv AS SELECT k FROM fs_part.heap",
				"CREATE MATERIALIZED VIEW fs_part.unpopulated AS SELECT k FROM fs_part.heap WITH NO DATA",
			} {
				count := int64(0)
				if strings.HasPrefix(q, "INSERT") || q == "CREATE MATERIALIZED VIEW fs_part.mv AS SELECT k FROM fs_part.heap" {
					count = 1
				}
				fsRun(t, p, b, c, fixture.Exec(q, count))
			}
			fsCheck(t, p, b, c, "fs_part.seq", []int64{8096})
			fsBoth(t, p, b, c, fixture.Select("SELECT last_value,is_called FROM fs_part.seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(17), false}}))
			fsCheck(t, p, b, c, "fs_part.mv", []int64{8096})
			fsEmpty(t, p, b, c, "fs_part.unpopulated")
			for _, name := range []string{"fs_part.unlogged", "fs_temp"} {
				fsEmpty(t, p, b, c, name)
				fsRun(t, p, b, c, fixture.Exec("INSERT INTO "+name+" VALUES(1)", 1))
				fsCheck(t, p, b, c, name, []int64{8096})
			}
			for _, am := range []string{"btree", "hash", "gist", "spgist", "brin"} {
				// GiST and SP-GiST use their genuine point operator classes.
				table, column := "heap", "k"
				switch am {
				case "gist", "spgist":
					table = am + "_heap"
					column = "v"
					fsRun(t, p, b, c, fixture.Exec("CREATE TABLE fs_part."+table+"(v point)", 0))
					fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part."+table+" VALUES(point(1,2))", 1))
				}
				name := "fs_part." + am + "_idx"
				fsRun(t, p, b, c, fixture.Exec("CREATE INDEX "+am+"_idx ON fs_part."+table+" USING "+am+"("+column+")", 0))
				fsIndex(t, p, b, c, name, am)
				fsRun(t, p, b, c, fixture.Exec("DELETE FROM fs_part."+table, 1))
				fsIndex(t, p, b, c, name, am)
				if table == "heap" {
					fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.heap VALUES(1)", 1))
				}
			}
		})
	})
	t.Run("VariableWidth", func(t *testing.T) {
		fsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fsRun(t, p, b, c, fixture.Exec("CREATE TABLE fs_part.text_heap(v text)", 0))
			fsRun(t, p, b, c, fixture.Exec("ALTER TABLE fs_part.text_heap SET(autovacuum_enabled=false)", 0))
			fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.text_heap VALUES('a'),('b')", 2))
			// Each row has 24+8+32+4=68 logical bytes, irrespective of text length.
			fsCheck(t, p, b, c, "fs_part.text_heap", []int64{8032})
			fsRun(t, p, b, c, fixture.Prepared(fixture.Exec("UPDATE fs_part.text_heap SET v=$1 WHERE v='b'", 1, strings.Repeat("long", 1000))))
			fsCheck(t, p, b, c, "fs_part.text_heap", []int64{8032})
			fsRun(t, p, b, c, fixture.Exec("DELETE FROM fs_part.text_heap WHERE v='a'", 1))
			fsCheck(t, p, b, c, "fs_part.text_heap", []int64{8096})
		})
	})
	t.Run("VisibleSnapshots", func(t *testing.T) {
		fsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, writer *sql.Conn) {
			reader, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer pvRollback(reader)
			defer pvRollback(writer)
			fsRun(t, p, b, reader, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", 0))
			fsRun(t, p, b, reader, fixture.Select("SELECT k FROM fs_part.heap", []string{"k"}, []string{"INT4"}, nil))
			fsRun(t, p, b, writer, fixture.Exec("INSERT INTO fs_part.heap VALUES(1)", 1))
			// PostgreSQL's physical FSM sees allocated pages independently of the
			// SQL snapshot. Native's named logical model must use that snapshot.
			fsCheck(t, p, b, reader, "fs_part.heap", nil)
			fsCheck(t, p, b, writer, "fs_part.heap", []int64{8096})
			fsRun(t, p, b, writer, fixture.Exec("BEGIN", 0))
			fsRun(t, p, b, writer, fixture.Exec("INSERT INTO fs_part.heap VALUES(2)", 1))
			fsCheck(t, p, b, writer, "fs_part.heap", []int64{8064})
			fsCheck(t, p, b, reader, "fs_part.heap", nil)
			fsRun(t, p, b, writer, fixture.Exec("ROLLBACK", 0))
			fsRun(t, p, b, reader, fixture.Exec("ROLLBACK", 0))
			fsCheck(t, p, b, reader, "fs_part.heap", []int64{8096})
		})
	})
}

func fsIndex(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, am string) {
	t.Helper()
	if b.PG {
		fsCheck(t, p, b, c, name, nil)
		if am == "hash" {
			fsProbe(t, p, b, c, name, 0, int64(0))
		}
		return
	}
	// Native compact index roots have no entirely unused recyclable pages;
	// within-page slack must not be exposed as heap free space.
	var size int64
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_relation_size($1::regclass)", name).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if size < 0 {
		t.Fatal("negative index allocation", size)
	}
	blocks := size / 8192
	if size%8192 != 0 {
		blocks++
	}
	expected := make([]int64, blocks)
	fsCheck(t, p, b, c, name, expected)
}
