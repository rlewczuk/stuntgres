package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
)

func TestReferenceStatisticsNDistinctControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"nd_ctl_owner"})
	p := fixture.NewScenario(t)
	native := p.Backends()[0]
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, native.DB, native.Recorder, s) }
	for _, q := range []string{"CREATE ROLE nd_ctl_owner", "GRANT CREATE,USAGE ON SCHEMA public TO nd_ctl_owner", "SET ROLE nd_ctl_owner", "CREATE TABLE nd_t(a INT,hole INT,b INT)", "ALTER TABLE nd_t DROP COLUMN hole", "CREATE STATISTICS nd_s(ndistinct) ON a,b FROM nd_t", "CREATE STATISTICS nd_absent(ndistinct) ON a,b FROM nd_t", "ALTER STATISTICS nd_absent SET STATISTICS 0", "RESET ROLE", "CREATE TABLE nd_p(a INT,b INT)", "CREATE TABLE nd_c() INHERITS(nd_p)", "CREATE STATISTICS nd_p_s(ndistinct) ON a,b FROM nd_p", "CREATE STATISTICS nd_c_s(ndistinct) ON a,b FROM nd_c", "CREATE TABLE nd_part(a INT,b INT) PARTITION BY RANGE(a)", "CREATE TABLE nd_leaf PARTITION OF nd_part FOR VALUES FROM(0) TO(10)", "CREATE STATISTICS nd_part_s(ndistinct) ON a,b FROM nd_part"} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO nd_t VALUES(1,1),(2,2)", 2))
	run(fixture.Exec("ANALYZE nd_t", 0))
	run(fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS 0", 0))
	run(fixture.Exec("INSERT INTO nd_t VALUES(3,3)", 1))
	run(fixture.Exec("INSERT INTO nd_p VALUES(1,1),(1,1)", 2))
	run(fixture.Exec("INSERT INTO nd_c VALUES(2,2),(2,2),(2,2)", 3))
	run(fixture.Exec("ANALYZE nd_p", 0))
	run(fixture.Exec("INSERT INTO nd_part VALUES(4,4)", 1))
	run(fixture.Exec("ANALYZE nd_part", 0))
	want := func(pg bool) fixture.Step {
		return ndistinctRows(pg, []any{"nd_c_s", false, `{"1, 2": 1}`}, []any{"nd_p_s", false, `{"1, 2": 1}`}, []any{"nd_p_s", true, `{"1, 2": 2}`}, []any{"nd_part_s", true, `{"1, 2": 1}`}, []any{"nd_s", false, `{"1, 3": 2}`})
	}
	run(want(false))
	var saved bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(saved.String(), "-- stuntgres-ndistinct-v1 ") {
		t.Fatal("no collected state in actual dump")
	}
	gate := &fixture.CatalogControlGatedWriter{Started: make(chan struct{}), Release: make(chan struct{})}
	dumped := make(chan error, 1)
	var releaseDump sync.Once
	dumpJoined := false
	go func() { dumped <- stuntgres.Dump(p.Ctx, p.Native, gate) }()
	defer func() {
		releaseDump.Do(func() { close(gate.Release) })
		if !dumpJoined {
			<-dumped
		}
	}()
	select {
	case <-gate.Started:
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	// Real target work completes while the captured dump is blocked in external I/O.
	run(fixture.Exec("INSERT INTO nd_t VALUES(4,4)", 1))
	run(want(false))
	releaseDump.Do(func() { close(gate.Release) })
	dumpErr := <-dumped
	dumpJoined = true
	if dumpErr != nil {
		t.Fatal(dumpErr)
	}
	if !bytes.Equal(saved.Bytes(), gate.Output.Bytes()) {
		t.Fatal("dump read mutable engine state after capture")
	}
	reader := &fixture.CatalogControlGatedReader{Started: make(chan struct{}), Release: make(chan struct{})}
	restoreContext, cancelRestore := context.WithCancel(p.Ctx)
	defer cancelRestore()
	restored := make(chan error, 1)
	var releaseRestore sync.Once
	restoreJoined := false
	go func() {
		restored <- stuntgres.Restore(restoreContext, p.Native, io.MultiReader(reader, bytes.NewReader(saved.Bytes())))
	}()
	defer func() {
		cancelRestore()
		releaseRestore.Do(func() { close(reader.Release) })
		if !restoreJoined {
			<-restored
		}
	}()
	select {
	case <-reader.Started:
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
	}
	run(want(false))
	cancelRestore()
	releaseRestore.Do(func() { close(reader.Release) })
	restoreErr := <-restored
	restoreJoined = true
	if !errors.Is(restoreErr, context.Canceled) {
		t.Fatal("blocked Restore cancellation", restoreErr)
	}
	run(want(false))
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			if b.PG {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE ROLE nd_ctl_owner", 0))
				conn := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
				defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				if _, err := conn.ExecContext(p.Ctx, saved.String()); err != nil {
					t.Fatal("actual emitted script rejected", err)
				}
				if _, err := conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION DEFAULT"); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, ndistinctRows(true))
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT stxname,stxstattarget,stxkeys,pg_get_userbyid(stxowner) AS owner FROM pg_statistic_ext WHERE stxname IN('nd_s','nd_absent') ORDER BY stxname", []string{"stxname", "stxstattarget", "stxkeys", "owner"}, []string{"NAME", "INT2", "INT2VECTOR", "NAME"}, [][]any{{"nd_absent", int64(0), "1 3", "nd_ctl_owner"}, {"nd_s", int64(0), "1 3", "nd_ctl_owner"}}))
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS DEFAULT", 0))
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("ANALYZE nd_t", 0))
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, ndistinctRows(true, []any{"nd_s", false, `{"1, 3": 3}`}))
				return
			}
			old, err := p.Native.QueryContext(p.Ctx, "SELECT stxdndistinct FROM pg_statistic_ext_data WHERE stxoid=(SELECT oid FROM pg_statistic_ext WHERE stxname='nd_s')")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			meta, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			var recordLine string
			for _, line := range strings.Split(saved.String(), "\n") {
				if strings.HasPrefix(line, "-- stuntgres-ndistinct-v1 ") {
					recordLine = line
					break
				}
			}
			encoded := strings.TrimPrefix(recordLine, "-- stuntgres-ndistinct-v1 ")
			raw, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatal(err)
			}
			corrupt := strings.Replace(string(raw), `"counts":[1]`, `"counts":[0]`, 1)
			for _, suffix := range []string{recordLine + "\n", "-- stuntgres-ndistinct-v2 e30=\n", "-- stuntgres-ndistinct-v1 !!!\n", "-- stuntgres-ndistinct-v1 " + base64.StdEncoding.EncodeToString([]byte(corrupt)) + "\n"} {
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(saved.String()+suffix)); err == nil {
					t.Fatal("corrupt native state accepted", suffix)
				}
				run(want(false))
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(saved.Bytes())); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			run(want(false))
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(ndistinctRows(false))
			if !old.Next() {
				t.Fatal("held old row lost", old.Err())
			}
			var value string
			if err := old.Scan(&value); err != nil || value != `{"1, 3": 2}` {
				t.Fatal(value, err)
			}
			old.Close()
			if meta[0].DatabaseTypeName() != "PG_NDISTINCT" {
				t.Fatal(meta[0])
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(want(false))
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(saved.Bytes(), again.Bytes()) {
				t.Fatal("state redump nondeterministic", saved.String(), again.String())
			}
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector(t.Name() + "-collision")
			if err != nil {
				t.Fatal(err)
			}
			other := sql.OpenDB(connector)
			defer other.Close()
			recordedOther, otherRecorder := fixture.RecordedNativePool(t, connector)
			if _, err := other.ExecContext(p.Ctx, "CREATE ROLE nd_ctl_owner; CREATE TABLE collision(v INT)"); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, other, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, recordedOther, otherRecorder, want(false))
			fixture.RunStep(t, p.Ctx, recordedOther, otherRecorder, fixture.Exec("ALTER STATISTICS nd_s SET STATISTICS DEFAULT", 0))
			fixture.RunStep(t, p.Ctx, recordedOther, otherRecorder, fixture.Exec("ANALYZE nd_t", 0))
			fixture.RunStep(t, p.Ctx, recordedOther, otherRecorder, ndistinctRows(false, []any{"nd_c_s", false, `{"1, 2": 1}`}, []any{"nd_p_s", false, `{"1, 2": 1}`}, []any{"nd_p_s", true, `{"1, 2": 2}`}, []any{"nd_part_s", true, `{"1, 2": 1}`}, []any{"nd_s", false, `{"1, 3": 3}`}))
		})
	}
}
