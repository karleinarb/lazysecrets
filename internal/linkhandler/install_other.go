//go:build !darwin && !linux

package linkhandler

import (
	"errors"
	"runtime"
)

var errUnsupported = errors.New("registering lazysecrets:// links isn't supported on " + runtime.GOOS + " yet")

func AppPath() (string, error)                              { return "", errUnsupported }
func Install(launch []string, env []string) (string, error) { return "", errUnsupported }
func Check() string                                         { return "" } // nothing to set up
func Uninstall() (string, error)                            { return "", errUnsupported }
