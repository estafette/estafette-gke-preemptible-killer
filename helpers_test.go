package main

import (
	"math/rand"
	"testing"
)

func TestApplyJitter(t *testing.T) {
	R = rand.New(rand.NewSource(0))

	var output = ApplyJitter(100)
	if output != 106 {
		t.Errorf("ApplyJitter, expected 10 got %d", output)
	}
}
