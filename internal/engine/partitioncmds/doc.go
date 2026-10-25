// Package partitioncmds holds the partition and inheritance statement
// executors (ALTER TABLE ATTACH PARTITION and DETACH PARTITION) and the
// partition and inheritance Restore repairs; the root composition dispatches
// to them and calls them from its Restore hooks.
package partitioncmds
