// Package pubscoop reconciles one generated Scoop manifest into a protected
// bucket through a reviewable GitHub pull request.
//
// The package owns publication policy and idempotency. Repository reads,
// branch writes, and pull-request creation remain behind narrow ports so the
// state machine is deterministic and testable without network access.
package pubscoop
