// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

var appLogger = newAppLogger(os.Stderr)

func newAppLogger(w io.Writer) *slog.Logger {
	if colorEnabled(w) {
		w = ansiLevelWriter{dst: w}
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func colorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

type ansiLevelWriter struct {
	dst io.Writer
}

func (w ansiLevelWriter) Write(p []byte) (int, error) {
	line := string(p)
	level, color := "", ""
	switch {
	case strings.Contains(line, "level=ERROR"):
		level, color = "level=ERROR", "\x1b[31m"
	case strings.Contains(line, "level=WARN"):
		level, color = "level=WARN", "\x1b[33m"
	case strings.Contains(line, "level=INFO"):
		level, color = "level=INFO", "\x1b[36m"
	case strings.Contains(line, "level=DEBUG"):
		level, color = "level=DEBUG", "\x1b[2m"
	}
	if level == "" {
		return w.dst.Write(p)
	}
	colored := bytes.Replace([]byte(line), []byte(level), []byte(color+level+"\x1b[0m"), 1)
	if _, err := w.dst.Write(colored); err != nil {
		return 0, err
	}
	return len(p), nil
}

func logPrint(args ...any) {
	appLogger.Info(fmt.Sprint(args...))
}

func logPrintf(format string, args ...any) {
	appLogger.Info(fmt.Sprintf(format, args...))
}

func logWarnf(format string, args ...any) {
	appLogger.Warn(fmt.Sprintf(format, args...))
}

func logErrorf(format string, args ...any) {
	appLogger.Error(fmt.Sprintf(format, args...))
}
