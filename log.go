package cachePool

import (
	"fmt"
)

func debug(format string, a ...interface{}) (n int, err error) {
	return fmt.Printf(format, a...)
}
func info(format string, a ...interface{}) (n int, err error) {
	return fmt.Printf(format, a...)
}
func notice(format string, a ...interface{}) (n int, err error) {
	return fmt.Printf(format, a...)
}

func warn(format string, a ...interface{}) (n int, err error) {
	return fmt.Printf(format, a...)
}
