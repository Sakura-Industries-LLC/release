// Package puboci plans immutable exact tags and moving channel tags.
//
// [CollectState] reads current registry state through [StateReader].
// [PlanTags] feeds that state to [rel.PlanTags]. The package performs no
// registry writes and does not retry transient failures.
package puboci
