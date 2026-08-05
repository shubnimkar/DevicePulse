package main

import (
	"fmt"
	"./agent/collector"
)

func main() {
	b := &collector.BrowserHistory{}
	res, err := b.Collect()
	if err != nil {
		fmt.Println("Collect failed:", err)
		return
	}
	fmt.Println("Success!", res)
}
