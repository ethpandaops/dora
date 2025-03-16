package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: dora-utils <command> [arguments]")
		fmt.Println("\nCommands:")
		fmt.Println("  blockdb-sync   Sync blocks from beacon node to blockdb")
		fmt.Println("  migrate        Migrate database between different engines")
		os.Exit(1)
	}

	command := os.Args[1]
	os.Args = append([]string{os.Args[0]}, os.Args[2:]...)

	switch command {
	case "blockdb-sync":
		blockdbSync()
	case "migrate":
		migrate()
	default:
		fmt.Printf("Unknown command: %s\n", command)
		os.Exit(1)
	}
}
