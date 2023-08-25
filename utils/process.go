package utils

import (
	"os"
	"os/signal"
	"runtime/debug"

	"github.com/sirupsen/logrus"
)

// WaitForCtrlC will block/wait until a control-c is pressed
func WaitForCtrlC() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
}

func HandleSubroutinePanic(identifier string) {
	if err := recover(); err != nil {
		logrus.WithError(err.(error)).Errorf("uncaught panic in %v subroutine: %v, stack: %v", identifier, err, string(debug.Stack()))
	}
}
