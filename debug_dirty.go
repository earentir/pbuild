package main

import (
	"fmt"

	"pbuild/gitmeta"
)

func main() {
	dirty, err := gitmeta.HeuristicDirty(".")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Printf("Dirty: %v\n", dirty)
}
