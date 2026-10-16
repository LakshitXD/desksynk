//go:build !linux

package main

import "errors"

func runServer(serverOpts) error {
	return errors.New("the server runs on Linux (Hyprland); run the client on this machine")
}
