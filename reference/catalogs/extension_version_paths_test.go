package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtensionVersionPathsAndMetadata(t *testing.T) {
	extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		q := "SELECT source,target,path FROM pg_catalog.pg_extension_update_paths($1::name) ORDER BY source,target"
		held := extensionVersionHeld(t, p, c, q)
		for _, version := range []string{"", "1.1", "1.2"} {
			if version == "1.1" {
				run(fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
			}
			if version == "1.2" {
				run(fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE", 0))
			}
			var installed any
			if version != "" {
				installed = version
			}
			extensionVersionAvailability(t, p, b, c, installed)
			for _, tc := range []struct {
				name any
				rows [][]any
			}{
				{"fuzzystrmatch", extensionVersionPathRows()},
				{nil, [][]any{}},
			} {
				s := extensionVersionPaths(q, tc.rows, tc.name)
				extensionVersionBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			unaccent := [][]any{}
			if b.PG {
				// Real stock unaccent advertises an older graph-only source;
				// the native provider explicitly supports current-only inventory.
				unaccent = [][]any{{"1.0", "1.1", "1.0--1.1"}, {"1.1", "1.0", nil}}
			}
			s := extensionVersionPaths(q, unaccent, "unaccent")
			extensionVersionBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		for _, name := range []string{"pg_extension_update_paths", "pg_catalog.pg_extension_update_paths"} {
			extensionVersionBoth(t, p, b, c, extensionVersionPaths("SELECT s.source,s.target,s.path FROM "+name+"('fuzzystrmatch') AS s ORDER BY s.source,s.target", extensionVersionPathRows()))
		}
		extensionVersionBoth(t, p, b, c, fixture.Select("SELECT x.a,x.b,x.c FROM pg_extension_update_paths('fuzzystrmatch') AS x(a,b,c) ORDER BY x.a,x.b", []string{"a", "b", "c"}, []string{"TEXT", "TEXT", "TEXT"}, extensionVersionPathRows()))
		// WITH ORDINALITY must not change the pair bag. Do not invent a
		// PostgreSQL guarantee about the underlying enumeration order.
		extensionVersionBoth(t, p, b, c, fixture.Select("SELECT source,target,path FROM pg_extension_update_paths('fuzzystrmatch') WITH ORDINALITY AS p(source,target,path,n) ORDER BY source,target", []string{"source", "target", "path"}, []string{"TEXT", "TEXT", "TEXT"}, extensionVersionPathRows()))
		extensionVersionBoth(t, p, b, c, fixture.Select("SELECT count(*),min(n),max(n) FROM pg_extension_update_paths('fuzzystrmatch') WITH ORDINALITY AS p(source,target,path,n)", []string{"count", "min", "max"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(6), int64(1), int64(6)}}))
		// The target-list record lane returns one composite representation
		// per path; an unreachable path has an empty composite attribute.
		records := [][]any{{"(1.0,1.1,1.0--1.1)"}, {"(1.0,1.2,1.0--1.1--1.2)"}, {"(1.1,1.0,)"}, {"(1.1,1.2,1.1--1.2)"}, {"(1.2,1.0,)"}, {"(1.2,1.1,)"}}
		s := fixture.Select("SELECT pg_extension_update_paths('fuzzystrmatch')", []string{"pg_extension_update_paths"}, []string{"RECORD"}, records)
		s.CheckMetadata = catalogRelationsMetadata(b.PG)
		s.Bag = true
		extensionVersionBoth(t, p, b, c, s)
		extensionVersionBoth(t, p, b, c, extensionVersionPaths("SELECT source,target,path FROM pg_extension_update_paths(NULL::name)", [][]any{}))
		placement := fixture.ImplicitError("SELECT 1 WHERE pg_extension_update_paths('fuzzystrmatch') IS NULL", fixture.Diagnostic{Code: "0A000", Message: "set-returning functions are not allowed in WHERE", Position: "16"})
		placement.SelectRows = true
		extensionVersionBoth(t, p, b, c, placement)
		// The shipped pg_proc identity is independently fixed, not an allocator.
		extensionVersionBoth(t, p, b, c, fixture.Select("SELECT oid::bigint,provolatile,proisstrict,prorettype::bigint FROM pg_proc WHERE oid=3084", []string{"oid", "provolatile", "proisstrict", "prorettype"}, []string{"INT8", "CHAR", "BOOL", "INT8"}, [][]any{{int64(3084), "s", true, int64(2249)}}))
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, executor := range []fixture.Executor{c, held} {
				rows, err := executor.QueryContext(ctx, q, "fuzzystrmatch")
				if rows != nil {
					rows.Close()
					t.Fatal("canceled paths exposed Rows")
				}
				var state interface{ SQLState() string }
				if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
					t.Fatal("ordinary path cancellation", err)
				}
			}
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, extensionVersionPaths(q, extensionVersionPathRows(), "fuzzystrmatch"))
	})
	t.Run("StatementFrontier", func(t *testing.T) {
		extensionVersionCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION fuzzystrmatch VERSION '1.1'", 0))
			q := "SELECT e.extversion,a.installed_version,v.version,v.installed FROM pg_extension e JOIN pg_available_extensions a ON a.name=e.extname JOIN pg_available_extension_versions v ON v.name=e.extname WHERE e.extname='fuzzystrmatch' ORDER BY v.version"
			held := extensionVersionHeld(t, p, c, q)
			rows, err := c.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("ALTER EXTENSION fuzzystrmatch UPDATE TO '1.2'", 0))
			for _, version := range []string{"1.1", "1.2"} {
				if !rows.Next() {
					t.Fatal("retained frontier row", rows.Err())
				}
				var e, a, v string
				var installed bool
				if err := rows.Scan(&e, &a, &v, &installed); err != nil || e != "1.1" || a != "1.1" || v != version || installed != (version == "1.1") {
					t.Fatal("mixed retained frontier", e, a, v, installed, err)
				}
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("frontier EOF", rows.Err())
			}
			rows.Close()
			check := fixture.Select(q, []string{"extversion", "installed_version", "version", "installed"}, []string{"TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{"1.2", "1.2", "1.1", false}, {"1.2", "1.2", "1.2", true}})
			extensionVersionBoth(t, p, b, c, check)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		})
	})
}
