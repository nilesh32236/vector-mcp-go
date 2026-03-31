package main

import "fmt"

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a + b
}

// main is the program entry point. It calls Add(2, 3) and prints the resulting sum to standard output.
func main() {
	fmt.Println(Add(2, 3))
}
