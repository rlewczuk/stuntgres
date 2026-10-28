package sqltype

import (
 "context"
 "strings"
)

type labelSpan struct { start, end int }
type labelPattern struct { start, end int; flags byte }
type lqueryLevel struct { low, high, first, end int; wildcard, negated bool }

// Scratch belongs to one invocation and is reused across paths and queries.
type ltreeMatchScratch struct { previous, next []bool; prefix []int; boolean []bool }

func ltreeAncestor(parent, child *ltreePayload) bool {
 return len(parent.text) == 0 || strings.HasPrefix(child.text, parent.text) && (len(parent.text) == len(child.text) || child.text[len(parent.text)] == '.')
}

func labelEqual(ctx context.Context, label, query string, flags byte) (bool, error) {
 if flags&4 == 0 && len(label) != len(query) || len(label) < len(query) { return false, nil }
 for i := range len(query) {
  if i&255 == 0 { if err := ctx.Err(); err != nil { return false, err } }
  a, b := label[i], query[i]
  if flags&2 != 0 { if a >= 'A' && a <= 'Z' { a += 'a'-'A' }; if b >= 'A' && b <= 'Z' { b += 'a'-'A' } }
  if a != b { return false, nil }
 }
 return true, nil
}

func labelMatch(ctx context.Context, label, query string, flags byte) (bool, error) {
 if flags&1 == 0 { return labelEqual(ctx, label, query, flags) }
 for q := 0; q < len(query); {
  if err := ctx.Err(); err != nil { return false, err }
  if query[q] == '_' { q++; continue }
  end := q
  for end < len(query) && query[end] != '_' { end++ }
  found := false
  for l := 0; l < len(label); {
   if err := ctx.Err(); err != nil { return false, err }
   if label[l] == '_' { l++; continue }
   stop := l
   for stop < len(label) && label[stop] != '_' { stop++ }
   matched, err := labelEqual(ctx, label[l:stop], query[q:end], flags)
   if err != nil { return false, err }; if matched { found = true; break }
   l = stop
  }
  if !found { return false, nil }; q = end
 }
 return true, nil
}

func (q *lqueryPayload) matchesLevel(ctx context.Context, level lqueryLevel, label string) (bool, error) {
 if level.wildcard { return true, nil }
 matched := false
 for _, alternative := range q.alternatives[level.first:level.end] {
  if err := ctx.Err(); err != nil { return false, err }
  ok, err := labelMatch(ctx, label, q.text[alternative.start:alternative.end], alternative.flags)
  if err != nil { return false, err }; if ok { matched = true; break }
 }
 return matched != level.negated, nil
}

// Each level transitions over contiguous matching-label runs. Prefix counts of
// reachable starts make bounded repetition linear rather than backtracking.
func (s *ltreeMatchScratch) query(ctx context.Context, path *ltreePayload, query *lqueryPayload) (bool, error) {
 if len(query.levels) == 1 {
  level := query.levels[0]
  if len(path.labels) < level.low || len(path.labels) > level.high { return false, nil }
  if level.wildcard { return true, nil }
  for _, label := range path.labels {
   if err := ctx.Err(); err != nil { return false, err }
   matched, err := query.matchesLevel(ctx, level, path.text[label.start:label.end])
   if err != nil || !matched { return false, err }
  }
  return true, nil
 }
 size := len(path.labels)+1
 if cap(s.previous) < size { s.previous = make([]bool, size); s.next = make([]bool, size); s.prefix = make([]int, size+1) } else { s.previous = s.previous[:size]; s.next = s.next[:size]; s.prefix = s.prefix[:size+1]; clear(s.previous) }
 s.previous[0] = true
 for _, level := range query.levels {
  if err := ctx.Err(); err != nil { return false, err }
  s.prefix[0] = 0
  for i, reachable := range s.previous { s.prefix[i+1] = s.prefix[i]; if reachable { s.prefix[i+1]++ } }
  run := 0
  anyReachable := false
  for end := range size {
   if err := ctx.Err(); err != nil { return false, err }
   if end > 0 {
    label := path.labels[end-1]
    ok, err := query.matchesLevel(ctx, level, path.text[label.start:label.end])
    if err != nil { return false, err }; if !ok { run = end }
   }
   low, high := max(end-level.high, run), end-level.low
   s.next[end] = high >= low && high >= 0 && s.prefix[high+1] > s.prefix[low]
   anyReachable = anyReachable || s.next[end]
  }
  if !anyReachable { return false, nil }
  s.previous, s.next = s.next, s.previous
 }
 return s.previous[size-1], nil
}

func (s *ltreeMatchScratch) textQuery(ctx context.Context, path *ltreePayload, query *ltxtqueryPayload) (bool, error) {
 if cap(s.boolean) < len(query.nodes) { s.boolean = make([]bool, len(query.nodes)) } else { s.boolean = s.boolean[:len(query.nodes)] }
 for i, node := range query.nodes {
  if err := ctx.Err(); err != nil { return false, err }
  switch node.op {
  case '!': s.boolean[i] = !s.boolean[node.right]
  case '&': s.boolean[i] = s.boolean[node.left] && s.boolean[node.right]
  case '|': s.boolean[i] = s.boolean[node.left] || s.boolean[node.right]
  default:
   matched := false
   for _, label := range path.labels {
    ok, err := labelMatch(ctx, path.text[label.start:label.end], query.text[node.start:node.end], node.flags)
    if err != nil { return false, err }; if ok { matched = true; break }
   }
   s.boolean[i] = matched
  }
 }
 return s.boolean[len(query.nodes)-1], nil
}
