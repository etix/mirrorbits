// Copyright (c) 2014-2019 Ludovic Fauvet
// Licensed under the MIT license

package filesystem

import (
	"errors"
	"path/filepath"
	"strings"
)

var (
	// ErrOutsideRepo is returned when the target file is outside of the repository
	ErrOutsideRepo = errors.New("target file outside repository")
)

// EvaluateFilePath sanitize and validate the file against the local repository
func EvaluateFilePath(repository, urlpath string) (string, error) {
	fpath := repository + urlpath

	// Get the absolute file path
	fpath, err := filepath.Abs(fpath)
	if err != nil {
		return "", err
	}

	// Check if absolute path is within the repository
	if !IsInRepository(repository, fpath) {
		return "", ErrOutsideRepo
	}

	// Evaluate symlinks
	targetPath, err := filepath.EvalSymlinks(fpath)
	if err != nil {
		return "", err
	}
	if targetPath != fpath {
		targetPath, err = filepath.Abs(targetPath)
		if err != nil {
			return "", err
		}
		if !IsInRepository(repository, targetPath) {
			return "", ErrOutsideRepo
		}
		return targetPath[len(repository):], nil
	}
	return fpath[len(repository):], nil
}

// IsInRepository ensures that the given file path is contained in the repository
func IsInRepository(repository, filePath string) bool {
	if filePath == repository {
		return true
	}
	if strings.HasPrefix(filePath, repository+"/") {
		return true
	}
	return false
}
