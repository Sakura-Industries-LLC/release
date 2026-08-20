// Package execx runs external programs with the release CLI's shared process
// policy.
//
// Run resolves one executable, starts it without a shell, forwards configured
// output streams, retains a bounded stderr tail, and bounds waits on leaked
// child I/O. Tool adapters remain responsible for argument construction,
// domain validation, secret handling, output parsing, and error presentation.
package execx
