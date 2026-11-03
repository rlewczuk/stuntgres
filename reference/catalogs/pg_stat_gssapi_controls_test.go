package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatGSSAPIControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			gssapiSetupSentinel(t, p, b)
			a := gssapiConn(t, p, b.DB)
			peerPool := fixture.CopyScenarioPool(t, p, b.PG, "", "")
			peerPool.SetMaxIdleConns(0)
			peer := gssapiConn(t, p, peerPool)
			aPID, peerPID := gssapiPID(t, p, a), gssapiPID(t, p, peer)
			if aPID == peerPID {
				t.Fatal("physical sessions share PID", aPID)
			}
			query := gssapiQuery + " WHERE pid IN ($1,$2)"
			gssapiBoth(t, p, b, a, query, gssapiTuples(aPID, peerPID), aPID, peerPID)
			statement, err := b.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer statement.Close()
			for _, prepared := range []bool{false, true} {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				var rowsErr error
				if prepared {
					rows, err := statement.QueryContext(ctx, aPID, peerPID)
					rowsErr = err
					if rows != nil {
						rows.Close()
						if !b.PG {
							t.Fatal("canceled native prepared execution published rows")
						}
					}
				} else {
					rows, err := b.DB.QueryContext(ctx, query, aPID, peerPID)
					rowsErr = err
					if rows != nil {
						rows.Close()
						if !b.PG {
							t.Fatal("canceled native execution published rows")
						}
					}
				}
				if !errors.Is(rowsErr, context.Canceled) {
					t.Fatalf("pre-canceled query prepared=%t error=%v", prepared, rowsErr)
				}
				gssapiBoth(t, p, b, a, query, gssapiTuples(aPID, peerPID), aPID, peerPID)
				gssapiSentinel(t, p, b, a)
			}
			if b.PG {
				return
			} // Reset/Restore are native public APIs, not PostgreSQL SQL.

			var baseline bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(baseline.String(), "pg_stat_gssapi") {
				t.Fatal("monitoring history was serialized", baseline.String())
			}
			retained, err := a.QueryContext(p.Ctx, query, aPID, peerPID)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			gssapiConsume(t, retained, false, gssapiTuples(aPID, peerPID))
			gssapiBoth(t, p, b, a, query, gssapiTuples(aPID, peerPID), aPID, peerPID)
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(baseline.String())); err != nil {
				t.Fatal(err)
			}
			gssapiSentinel(t, p, b, a)
			gssapiBoth(t, p, b, a, query, gssapiTuples(aPID, peerPID), aPID, peerPID)
			if gssapiPID(t, p, a) != aPID || gssapiPID(t, p, peer) != peerPID {
				t.Fatal("replacement changed physical identities")
			}

			want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: `relation "gss_replacement" already exists`}
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE gss_replacement(n integer); CREATE TABLE gss_replacement(n integer);"))
			fixture.AssertDiagnostic(t, err, &want)
			gssapiSentinel(t, p, b, a)
			gssapiBoth(t, p, b, a, query, gssapiTuples(aPID, peerPID), aPID, peerPID)
			failure := errors.New("GSSAPI control source failure")
			if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: failure}); !errors.Is(err, failure) {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			if err := stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(ctx, p.Native); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			gssapiSentinel(t, p, b, a)
			gssapiBoth(t, p, b, a, query, gssapiTuples(aPID, peerPID), aPID, peerPID)

			// A newly attached logical target inherits the existing physical cohort,
			// not a duplicate registration or a command-created approximation.
			gssapiExec(t, p, a, "CREATE DATABASE gssapi_controls_peer")
			otherPool := fixture.CopyScenarioPool(t, p, false, "gssapi_controls_peer", "")
			other := gssapiConn(t, p, otherPool)
			gssapiBoth(t, p, b, other, query, gssapiTuples(aPID, peerPID), aPID, peerPID)
			gssapiRetire(t, p, b, a, peerPool, peer, peerPID)
			gssapiClear(t, p, other)
			gssapiBoth(t, p, b, a, query, gssapiTuples(aPID), aPID, peerPID)
			gssapiBoth(t, p, b, other, query, gssapiTuples(aPID), aPID, peerPID)
			gssapiSentinel(t, p, b, a)
		})
	}
}
