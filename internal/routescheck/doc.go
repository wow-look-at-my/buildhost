// Package routescheck contains no production code. It exists only as a home
// for a test that imports every backend and asserts their routes register at
// package-init time (without auth.Init). It has zero coverable statements, so
// it never affects the coverage total -- unlike a test placed in cmd/buildhost,
// which would pull that otherwise-untested CLI package into the measurement.
package routescheck
