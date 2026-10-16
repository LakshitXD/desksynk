//go:build !darwin

package main

import "errors"

func runClient(clientOpts) error {
	return errors.New("the client runs on macOS; run the server on this machine")
}
