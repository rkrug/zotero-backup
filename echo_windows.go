package main

import (
	"os"
	"syscall"
)

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	setConsoleMode = kernel32.NewProc("SetConsoleMode")
)

const enableEchoInput = 0x4

// echoOff turns off console echo and returns a function that turns it on again.
func echoOff(in *os.File) (func(), error) {
	h := syscall.Handle(in.Fd())
	var mode uint32
	if err := syscall.GetConsoleMode(h, &mode); err != nil {
		return nil, err
	}
	if r, _, err := setConsoleMode.Call(uintptr(h), uintptr(mode&^enableEchoInput)); r == 0 {
		return nil, err
	}
	return func() { setConsoleMode.Call(uintptr(h), uintptr(mode)) }, nil
}
