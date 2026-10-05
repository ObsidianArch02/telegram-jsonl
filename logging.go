// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

var appLogger = newAppLogger(os.Stderr)

func newAppLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func logPrint(args ...any) {
	appLogger.Info(fmt.Sprint(args...))
}

func logPrintf(format string, args ...any) {
	appLogger.Info(fmt.Sprintf(format, args...))
}

func logErrorf(format string, args ...any) {
	appLogger.Error(fmt.Sprintf(format, args...))
}
