package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

func pgSequenceRetained(t *testing.T, rows *sql.Rows, pg bool, want []any) {
	t.Helper()
	defer rows.Close()
	pgSequenceMetadata(t, rows, pg)
	if !rows.Next() {
		t.Fatal("retained row missing", rows.Err())
	}
	got := make([]any, 8)
	dest := make([]any, 8)
	for i := range dest {
		dest[i] = &got[i]
	}
	if err := rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	for i, v := range got {
		oidReferenceValue(t, pg, pgSequenceTypes[i], v)
	}
	var oid, typ, start, inc, max, min, cache int64
	var cycle bool
	if err := rows.Scan(&oid, &typ, &start, &inc, &max, &min, &cache, &cycle); err != nil {
		t.Fatal(err)
	}
	if r := []any{oid, typ, start, inc, max, min, cache, cycle}; !reflect.DeepEqual(r, want) {
		t.Fatal("retained row", r, "want", want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained cardinality", rows.Err())
	}
}

func TestReferencePGSequenceVisibility(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			a, peer := referenceLockConnections(t, p.ctx, b.db)
			pgSequenceExec(t, p, b, a, "CREATE SEQUENCE pgs_visibility AS INTEGER INCREMENT 7 MINVALUE -20 MAXVALUE 200 START 15 CACHE 9 CYCLE")
			oid := pgDescriptionRelationID(t, p, b, "pgs_visibility")
			original := pgSequenceCustom(oid)
			pgSequenceExec(t, p, b, a, "BEGIN")
			defer a.ExecContext(p.ctx, "ROLLBACK")
			pgSequenceExec(t, p, b, a, "ALTER SEQUENCE pgs_visibility START 21 CACHE 4")
			changed := append([]any(nil), original...)
			changed[2], changed[6] = int64(21), int64(4)
			pgSequenceCheck(t, p, b, a, oid, changed)
			pgSequenceCheck(t, p, b, peer, oid, original)
			pgSequenceExec(t, p, b, a, "SAVEPOINT pgs_save")
			pgSequenceExec(t, p, b, a, "DROP SEQUENCE pgs_visibility")
			pgSequenceCheck(t, p, b, a, oid, nil)
			pgSequenceCheck(t, p, b, peer, oid, original)
			pgSequenceExec(t, p, b, a, "CREATE SEQUENCE pgs_private")
			var private int64
			if err := a.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='pgs_private'").Scan(&private); err != nil {
				t.Fatal(err)
			}
			pgSequenceCheck(t, p, b, a, private, pgSequenceDefault(private))
			pgSequenceCheck(t, p, b, peer, private, nil)
			pgSequenceExec(t, p, b, a, "ROLLBACK TO pgs_save")
			pgSequenceCheck(t, p, b, a, oid, changed)
			pgSequenceCheck(t, p, b, a, private, nil)
			pgSequenceExec(t, p, b, a, "ROLLBACK")
			pgSequenceCheck(t, p, b, a, oid, original)
			pgSequenceCheck(t, p, b, peer, oid, original)
			stmt, err := a.PrepareContext(p.ctx, pgSequenceProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: a, statement: stmt, query: pgSequenceProbe}
			pgDescriptionExecutorRows(t, p, b, held, pgSequenceSelect(b, pgSequenceProbe, [][]any{original}, oid))
			old, err := a.QueryContext(p.ctx, pgSequenceProbe, oid)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			pgSequenceExec(t, p, b, peer, "BEGIN")
			pgSequenceExec(t, p, b, peer, "ALTER SEQUENCE pgs_visibility START 21 CACHE 4 AS BIGINT")
			pgSequenceExec(t, p, b, peer, "COMMIT")
			changed[1] = int64(20)
			pgSequenceRetained(t, old, b.pg, original)
			pgDescriptionExecutorRows(t, p, b, held, pgSequenceSelect(b, pgSequenceProbe, [][]any{changed}, oid))
			old, err = a.QueryContext(p.ctx, pgSequenceProbe, oid)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			pgSequenceExec(t, p, b, peer, "DROP SEQUENCE pgs_visibility")
			pgSequenceRetained(t, old, b.pg, changed)
			pgDescriptionExecutorRows(t, p, b, held, pgSequenceSelect(b, pgSequenceProbe, [][]any{}, oid))
		})
	}
}

func TestReferencePGSequenceRepeatableRead(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			r, peer := referenceLockConnections(t, p.ctx, b.db)
			for _, q := range []string{"CREATE SEQUENCE pgs_rr_kept AS INTEGER INCREMENT 7 MINVALUE -20 MAXVALUE 200 START 15 CACHE 9 CYCLE", "CREATE SEQUENCE pgs_rr_dropped", "CREATE SEQUENCE pgs_rr_own"} {
				pgSequenceExec(t, p, b, peer, q)
			}
			kept := pgDescriptionRelationID(t, p, b, "pgs_rr_kept")
			dropped := pgDescriptionRelationID(t, p, b, "pgs_rr_dropped")
			own := pgDescriptionRelationID(t, p, b, "pgs_rr_own")
			query := "SELECT * FROM pg_catalog.pg_sequence WHERE seqrelid=$1::oid OR seqrelid=$2::oid OR seqrelid=$3::oid"
			pgSequenceExec(t, p, b, r, "BEGIN ISOLATION LEVEL REPEATABLE READ")
			defer r.ExecContext(p.ctx, "ROLLBACK")
			initial := [][]any{pgSequenceCustom(kept), pgSequenceDefault(dropped)}
			pgDescriptionExecutorRows(t, p, b, r, pgSequenceSelect(b, query, initial, kept, dropped, int64(4294967295)))
			stmt, err := r.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: r, statement: stmt, query: query}
			pgDescriptionExecutorRows(t, p, b, held, pgSequenceSelect(b, query, initial, kept, dropped, int64(4294967295)))
			for _, q := range []string{"BEGIN", "ALTER SEQUENCE pgs_rr_kept START 21 CACHE 4", "CREATE SEQUENCE pgs_rr_added", "DROP SEQUENCE pgs_rr_dropped", "COMMIT"} {
				pgSequenceExec(t, p, b, peer, q)
			}
			added := pgDescriptionRelationID(t, p, b, "pgs_rr_added")
			pgDescriptionExecutorRows(t, p, b, r, pgSequenceSelect(b, query, initial, kept, dropped, added))
			pgDescriptionExecutorRows(t, p, b, held, pgSequenceSelect(b, query, initial, kept, dropped, added))
			pgSequenceExec(t, p, b, r, "SAVEPOINT pgs_rr_save")
			pgSequenceExec(t, p, b, r, "ALTER SEQUENCE pgs_rr_own START 31 CACHE 6")
			ownrow := pgSequenceDefault(own)
			ownrow[2], ownrow[6] = int64(31), int64(6)
			pgSequenceCheck(t, p, b, r, own, ownrow)
			pgSequenceExec(t, p, b, r, "CREATE SEQUENCE pgs_rr_private")
			var private int64
			if err := r.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='pgs_rr_private'").Scan(&private); err != nil {
				t.Fatal(err)
			}
			pgSequenceCheck(t, p, b, r, private, pgSequenceDefault(private))
			pgSequenceCheck(t, p, b, peer, private, nil)
			pgSequenceExec(t, p, b, r, "DROP SEQUENCE pgs_rr_own")
			pgSequenceCheck(t, p, b, r, own, nil)
			pgSequenceExec(t, p, b, r, "ROLLBACK TO pgs_rr_save")
			pgSequenceCheck(t, p, b, r, own, pgSequenceDefault(own))
			pgSequenceCheck(t, p, b, r, private, nil)
			pgDescriptionExecutorRows(t, p, b, held, pgSequenceSelect(b, query, initial, kept, dropped, added))
			pgSequenceExec(t, p, b, r, "COMMIT")
			fresh := pgSequenceCustom(kept)
			fresh[2], fresh[6] = int64(21), int64(4)
			pgDescriptionExecutorRows(t, p, b, r, pgSequenceSelect(b, query, [][]any{fresh, pgSequenceDefault(added)}, kept, dropped, added))
		})
	}
}

func TestReferencePGSequenceRepeatableReadFirstPrepare(t *testing.T) {
	for _, before := range []bool{false, true} {
		t.Run(map[bool]string{false: "InsideBegin", true: "BeforeBegin"}[before], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					r, peer := referenceLockConnections(t, p.ctx, b.db)
					pgSequenceExec(t, p, b, peer, "CREATE SEQUENCE pgs_prepare START 15 CACHE 9")
					oid := pgDescriptionRelationID(t, p, b, "pgs_prepare")
					var stmt *sql.Stmt
					var err error
					if before {
						stmt, err = r.PrepareContext(p.ctx, pgSequenceProbe)
						if err != nil {
							t.Fatal(err)
						}
					}
					pgSequenceExec(t, p, b, r, "BEGIN ISOLATION LEVEL REPEATABLE READ")
					defer r.ExecContext(p.ctx, "ROLLBACK")
					if !before {
						stmt, err = r.PrepareContext(p.ctx, pgSequenceProbe)
						if err != nil {
							t.Fatal(err)
						}
					}
					defer stmt.Close()
					pgSequenceExec(t, p, b, peer, "BEGIN")
					pgSequenceExec(t, p, b, peer, "ALTER SEQUENCE pgs_prepare START 21 CACHE 4")
					pgSequenceExec(t, p, b, peer, "COMMIT")
					row := pgSequenceDefault(oid)
					row[2], row[6] = int64(15), int64(9)
					// Preparing inside RR retains the definition before the peer commit;
					// preparing before BEGIN leaves acquisition to the first Execute.
					if before {
						row[2], row[6] = int64(21), int64(4)
					}
					held := referenceHeldStatement{referenceExecutor: r, statement: stmt, query: pgSequenceProbe}
					pgDescriptionExecutorRows(t, p, b, held, pgSequenceSelect(b, pgSequenceProbe, [][]any{row}, oid))
					pgSequenceCheck(t, p, b, r, oid, row)
					pgSequenceExec(t, p, b, peer, "ALTER SEQUENCE pgs_prepare START 31 CACHE 6")
					pgDescriptionExecutorRows(t, p, b, held, pgSequenceSelect(b, pgSequenceProbe, [][]any{row}, oid))
					pgSequenceCheck(t, p, b, r, oid, row)
					pgSequenceExec(t, p, b, r, "COMMIT")
					row[2], row[6] = int64(31), int64(6)
					pgSequenceCheck(t, p, b, r, oid, row)
				})
			}
		})
	}
}
