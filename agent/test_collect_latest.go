//go:build ignore

package main

import (
	"fmt"
	"github.com/devicepulse/agent/collector"
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
