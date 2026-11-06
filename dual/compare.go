package dual

import (
	"fmt"
	"slices"
	"strconv"
)

// compareRows compares the sets native consumed with the reference's sets and
// reports whether some set matched only as a multiset. Sets native never
// reached are summarized but not compared.
func compareRows(d *diffLines, o *Options, query string, n, r *rowsOutcome) bool {
	if n.err == nil && r.err == nil && (n.complete || len(n.sets) > len(r.sets)) && len(n.sets) != len(r.sets) {
		d.add(fmt.Sprintf("sets: stuntgres %d, reference %d", len(n.sets), len(r.sets)))
	}
	ordered := o.Order == OrderAlways || (o.Order == OrderByClause && hasTopLevelOrderBy(query))
	orderOnly := false
	for s := range min(len(n.sets), len(r.sets)) {
		if compareSet(d, o, s, n.sets[s], r.sets[s], ordered) {
			orderOnly = true
		}
	}
	return orderOnly
}

// compareSet adds the column, type and row lines of set s. Rows are compared
// only when both sides have the same number of columns.
func compareSet(d *diffLines, o *Options, s int, n, r rawSet, ordered bool) bool {
	if !slices.Equal(n.columns, r.columns) {
		d.add(fmt.Sprintf("#%d columns: stuntgres %s, reference %s", s, compactJSON(nonNil(n.columns)), compactJSON(nonNil(r.columns))))
	}
	for k := range min(len(n.types), len(r.types)) {
		if n.types[k] != "" && r.types[k] != "" && n.types[k] != r.types[k] {
			d.add(fmt.Sprintf("#%d types[%d]: stuntgres %s, reference %s", s, k, quoteJSON(n.types[k]), quoteJSON(r.types[k])))
		}
	}
	if len(n.columns) != len(r.columns) {
		return false
	}
	native := canonSet{columns: n.columns, rows: canonicalRows(o, false, n, n, r), partial: n.partial}
	ref := canonSet{columns: n.columns, rows: canonicalRows(o, true, r, n, r)}
	return classifySet(d, s, native, ref, ordered)
}

// canonicalRows normalizes set's values. The hook sees the native column name
// and the native type name, else the reference's.
func canonicalRows(o *Options, ref bool, set, n, r rawSet) [][]*string {
	rows := make([][]*string, len(set.rows))
	for i, row := range set.rows {
		values := make([]*string, len(row))
		for k, v := range row {
			typ := at(n.types, k)
			if typ == "" {
				typ = at(r.types, k)
			}
			values[k] = normalize(o, ref, at(n.columns, k), typ, v)
		}
		rows[i] = values
	}
	return rows
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

// compareExec compares RowsAffected and LastInsertId availability, and their
// values when both are available.
func compareExec(d *diffLines, n, r execSummary) {
	if !sameCount(n.RowsAffected, r.RowsAffected) {
		d.add("rows_affected: stuntgres " + count(n.RowsAffected) + ", reference " + count(r.RowsAffected))
	}
	if !sameCount(n.LastInsertID, r.LastInsertID) {
		d.add("last_insert_id: stuntgres " + count(n.LastInsertID) + ", reference " + count(r.LastInsertID))
	}
}

func sameCount(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func count(n *int64) string {
	if n == nil {
		return "unavailable"
	}
	return strconv.FormatInt(*n, 10)
}

func comparePrepare(d *diffLines, n, r prepSummary) {
	if n.NumInput >= 0 && r.NumInput >= 0 && n.NumInput != r.NumInput {
		d.add(fmt.Sprintf("num_input: stuntgres %d, reference %d", n.NumInput, r.NumInput))
	}
}
