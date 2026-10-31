package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"example.com/isureal-company/isureal"
	"github.com/labstack/echo/v5"
)

// 2026-09-23

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	config := isureal.ConfigFromEnv()
	server, closeDB := isureal.NewServer(ctx, config)
	startErr := (echo.StartConfig{Address: ":8080"}).Start(ctx, server)
	if err := closeDB(); err != nil {
		panic("failed to close database: " + err.Error())
	}
	if startErr != nil {
		panic("failed to start server: " + startErr.Error())
	}
}
