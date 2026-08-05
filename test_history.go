package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Println("could not get home dir:", err)
		return
	}

	chromeBase := filepath.Join(homeDir, "Library", "Application Support", "Google", "Chrome")
	var chromeHistoryPath string

	dirEntries, err := os.ReadDir(chromeBase)
	if err != nil {
		fmt.Println("could not read dir:", err)
		return
	}
	
	fmt.Println("Found", len(dirEntries), "entries in", chromeBase)
	
	for _, e := range dirEntries {
		if e.IsDir() && (e.Name() == "Default" || strings.HasPrefix(e.Name(), "Profile ")) {
			potentialPath := filepath.Join(chromeBase, e.Name(), "History")
			fmt.Println("Checking:", potentialPath)
			if _, err := os.Stat(potentialPath); err == nil {
				chromeHistoryPath = potentialPath
				fmt.Println("Found a valid history file!", chromeHistoryPath)
				break // Found a valid history file
			} else {
				fmt.Println("Error stating path:", err)
			}
		}
	}
}
