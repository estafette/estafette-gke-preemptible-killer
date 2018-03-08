package main

import (
	"math/rand"
	"testing"
)

func TestApplyJitter(t *testing.T) {
	R = rand.New(rand.NewSource(0))

	var output = ApplyJitter(100)
	if output != 99 {
		t.Errorf("ApplyJitter, expected 99 got %d", output)
	}
}
