// Package pkgmeta reads normalized metadata from native DEB, RPM, and APK
// package files. DEB and RPM queries run through fixed networkless container
// invocations; APK control metadata is parsed directly from the package.
package pkgmeta
