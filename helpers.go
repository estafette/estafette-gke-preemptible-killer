package main

import (
	"math/rand"
	"time"
)

// seed random number
var R = rand.New(rand.NewSource(time.Now().UnixNano()))

// ApplyJitter return a random number
func ApplyJitter(input int) (output int) {
	deviation := int(0.25 * float64(input))
	return input - deviation + R.Intn(2*deviation)
}
