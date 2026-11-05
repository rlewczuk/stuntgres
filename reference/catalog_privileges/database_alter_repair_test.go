package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/lib/pq"
	"stuntgres"
	"stuntgres/reference/fixture"
)

type databaseAlterRepairReader struct {
	source io.Reader
	before func()
}

func (r *databaseAlterRepairReader) Read(p []byte) (int, error) {
	if r.before != nil {
		before := r.before
		r.before = nil
		before()
	}
	return r.source.Read(p)
}

// Explicit canonical native repairs deliberately mutate cluster authority.
// Untargeted replacement OWNER/ACL stays current; supported alias images remain
// replayable. All observations use genuine selected pools.
func databaseAlterExplicitRepairs(c *databaseCreateCase, selected, raw *sql.DB, selectedRec *fixture.Recorder, peer *sql.DB, peerRec *fixture.Recorder, metadata fixture.Step) {
	c.t.Helper()
	var owner string
	var acl sql.NullString
	if err := raw.QueryRowContext(c.p.Ctx, "SELECT spcowner::regrole::text,spcacl::text FROM pg_tablespace WHERE spcname='pg_default'").Scan(&owner, &acl); err != nil {
		c.t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		value := "NULL"
		if acl.Valid {
			value = pq.QuoteLiteral(acl.String)
		}
		cleanup := "UPDATE pg_catalog.pg_tablespace SET spcowner=" + pq.QuoteLiteral(owner) + "::pg_catalog.regrole WHERE spcname='pg_default'::pg_catalog.name; UPDATE pg_catalog.pg_tablespace SET spcacl=" + value + "::pg_catalog.aclitem[] WHERE spcname='pg_default'::pg_catalog.name;"
		if err := stuntgres.Restore(ctx, raw, strings.NewReader(cleanup)); err != nil {
			c.t.Error("restore explicit shared authority", err)
		}
	}()
	query := "SELECT spcowner::regrole::text AS owner_name,has_tablespace_privilege('da_plain','pg_default','CREATE') AS ordinary,has_tablespace_privilege('da_plain','pg_default','CREATE WITH GRANT OPTION') AS delegates,has_tablespace_privilege('da_owner','pg_default','CREATE') AS retained_right FROM pg_tablespace WHERE spcname='pg_default'"
	check := func(owner string, ordinary, delegates bool, created bool) {
		c.t.Helper()
		step := fixture.Select(query, []string{"owner_name", "ordinary", "delegates", "retained_right"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{owner, ordinary, delegates, !created}})
		c.run(step)
		databaseAlterRun(c, selected, selectedRec, step)
		databaseAlterRun(c, peer, peerRec, step)
		var count int64
		if created {
			count = 1
		}
		databaseAlterRun(c, selected, selectedRec, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='da_repair_marker'", []string{"n"}, []string{"INT8"}, [][]any{{count}}))
		c.run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='da_repair_marker'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if !created {
			databaseAlterRun(c, selected, selectedRec, fixture.Select("SELECT v FROM da_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		}
		c.run(fixture.Select("SELECT v FROM da_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		databaseAlterRun(c, peer, peerRec, fixture.Select("SELECT v FROM da_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
		c.run(metadata)
	}
	script := "CREATE TABLE da_repair_marker(v integer); UPDATE pg_catalog.pg_tablespace SET spcowner='da_next'::pg_catalog.regrole WHERE spcname='pg_default'::pg_catalog.name; UPDATE pg_catalog.pg_tablespace SET spcacl='{da_plain=C*/da_next}'::pg_catalog.aclitem[] WHERE spcname='pg_default'::pg_catalog.name;"
	check("postgres", false, false, false)
	invalid := script + "UPDATE pg_catalog.pg_tablespace SET spcacl=NULL::pg_catalog.aclitem[],spcowner='postgres'::pg_catalog.regrole WHERE spcname='pg_default'::pg_catalog.name;"
	fixture.State(c.t, stuntgres.Restore(c.p.Ctx, raw, strings.NewReader(invalid)), "0A000")
	check("postgres", false, false, false)
	ctx, cancel := context.WithCancel(c.p.Ctx)
	canceled := &databaseAlterRepairReader{source: strings.NewReader(script), before: cancel}
	err := stuntgres.Restore(ctx, raw, canceled)
	cancel()
	if !errors.Is(err, context.Canceled) {
		c.t.Fatal("canceled explicit repair", err)
	}
	check("postgres", false, false, false)
	stale := &databaseAlterRepairReader{source: strings.NewReader(script), before: func() { c.run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_default TO da_plain", 0)) }}
	err = stuntgres.Restore(c.p.Ctx, raw, stale)
	if err == nil || err.Error() != "stuntgres: catalog replaced before publication" {
		c.t.Fatal("stale shared predecessor accepted", err)
	}
	check("postgres", true, false, false)
	if err := stuntgres.Restore(c.p.Ctx, raw, strings.NewReader(script)); err != nil {
		c.t.Fatal(err)
	}
	check("da_next", true, true, true)
	// Owner grant options are implicit even with no ordinary owner ACL entry.
	ownerRights := fixture.Select("SELECT has_tablespace_privilege('da_next','pg_default','CREATE') AS ordinary,has_tablespace_privilege('da_next','pg_default','CREATE WITH GRANT OPTION') AS delegates", []string{"ordinary", "delegates"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}})
	c.run(ownerRights)
	databaseAlterRun(c, selected, selectedRec, ownerRights)
	databaseAlterRun(c, peer, peerRec, ownerRights)
}
