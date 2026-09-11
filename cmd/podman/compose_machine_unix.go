//go:build !windows

package main

import (
	"errors"
	"net/url"
)

// easytidy: machine feature removed; compose only works against the local socket.
func getMachineConn(connection string, parsedConnection *url.URL) (string, error) {
	if connection != "" {
		return "", errors.New("machine connections are not supported in this build")
	}
	return parsedConnection.String(), nil
}
