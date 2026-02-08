package main

import (
	"os"

	"github.com/suyog1pathak/transporter/cmd/transporter/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		os.Exit(1)
	}
}
