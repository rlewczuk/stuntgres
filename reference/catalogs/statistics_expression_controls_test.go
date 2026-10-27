package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsExpressionControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"expr_stat_owner", "expr_table_owner"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		for _, q := range []string{"CREATE ROLE expr_stat_owner", "CREATE ROLE expr_table_owner"} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
	}
	native := p.Backends()[0]
	conn, err := native.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, conn, native.Recorder, step) }
	defer conn.Close()
	for _, q := range []string{"GRANT CREATE,USAGE ON SCHEMA public TO expr_stat_owner", "CREATE TABLE expr_t(spare INT,a INT,b INT)", "ALTER TABLE expr_t OWNER TO expr_stat_owner", "SET ROLE expr_stat_owner", "CREATE STATISTICS expr_single ON (a+1) FROM expr_t", "CREATE STATISTICS expr_joint(ndistinct) ON (a+1),b FROM expr_t", "RESET ROLE"} {
		run(fixture.Exec(q, 0))
	}
	values := make([]string, 0, 20)
	for range 14 {
		values = append(values, "(0,1,10)")
	}
	for range 2 {
		values = append(values, "(0,2,20)")
	}
	for range 4 {
		values = append(values, "(0,NULL,NULL)")
	}
	run(fixture.Exec("INSERT INTO expr_t VALUES"+strings.Join(values, ","), 20))
	for _, q := range []string{"ANALYZE expr_t", "UPDATE expr_t SET a=9,b=90", "ALTER TABLE expr_t RENAME COLUMN a TO renamed", "ALTER TABLE expr_t DROP COLUMN spare", "ALTER TABLE expr_t OWNER TO expr_table_owner", "REVOKE CREATE ON SCHEMA public FROM expr_stat_owner"} {
		count := int64(0)
		if strings.HasPrefix(q, "UPDATE") {
			count = 20
		}
		run(fixture.Exec(q, count))
		t.Logf("expression controls phase: after %s", q)
		run(expressionSkew("expr_single", 1, "{2,3}"))
	}
	run(expressionSkew("expr_single", 1, "{2,3}"))
	old, err := conn.QueryContext(p.Ctx, "SELECT (stxdexpr[1]).stavalues1::text"+expressionDataFrom, "expr_single")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	var saved bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Len() == 0 {
		t.Fatal("empty expression dump")
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			executor := fixture.Executor(b.DB)
			if b.PG {
				c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err := c.ExecContext(p.Ctx, saved.String()); err != nil {
					t.Fatal("actual expression declaration replay", err)
				}
				if _, err := c.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION DEFAULT"); err != nil {
					t.Fatal(err)
				}
				executor = c
			} else {
				assertExpressionRestoreFailures(t, p, saved.Bytes())
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if !old.Next() {
					t.Fatal("retained expression row lost", old.Err())
				}
				var value string
				if err := old.Scan(&value); err != nil || value != "{2,3}" {
					t.Fatal(value, err)
				}
				old.Close()
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
					t.Fatal(err)
				}
			}
			check := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, executor, b.Recorder, step) }
			check(expressionDefinition("expr_single", "", "{e}"))
			check(expressionDefinition("expr_joint", "3", "{d,e}"))
			check(fixture.Select("SELECT pg_get_userbyid(s.stxowner) AS statistics_owner,pg_get_userbyid(c.relowner) AS table_owner FROM pg_statistic_ext s JOIN pg_class c ON c.oid=s.stxrelid WHERE s.stxname='expr_single'", []string{"statistics_owner", "table_owner"}, []string{"NAME", "NAME"}, [][]any{{"expr_stat_owner", "expr_table_owner"}}))
			check(fixture.Select("SELECT min(renamed),max(renamed),count(*) FROM expr_t", []string{"min", "max", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(9), int64(9), int64(20)}}))
			if b.PG {
				// Native semantic comments do not pretend to restore PostgreSQL's
				// physical statistics. Genuine replayed definitions are initially unbuilt.
				check(expressionMissingData())
			} else {
				check(expressionSkew("expr_single", 1, "{2,3}"))
				check(expressionJoint(false, "expr_joint", `{"3, -1": 3}`, 1))
				var again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(saved.Bytes(), again.Bytes()) {
					t.Fatal("expression redump not deterministic")
				}
				expressionRemint(t, p, saved.Bytes())
			}
			check(fixture.Exec("ANALYZE expr_t", 0))
			check(expressionUniform("expr_single", "{10}"))
			check(expressionJoint(b.PG, "expr_joint", `{"3, -1": 1}`, 1))
		})
	}
}

func assertExpressionRestoreFailures(t *testing.T, p *fixture.Pair, saved []byte) {
	t.Helper()
	if _, err := p.Native.ExecContext(p.Ctx, "CREATE TABLE expr_destination_sentinel(v INT); INSERT INTO expr_destination_sentinel VALUES(47)"); err != nil {
		t.Fatal(err)
	}
	assert := func() {
		var value int
		if err := p.Native.QueryRowContext(p.Ctx, "SELECT v FROM expr_destination_sentinel").Scan(&value); err != nil || value != 47 {
			t.Fatal("failed restore replaced destination", value, err)
		}
		fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, expressionSkew("expr_single", 1, "{2,3}"))
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(saved)); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled restore", err)
	}
	assert()
	for _, suffix := range []string{"CREATE TABLE broken(", "-- stuntgres-ndistinct-v1 !!!\n"} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(string(saved)+"\n"+suffix)); err == nil {
			t.Fatal("malformed restore accepted")
		}
		assert()
	}
	// Corrupt a real source semantic record, never a handwritten substitute.
	found := false
	for _, line := range strings.Split(string(saved), "\n") {
		if strings.HasPrefix(line, "-- stuntgres-ndistinct-v1 ") {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "-- stuntgres-ndistinct-v1 "))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(raw, []byte(`"expressions"`)) {
				continue
			}
			found = true
			corrupt := bytes.Replace(raw, []byte(`"null_count":4`), []byte(`"null_count":999999`), 1)
			if bytes.Equal(raw, corrupt) {
				t.Fatal("expression semantic record lacks expected independent null count")
			}
			malformed := strings.Replace(string(saved), line, "-- stuntgres-ndistinct-v1 "+base64.StdEncoding.EncodeToString(corrupt), 1)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(malformed)); err == nil {
				t.Fatal("invalid semantic null count accepted")
			}
			assert()
			break
		}
	}
	if !found {
		t.Fatal("expression facts missing from actual semantic dump")
	}
	if _, err := p.Native.ExecContext(p.Ctx, "DROP TABLE expr_destination_sentinel"); err != nil {
		t.Fatal(err)
	}
}

func expressionRemint(t *testing.T, p *fixture.Pair, saved []byte) {
	t.Helper()
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector(t.Name() + "-remint")
	if err != nil {
		t.Fatal(err)
	}
	other := sql.OpenDB(connector)
	defer other.Close()
	recorded, recorder := fixture.RecordedNativePool(t, connector)
	if _, err := other.ExecContext(p.Ctx, "CREATE ROLE expr_stat_owner; CREATE ROLE expr_table_owner; CREATE TABLE collision(v INT)"); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, other, bytes.NewReader(saved)); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, recorded, recorder, expressionSkew("expr_single", 1, "{2,3}"))
	fixture.RunStep(t, p.Ctx, recorded, recorder, expressionJoint(false, "expr_joint", `{"3, -1": 3}`, 1))
	fixture.RunStep(t, p.Ctx, recorded, recorder, fixture.Exec("ANALYZE expr_t", 0))
	fixture.RunStep(t, p.Ctx, recorded, recorder, expressionUniform("expr_single", "{10}"))
}
