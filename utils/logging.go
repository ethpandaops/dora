package utils

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	logger "github.com/sirupsen/logrus"
)

// LogFatal logs a fatal error with callstack info that skips callerSkip many levels with arbitrarily many additional infos.
// callerSkip equal to 0 gives you info directly where LogFatal is called.
func LogFatal(err error, errorMsg interface{}, callerSkip int, additionalInfos ...map[string]interface{}) {
	logErrorInfo(err, callerSkip, additionalInfos...).Fatal(errorMsg)
}

// LogError logs an error with callstack info that skips callerSkip many levels with arbitrarily many additional infos.
// callerSkip equal to 0 gives you info directly where LogError is called.
func LogError(err error, errorMsg interface{}, callerSkip int, additionalInfos ...map[string]interface{}) {
	logErrorInfo(err, callerSkip, additionalInfos...).Error(errorMsg)
}

func logErrorInfo(err error, callerSkip int, additionalInfos ...map[string]interface{}) *logger.Entry {
	logFields := logger.NewEntry(logger.New())

	pc, fullFilePath, line, ok := runtime.Caller(callerSkip + 2)
	if ok {
		logFields = logFields.WithFields(logger.Fields{
			"_file":     filepath.Base(fullFilePath),
			"_function": runtime.FuncForPC(pc).Name(),
			"_line":     line,
		})
	} else {
		logFields = logFields.WithField("runtime", "Callstack cannot be read")
	}

	errColl := []string{}
	for {
		errColl = append(errColl, fmt.Sprint(err))
		nextErr := errors.Unwrap(err)
		if nextErr != nil {
			err = nextErr
		} else {
			break
		}
	}

	errMarkSign := "~"
	for idx := 0; idx < (len(errColl) - 1); idx++ {
		errInfoText := fmt.Sprintf("%serrInfo_%v%s", errMarkSign, idx, errMarkSign)
		nextErrInfoText := fmt.Sprintf("%serrInfo_%v%s", errMarkSign, idx+1, errMarkSign)
		if idx == (len(errColl) - 2) {
			nextErrInfoText = fmt.Sprintf("%serror%s", errMarkSign, errMarkSign)
		}

		// Replace the last occurrence of the next error in the current error
		lastIdx := strings.LastIndex(errColl[idx], errColl[idx+1])
		if lastIdx != -1 {
			errColl[idx] = errColl[idx][:lastIdx] + nextErrInfoText + errColl[idx][lastIdx+len(errColl[idx+1]):]
		}

		errInfoText = strings.ReplaceAll(errInfoText, errMarkSign, "")
		logFields = logFields.WithField(errInfoText, errColl[idx])
	}

	if err != nil {
		logFields = logFields.WithField("errType", fmt.Sprintf("%T", err)).WithError(err)
	}

	for _, infoMap := range additionalInfos {
		for name, info := range infoMap {
			logFields = logFields.WithField(name, info)
		}
	}

	return logFields
}
