package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func pgNamespaceTempPair(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, object string, actor int64) (string, int64, string, int64) {
	t.Helper()
	rows, err := db.QueryContext(p.Ctx, "SELECT n.oid,n.nspname,c.relowner FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.relname=$1", object)
	if err != nil {
		t.Fatal(err)
	}
	var id, owner int64
	var name string
	if !rows.Next() {
		rows.Close()
		t.Fatal("TEMP table metadata missing", rows.Err())
	}
	if err := rows.Scan(&id, &name, &owner); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		rows.Close()
		t.Fatal("TEMP metadata multiplicity", rows.Err())
	}
	rows.Close()
	suffix, ok := strings.CutPrefix(name, "pg_temp_")
	if !ok {
		t.Fatal("non-TEMP namespace", name)
	}
	if n, err := strconv.ParseUint(suffix, 10, 64); err != nil || n == 0 {
		t.Fatal("invalid physical suffix", name, err)
	}
	if owner != actor {
		t.Fatal("TEMP object owner", owner, actor)
	}
	toast := "pg_toast_temp_" + suffix
	tid := pgNamespaceID(t, p, db, toast)
	if tid == id || id == 11 || id == 99 || id == 2200 {
		t.Fatal("TEMP pair aliases identity", id, tid)
	}
	pgNamespaceRow(t, p, b, db, name, id, 10, nil)
	pgNamespaceRow(t, p, b, db, toast, tid, 10, nil)
	return name, id, toast, tid
}

func TestReferencePGNamespaceTemporary(t *testing.T) {
	t.Run("FirstUseTransactions", pgNamespaceTemporaryTransactions)
	pgNamespaceScenario(t, nil, []string{"ns18_temp_actor", "ns18_temp_reader"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		creator := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		peer := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		var pid int
		if b.PG {
			if err := creator.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
		}
		pgNamespaceExec(t, p, b, creator, "CREATE ROLE ns18_temp_actor", "CREATE ROLE ns18_temp_reader", "ALTER DEFAULT PRIVILEGES FOR ROLE ns18_temp_actor GRANT USAGE,CREATE ON SCHEMAS TO ns18_temp_reader", "SET ROLE ns18_temp_actor", "CREATE TEMP TABLE ns18_temp_obj(i INT)")
		actor := pgNamespaceRoleID(t, p, creator, "ns18_temp_actor")
		name, id, toast, tid := pgNamespaceTempPair(t, p, b, creator, "ns18_temp_obj", actor)
		fixture.RunStep(t, p.Ctx, creator, b.Recorder, fixture.Exec("INSERT INTO pg_temp.ns18_temp_obj VALUES(18)", 1))
		pgNamespaceCheck(t, p, b, creator, fixture.Select("SELECT i FROM pg_temp.ns18_temp_obj", []string{"i"}, []string{"INT4"}, [][]any{{int64(18)}}))
		pgNamespaceExec(t, p, b, peer, "SET ROLE ns18_temp_reader")
		pgNamespaceRow(t, p, b, peer, name, id, 10, nil)
		pgNamespaceRow(t, p, b, peer, toast, tid, 10, nil)
		for _, prepared := range []bool{false, true} {
			s := fixture.SchemaReferenceError("SELECT i FROM "+name+".ns18_temp_obj", "42501", "permission denied for schema "+name, "", prepared, "execute", "15")
			s.SelectRows = true
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, s)
			s = fixture.SchemaReferenceCreationError("CREATE TABLE "+toast+".ns18_toast_denied(i INT)", "42501", "permission denied for schema "+toast, prepared, "14")
			fixture.RunStep(t, p.Ctx, creator, b.Recorder, s)
		}
		pgNamespaceExec(t, p, b, creator, "SET search_path TO "+toast+",pg_temp")
		pgNamespaceCheck(t, p, b, creator, fixture.Select("SELECT i FROM ns18_temp_obj", []string{"i"}, []string{"INT4"}, [][]any{{int64(18)}}))
		pgNamespaceExec(t, p, b, creator, "CREATE TABLE ns18_temp_path(i INT)")
		pgNamespaceCheck(t, p, b, creator, fixture.Select("SELECT relnamespace,relpersistence FROM pg_class WHERE relname='ns18_temp_path'", []string{"relnamespace", "relpersistence"}, []string{"OID", "CHAR"}, [][]any{{id, "t"}}))
		pgNamespaceExec(t, p, b, creator, "RESET ROLE", "ALTER DEFAULT PRIVILEGES FOR ROLE ns18_temp_actor REVOKE USAGE,CREATE ON SCHEMAS FROM ns18_temp_reader")
		// ErrBadConn discards the actual physical driver connection, not a pooled borrower.
		err := creator.Raw(func(any) error { return driver.ErrBadConn })
		if !errors.Is(err, driver.ErrBadConn) {
			t.Fatal("physical retirement", err)
		}
		if b.PG {
			for {
				var live bool
				if err := peer.QueryRowContext(p.Ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&live); err != nil {
					t.Fatal("observing physical exit", err)
				}
				if !live {
					break
				}
				runtime.Gosched()
			}
		}
		pgNamespaceRow(t, p, b, peer, name, id, 10, nil)
		pgNamespaceRow(t, p, b, peer, toast, tid, 10, nil)
		pgNamespaceCheck(t, p, b, peer, fixture.Select("SELECT relname FROM pg_class WHERE relnamespace=$1", []string{"relname"}, []string{"NAME"}, nil, id))
		pgNamespaceExec(t, p, b, peer, "RESET ROLE")
		replacement := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgNamespaceExec(t, p, b, replacement, "SET ROLE ns18_temp_actor")
		for _, prepared := range []bool{false, true} {
			s := fixture.SchemaReferenceCreationError("CREATE TABLE "+name+".ns18_retired_denied(i INT)", "42501", "permission denied for schema "+name, prepared, "14")
			fixture.RunStep(t, p.Ctx, replacement, b.Recorder, s)
		}
		pgNamespaceExec(t, p, b, replacement, "RESET ROLE")
	})
}

func pgNamespaceTemporaryTransactions(t *testing.T) {
	pgNamespaceScenario(t, nil, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		db := b.DB
		if b.PG {
			u, err := url.Parse(fixture.DSN)
			if err != nil {
				t.Fatal(err)
			}
			u.Path = "/postgres"
			db, err = sql.Open("stuntgres_reference_postgres", u.String())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
		}
		peer := fixture.SequenceCoreConn(t, p.Ctx, db)
		creator := fixture.SequenceCoreConn(t, p.Ctx, db)
		for _, mode := range []struct{ prepared, commit bool }{{false, false}, {true, false}, {true, true}} {
			pgNamespaceExec(t, p, b, creator, "BEGIN")
			s := fixture.Exec("CREATE TEMP TABLE ns18_temp_tx(i INT)", 0)
			s.Prepared = mode.prepared
			fixture.RunStep(t, p.Ctx, creator, b.Recorder, s)
			actor := int64(10)
			name, id, toast, tid := pgNamespaceTempPair(t, p, b, creator, "ns18_temp_tx", actor)
			for _, n := range []string{name, toast} {
				pgNamespaceCheck(t, p, b, peer, fixture.Select("SELECT nspname FROM pg_namespace WHERE nspname=$1", []string{"nspname"}, []string{"NAME"}, nil, n))
			}
			if mode.commit {
				pgNamespaceExec(t, p, b, creator, "COMMIT")
				pgNamespaceRow(t, p, b, peer, name, id, 10, nil)
				pgNamespaceRow(t, p, b, peer, toast, tid, 10, nil)
			} else {
				pgNamespaceExec(t, p, b, creator, "ROLLBACK")
				for _, n := range []string{name, toast} {
					pgNamespaceCheck(t, p, b, peer, fixture.Select("SELECT nspname FROM pg_namespace WHERE nspname=$1", []string{"nspname"}, []string{"NAME"}, nil, n))
				}
			}
		}
	})
}
