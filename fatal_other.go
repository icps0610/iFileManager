//go:build !windows

package main

import "fmt"

func showFatal(msg string) {
	fmt.Println(msg)
}
