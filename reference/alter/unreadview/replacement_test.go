package unreadview_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterTypeUnreadViewReplacement(t *testing.T) {
	// Keep the exact empty, no-default/no-row reproduction first. In particular,
	// do not replace it with the populated or migration-shaped witnesses below.
	t.Run("empty_minimal", func(t *testing.T) {
		routes(t, func(s *scenario, mode string) {
			finish := s.begin(mode)
			s.setup(false, false)
			var held *sql.Stmt
			if s.held {
				held = s.prepare(s.conn, ids("v").Query)
			}
			s.replacement()
			finish(true)
			s.run(ids("v"))
			if held != nil {
				s.heldRead(held, ids("v"))
			}
		})
	})
	for _, populated := range []bool{false, true} {
		for _, preexisting := range []bool{false, true} {
			name := "empty"
			if populated {
				name = "populated"
			}
			if preexisting {
				name += "/preexisting"
			} else {
				name += "/same_transaction"
			}
			t.Run(name, func(t *testing.T) {
				routes(t, func(s *scenario, mode string) {
					if preexisting {
						s.setup(populated, true)
					}
					var held *sql.Stmt
					if preexisting && s.held {
						held = s.prepare(s.conn, ids("v").Query)
					}
					finish := s.begin(mode)
					if !preexisting {
						s.setup(populated, true)
						if s.held {
							held = s.prepare(s.conn, ids("v").Query)
						}
					}
					before, old := s.identity(), s.oid("oldenum")
					nested := s.nestedIdentity()
					s.links()
					s.exec("CREATE TYPE replacement AS ENUM ('active','dormant')", 0)
					replacement := s.oid("replacement")
					if replacement == old {
						s.t.Fatal("replacement reused old identity")
					}
					s.exec("ALTER TABLE t ALTER state TYPE replacement USING state::text::replacement", 0)
					s.exec("DROP TYPE oldenum", 0)
					s.exec("ALTER TYPE replacement RENAME TO oldenum", 0)
					// Observe catalogs only on the peer: reading the altered relation
					// itself would legitimately wait for the owner's DDL lock.
					count := int64(0)
					if preexisting {
						count = 1
					}
					s.step(s.peer, fixture.Select("SELECT count(*) FROM pg_type WHERE typname='oldenum'", []string{"count"}, []string{"INT8"}, [][]any{{count}}))
					s.step(s.peer, fixture.Select("SELECT count(*) FROM pg_type WHERE typname='replacement'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					peer := *s
					peer.e = s.peer
					if preexisting && peer.oid("oldenum") != old {
						s.t.Fatal("peer observed uncommitted replacement")
					}
					finish(true)
					if s.oid("oldenum") != replacement {
						s.t.Fatal("rename changed replacement identity")
					}
					if peer.oid("oldenum") != replacement {
						s.t.Fatal("peer did not observe committed replacement")
					}
					s.sameIdentity(before)
					if got := s.nestedIdentity(); !reflect.DeepEqual(got, nested) {
						s.t.Fatal("nested relation/rule identities changed", got, nested)
					}
					s.links()
					values := []int64{}
					if populated {
						values = []int64{1, 2}
					}
					s.graph(values...)
					if held != nil {
						s.heldRead(held, ids("v", values...))
					}
					s.step(s.peer, ids("v", values...))
					if populated {
						s.run(fixture.Select("SELECT id,state::text FROM t ORDER BY id", []string{"id", "state"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "active"}, {int64(2), nil}}))
					}
					s.exec("INSERT INTO t VALUES(3,'dormant')", 1)
					s.exec("INSERT INTO marker VALUES(3)", 1)
					values = append(values, 3)
					s.graph(values...)
					if held != nil {
						s.heldRead(held, ids("v", values...))
					}
					s.run(fixture.Select("SELECT state::text FROM t WHERE id=3", []string{"state"}, []string{"TEXT"}, [][]any{{"dormant"}}))
				})
			})
		}
	}
	t.Run("retained_results", func(t *testing.T) {
		backends(t, func(s *scenario) {
			s.setup(true, true)
			held := s.prepare(s.conn, ids("v").Query)
			rows, err := held.QueryContext(s.p.Ctx)
			if err != nil {
				s.t.Fatal(err)
			}
			defer rows.Close()
			metadata := idMetadata(s.t, rows)
			// PostgreSQL streams hold AccessShare until drained. Native eagerly
			// owns the old result and must preserve it while DDL/new writes occur.
			if s.b.PG {
				consumeIDs(s.t, rows, 1, 2)
			}
			finish := s.begin("go")
			s.replacement()
			finish(true)
			s.exec("INSERT INTO t VALUES(3,'dormant')", 1)
			s.exec("INSERT INTO marker VALUES(3)", 1)
			if !s.b.PG {
				if got := idMetadata(s.t, rows); !reflect.DeepEqual(got, metadata) {
					s.t.Fatal("retained metadata mutated", got, metadata)
				}
				consumeIDs(s.t, rows, 1, 2)
			}
			s.heldRead(held, ids("v", 1, 2, 3))
			s.graph(1, 2, 3)
		})
	})
	t.Run("stable_attributes_and_frozen_projection", stableProjection)
}

type columnMetadata struct {
	Name, Type              string
	Scan                    reflect.Type
	Length                  int64
	LengthKnown             bool
	Precision, Scale        int64
	DecimalKnown            bool
	Nullable, NullableKnown bool
}

func idMetadata(t *testing.T, rows *sql.Rows) columnMetadata {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"id"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 {
		t.Fatal(types, err)
	}
	c := types[0]
	if c.Name() != "id" || c.DatabaseTypeName() != "INT4" {
		t.Fatal(c.Name(), c.DatabaseTypeName())
	}
	m := columnMetadata{Name: c.Name(), Type: c.DatabaseTypeName(), Scan: c.ScanType()}
	m.Length, m.LengthKnown = c.Length()
	m.Precision, m.Scale, m.DecimalKnown = c.DecimalSize()
	m.Nullable, m.NullableKnown = c.Nullable()
	return m
}

func consumeIDs(t *testing.T, rows *sql.Rows, want ...int64) {
	t.Helper()
	for _, expected := range want {
		if !rows.Next() {
			t.Fatal("missing retained row", rows.Err())
		}
		var got int64
		if err := rows.Scan(&got); err != nil || got != expected {
			t.Fatal(got, expected, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained exhaustion", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func stableProjection(t *testing.T) {
	routes(t, func(s *scenario, mode string) {
		s.exec("CREATE TYPE oldenum AS ENUM ('active')", 0)
		s.exec("CREATE TABLE t(discard TEXT,id INT,state oldenum)", 0)
		s.exec("INSERT INTO t VALUES('unused',1,'active'),('unused',2,NULL)", 2)
		s.exec("CREATE VIEW v AS SELECT id FROM t", 0)
		s.exec("CREATE VIEW w AS SELECT * FROM v", 0)
		s.exec("CREATE VIEW self_v AS SELECT a.id FROM t a JOIN t b ON a.id=b.id", 0)
		s.exec("CREATE TABLE marker(id INT)", 0)
		s.exec("INSERT INTO marker VALUES(1),(2)", 2)
		s.exec("CREATE VIEW j AS SELECT * FROM w NATURAL JOIN marker", 0)
		s.exec("CREATE TABLE natural_left(id INT)", 0)
		s.exec("INSERT INTO natural_left VALUES(1),(2)", 2)
		s.exec("CREATE VIEW frozen_natural AS SELECT natural_left.* FROM natural_left NATURAL JOIN marker", 0)
		held := s.prepare(s.conn, ids("self_v").Query)
		before := s.identity()
		s.exec("ALTER TABLE t DROP COLUMN discard", 0)
		s.exec("ALTER TABLE t RENAME COLUMN state TO unread_status", 0)
		s.exec("ALTER TABLE t RENAME TO renamed_source", 0)
		// Newly matching names were not NATURAL keys when frozen_natural was
		// defined; unequal values must not filter its creation-time id match.
		s.exec("ALTER TABLE marker ADD COLUMN later TEXT", 0)
		s.exec("ALTER VIEW w RENAME TO nested_ids", 0)
		s.exec("ALTER TABLE renamed_source ADD COLUMN later TEXT", 0)
		s.exec("UPDATE marker SET later='right'", 2)
		s.exec("UPDATE renamed_source SET later='left'", 2)
		s.exec("ALTER TABLE natural_left ADD COLUMN later TEXT", 0)
		s.exec("UPDATE natural_left SET later='left'", 2)
		finish := s.begin(mode)
		s.exec("CREATE TYPE replacement AS ENUM ('active','dormant')", 0)
		s.exec("ALTER TABLE renamed_source ALTER unread_status TYPE replacement USING unread_status::text::replacement", 0)
		s.exec("DROP TYPE oldenum", 0)
		s.exec("ALTER TYPE replacement RENAME TO oldenum", 0)
		finish(true)
		for _, view := range []string{"v", "nested_ids", "self_v", "j"} {
			s.run(ids(view, 1, 2))
		}
		s.run(ids("frozen_natural", 1, 2))
		s.heldRead(held, ids("self_v", 1, 2))
		if got := s.scalar("SELECT oid::BIGINT FROM pg_class WHERE oid='renamed_source'::regclass"); got != before[0] {
			s.t.Fatal("rename changed source identity")
		}
		if got := s.scalar("SELECT attnum::BIGINT FROM pg_attribute WHERE attrelid='renamed_source'::regclass AND attname='id'"); got != before[1] {
			s.t.Fatal("compaction changed stable read identity")
		}
		s.exec("INSERT INTO renamed_source(id,unread_status,later) VALUES(3,'dormant','left')", 1)
		s.exec("INSERT INTO marker VALUES(3,'right')", 1)
		s.exec("INSERT INTO natural_left VALUES(3,'left')", 1)
		s.run(ids("frozen_natural", 1, 2, 3))
		for _, view := range []string{"v", "nested_ids", "self_v", "j"} {
			s.run(ids(view, 1, 2, 3))
		}
	})
}
