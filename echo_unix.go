//go:build !windows

package main

import (
	"os"
	"os/exec"
)

// echoOff turns off terminal echo with stty and returns a function that turns it on again.
func echoOff(in *os.File) (func(), error) {
	off := exec.Command("stty", "-echo")
	off.Stdin = in
	if err := off.Run(); err != nil {
		return nil, err
	}
	return func() {
		on := exec.Command("stty", "echo")
		on.Stdin = in
		on.Run()
	}, nil
}
