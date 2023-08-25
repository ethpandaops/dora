package utils

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	logger "github.com/sirupsen/logrus"
)

type LogWriter struct {
	logFile *os.File
}

func InitLogger() *LogWriter {
	logger.SetOutput(io.Discard) // Send all logs to nowhere by default
	logger.SetLevel(logger.TraceLevel)
	logWriter := &LogWriter{}

	outputLevel := getLogLevels(logger.InfoLevel)
	if Config.Logging.OutputLevel != "" {
		levelParts := strings.Split(Config.Logging.OutputLevel, "|")
		if len(levelParts) > 1 {
			outputLevel = []logger.Level{}
			for _, level := range levelParts {
				logLevel := parseLogLevel(level)
				if logLevel != 9999 {
					outputLevel = append(outputLevel, logLevel)
				}
			}
		} else {
			logLevel := parseLogLevel(levelParts[0])
			if logLevel != 9999 {
				outputLevel = getLogLevels(logLevel)
			} else {
				outputLevel = []logger.Level{}
			}
		}
	}
	if len(outputLevel) > 0 {
		var writer io.Writer
		if Config.Logging.OutputStderr {
			writer = os.Stderr
		} else {
			writer = os.Stdout
		}
		logger.AddHook(&LogWriterHook{
			Writer:    writer,
			LogLevels: outputLevel,
		})
	}

	if Config.Logging.FilePath != "" {
		fileLevel := getLogLevels(logger.InfoLevel)
		if Config.Logging.FileLevel != "" {
			levelParts := strings.Split(Config.Logging.FileLevel, "|")
			if len(levelParts) > 1 {
				fileLevel = []logger.Level{}
				for _, level := range levelParts {
					logLevel := parseLogLevel(level)
					if logLevel != 9999 {
						fileLevel = append(fileLevel, logLevel)
					}
				}
			} else {
				logLevel := parseLogLevel(levelParts[0])
				if logLevel != 9999 {
					fileLevel = getLogLevels(logLevel)
				} else {
					fileLevel = []logger.Level{}
				}
			}
		}

		fmt.Printf("logging to file: %v (%v)\n", Config.Logging.FilePath, fileLevel)
		f, err := os.OpenFile(Config.Logging.FilePath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			fmt.Println("Failed to create logfile" + Config.Logging.FilePath)
			panic(err)
		}
		logWriter.logFile = f
		logger.AddHook(&LogWriterHook{ // Send info and debug logs to stdout
			Writer:    f,
			LogLevels: fileLevel,
		})
	}

	return logWriter
}

func (logWriter *LogWriter) Dispose() {
	if logWriter.logFile != nil {
		logWriter.logFile.Close()
		logWriter.logFile = nil
	}
}

func getLogLevels(level logger.Level) []logger.Level {
	if level == logger.TraceLevel {
		return []logger.Level{
			logger.PanicLevel,
			logger.FatalLevel,
			logger.ErrorLevel,
			logger.WarnLevel,
			logger.InfoLevel,
			logger.DebugLevel,
			logger.TraceLevel,
		}
	} else if level == logger.DebugLevel {
		return []logger.Level{
			logger.PanicLevel,
			logger.FatalLevel,
			logger.ErrorLevel,
			logger.WarnLevel,
			logger.InfoLevel,
			logger.DebugLevel,
		}
	} else if level == logger.InfoLevel {
		return []logger.Level{
			logger.PanicLevel,
			logger.FatalLevel,
			logger.ErrorLevel,
			logger.WarnLevel,
			logger.InfoLevel,
		}
	} else if level == logger.WarnLevel {
		return []logger.Level{
			logger.PanicLevel,
			logger.FatalLevel,
			logger.ErrorLevel,
			logger.WarnLevel,
		}
	} else if level == logger.ErrorLevel {
		return []logger.Level{
			logger.PanicLevel,
			logger.FatalLevel,
			logger.ErrorLevel,
		}
	} else if level == logger.FatalLevel {
		return []logger.Level{
			logger.PanicLevel,
			logger.FatalLevel,
		}
	} else if level == logger.PanicLevel {
		return []logger.Level{
			logger.PanicLevel,
		}
	} else {
		return []logger.Level{}
	}
}

func parseLogLevel(level string) logger.Level {
	switch level {
	case "trace":
		return logger.TraceLevel
	case "debug":
		return logger.DebugLevel
	case "info":
		return logger.InfoLevel
	case "warn":
		return logger.WarnLevel
	case "error":
		return logger.ErrorLevel
	case "fatal":
		return logger.FatalLevel
	case "panic":
		return logger.PanicLevel
	case "none":
		return 9999
	}
	return 0
}

// WriterHook is a hook that writes logs of specified LogLevels to specified Writer
type LogWriterHook struct {
	Writer    io.Writer
	LogLevels []logger.Level
}

// Fire will be called when some logging function is called with current hook
// It will format log entry to string and write it to appropriate writer
func (hook *LogWriterHook) Fire(entry *logger.Entry) error {
	line, err := entry.String()
	if err != nil {
		return err
	}
	_, err = hook.Writer.Write([]byte(line))
	return err
}

func (hook *LogWriterHook) Levels() []logger.Level {
	return hook.LogLevels
}

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

func GetRedactedUrl(requrl string) string {
	urlData, _ := url.Parse(requrl)
	var logurl string
	if urlData != nil {
		logurl = urlData.Redacted()
	} else {
		logurl = requrl
	}
	return logurl
}
