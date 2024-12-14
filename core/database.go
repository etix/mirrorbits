// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package core

const (
	// RedisMinimumVersion contains the minimum redis version required to run the application
	RedisMinimumVersion = "3.2.0"
	// DBVersion represents the current DB format version
	DBVersion = 2
	// DBVersionKey contains the global redis key containing the DB version format
	DBVersionKey = "MIRRORBITS_DB_VERSION"
)
