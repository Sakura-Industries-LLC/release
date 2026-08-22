// Package pkgrepo validates native packages and builds deterministic static
// APT, RPM/DNF, and APK repository trees. It owns configuration, allowlisting,
// canonical paths, conflict detection, publication ordering, and cache policy;
// external package tools and metadata signers remain behind consumer-owned
// ports.
package pkgrepo
