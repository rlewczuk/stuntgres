// Package analyze holds statement binding: the bound form of a statement
// (Binding), its per-kind payloads and the capture groups that views, rules,
// policies and prepared statements read. The expression binder that produces
// them still lives in the engine and moves here in a later step
// (docs/ENGINE-SPLIT-analyze.md, 12d).
package analyze
