package gaps

import (
	"errors"
	"syscall"
)

// busy reports a Windows error that only means another process (another
// okvault rotating the log, a virus scanner, the indexer) holds the file for
// a moment: a sharing or lock violation, or access denied while a replacing
// rename is in flight.
func busy(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	const (
		errorAccessDenied     = 5
		errorSharingViolation = 32
		errorLockViolation    = 33
	)
	return errno == errorAccessDenied || errno == errorSharingViolation || errno == errorLockViolation
}
