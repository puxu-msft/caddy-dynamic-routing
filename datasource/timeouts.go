package datasource

import "time"

// DefaultInitialLoadTimeout is the default maximum time allowed for a data source
// to perform its initial full refresh during Provision.
//
// This is intentionally conservative: initial load should either complete quickly
// or fail fast and allow the module's background watchers/pollers to retry.
const DefaultInitialLoadTimeout = 30 * time.Second
