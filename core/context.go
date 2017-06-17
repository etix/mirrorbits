// Copyright (c) 2014-2017 Ludovic Fauvet
// Licensed under the MIT license

package core

// ContextKey reprensents a context key associated with a value
type ContextKey int

const (
	// ContextAllowRedirects is the key for option: AllowRedirects
	ContextAllowRedirects ContextKey = iota
	// ContextMirrorID is the key for the variable: MirrorID
	ContextMirrorID
)
