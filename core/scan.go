package core

import "time"

// ScannerType holds the type of scanner in use
type ScannerType int8

const (
	// RSYNC represents an rsync scanner
	RSYNC ScannerType = iota
	// FTP represents an ftp scanner
	FTP
)

// Precision is used to compute the precision of the mod time (millisecond, second)
type Precision time.Duration

func (p Precision) Duration() time.Duration {
	return time.Duration(p)
}
