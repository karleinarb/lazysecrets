//go:build !unix

package cachefile

// lock does nothing here: saves can then race, losing lists that load
// again.
func lock(string) (func(), error) { return func() {}, nil }
