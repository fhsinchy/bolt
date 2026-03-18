package main

import (
	"fmt"
	"os"
	"sync"
)

var version = "dev"

func main() {
	args := os.Args[1:]

	if len(args) > 0 {
		switch args[0] {
		case "version", "--version", "-v":
			fmt.Printf("bolt-host %s\n", version)
			return
		case "help", "--help", "-h":
			fmt.Print("bolt-host - Chrome native messaging bridge for Bolt\n\nThis binary is spawned by Chrome. Do not run it directly.\n")
			return
		}
	}

	sock := socketPath()
	r := newRelay(sock)

	var mu sync.Mutex
	h := &host{
		relay:  r,
		stdout: os.Stdout,
		mu:     &mu,
	}
	h.run(os.Stdin)
}
