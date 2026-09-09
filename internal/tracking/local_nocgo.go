//go:build !cgo

package tracking

// isCgoEnabled returns true since modernc.org/sqlite is pure Go
func isCgoEnabled() bool {
	return true
}
