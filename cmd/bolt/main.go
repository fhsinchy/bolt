package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/fhsinchy/bolt/internal/config"
	"github.com/fhsinchy/bolt/internal/daemon"
)

var version = "dev"

func main() {
	args := os.Args[1:]

	if len(args) == 0 {
		startDaemon()
		return
	}

	switch args[0] {
	case "start":
		startDaemon()
	case "version", "--version", "-v":
		fmt.Printf("bolt %s\n", version)
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", args[0])
		printUsage()
		os.Exit(1)
	}
}

func startDaemon() {
	d, err := daemon.New(config.DefaultPath(), version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	if err := d.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`bolt - fast segmented download manager (daemon)

Usage:
  bolt                  Start the daemon
  bolt start            Start the daemon
  bolt version          Show version
  bolt help             Show this help
`)
}
