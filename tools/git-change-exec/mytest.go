package main

import "fmt"

func testcomments() {
	fmt.Printf("not a comment line") // TODO

	fmt.Printf(
		"not a comment line") // TODO

	fmt.Printf( // TODO 1
		"not a comment line") // TODO 2

	// full comment line

	/*
	 *
	 *
	 *
	 * multi line comment
	 *
	 *
	 *
	 *
	 */
}
