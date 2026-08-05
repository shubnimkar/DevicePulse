package collector

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func TestBrowsers() {
	homeDir, _ := os.UserHomeDir()
	
	fmt.Println("Safari:")
	safariPath := homeDir + "/Library/Safari/History.db"
	
	tempFile, err := os.CreateTemp("", "safari_history_*.db")
	if err != nil {
		fmt.Println("Error temp:", err)
		return
	}
	tempPath := tempFile.Name()
	tempFile.Close()
	defer os.Remove(tempPath)
	
	err = copyFile(safariPath, tempPath)
	if err != nil {
		fmt.Println("Error copy:", err)
		return
	}
	
	db, err := sql.Open("sqlite", tempPath)
	if err != nil {
		fmt.Println("Error open db:", err)
		return
	}
	defer db.Close()
	
	query := `
		SELECT i.url, v.title, v.visit_time
		FROM history_items i
		JOIN history_visits v ON i.id = v.history_item
		ORDER BY v.visit_time DESC
		LIMIT 1
	`
	rows, err := db.Query(query)
	if err != nil {
		fmt.Println("Error query:", err)
		return
	}
	defer rows.Close()
	
	for rows.Next() {
		var url, title string
		var visitTime float64
		rows.Scan(&url, &title, &visitTime)
		fmt.Println("Row:", url, title, visitTime)
	}
	fmt.Println("Done")
}
