// Copyright (c) 2014-2015 Ludovic Fauvet
// Licensed under the MIT license

package network

import (
	"net"
	"strings"
)

// Return the IP address of a mirror and return an error
// if the DNS returns more than one address
func LookupMirrorIP(host string) (string, error) {
	addrs, err := net.LookupIP(host)
	if err != nil {
		return "", err
	}
	// A mirror with multiple IP address is a problem
	// since we can't determine the exact position of
	// the server.
	if len(addrs) > 1 {
		err = ErrMultipleAddresses
	}

	return addrs[0].String(), err
}

// Remove the port from a remote address (x.x.x.x:yyyy)
func RemoteIpFromAddr(remoteAddr string) string {
	return remoteAddr[:strings.LastIndex(remoteAddr, ":")]
}

// Extract the remote IP from an X-Forwarded-For header
func ExtractRemoteIP(XForwardedFor string) string {
	addresses := strings.Split(XForwardedFor, ",")
	if len(addresses) > 0 {
		// The left-most address is supposed to be the original client address.
		// Each successive are added by proxies. In most cases we should probably
		// take the last address but in case of optimization services this will
		// probably not work. For now we'll always take the original one.
		return strings.TrimSpace(addresses[0])
	}
	return ""
}
