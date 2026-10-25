package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryRelationClosingEdge(t *testing.T) {
	geometryRelationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// REL_18_6 dist_ppoly_internal initializes its closing candidate first ->
		// last. Epsilon-horizontal/vertical promotion anchors at the first vertex;
		// reversing that candidate would give 1.0000005 instead of exactly 1.
		const horizontal = "((0,0),(0,10),(2,0.0000005))"
		const vertical = "((0,0),(10,0),(0.0000005,2))"
		geometryRelationEdges(t, p, b, c, []geometryRelationCase{
			{"<->", "point", "polygon", "(1,-1)", horizontal, "FLOAT8", float64(1)},
			{"<->", "polygon", "point", horizontal, "(1,-1)", "FLOAT8", float64(1)},
			{"<->", "point", "polygon", "(-1,1)", vertical, "FLOAT8", float64(1)},
			{"<->", "polygon", "point", vertical, "(-1,1)", "FLOAT8", float64(1)},
			{"<->", "circle", "polygon", "<(1,-1),0.25>", horizontal, "FLOAT8", float64(.75)},
			{"<->", "polygon", "circle", horizontal, "<(1,-1),0.25>", "FLOAT8", float64(.75)},
			{"<->", "circle", "polygon", "<(-1,1),0.25>", vertical, "FLOAT8", float64(.75)},
			{"<->", "polygon", "circle", vertical, "<(-1,1),0.25>", "FLOAT8", float64(.75)},
		})
	})
}

func TestReferenceGeometryRelationInvalidUnknown(t *testing.T) {
	geometryRelationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Checkpoint A observed same-type LSEG inference, not shape-based POINT
		// inference: conversion fails at the unknown literal (character position 8).
		const q = "SELECT '(1,1)' ## lseg '[(0,0),(2,0)]'"
		for _, prepared := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, boxError(q, "22P02", "invalid input syntax for type lseg: \"(1,1)\"", "8", "", prepared, false))
		}
	})
}
