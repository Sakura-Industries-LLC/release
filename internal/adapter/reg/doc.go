// Package reg implements [puboci.StateReader] with oras-go.
//
// [New] builds a read-only registry client. Token text is applied only when
// building a per-request authenticated transport and is never stored in a
// formattable field or included in returned errors. Resolve and Version
// classify registry failures as absent, auth, retryable, or corrupt.
package reg
