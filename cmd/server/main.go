package main

import (
	"log/slog"
	"os"

	"money-manager-server/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		slog.Error("server stopped with an error", "error", err)
		os.Exit(1)
	}
}
