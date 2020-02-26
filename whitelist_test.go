package main

import (
	"fmt"
	"testing"
	"time"
)

// Test data
var (
	a time.Time
	b time.Time
	c time.Time
)

func init() {
	var err error
	s := "2000-01-02T03:04:00Z"
	a, err = time.Parse(time.RFC3339, s)
	if err != nil {
		panic(fmt.Sprintf("%v cannot be parsed: %v", s, err))
	}
	s = "2000-01-02T04:04:00Z"
	b, err = time.Parse(time.RFC3339, s)
	if err != nil {
		panic(fmt.Sprintf("%v cannot be parsed: %v", s, err))
	}
	s = "2000-05-06T07:08:00Z"
	c, err = time.Parse(time.RFC3339, s)
	if err != nil {
		panic(fmt.Sprintf("%v cannot be parsed: %v", s, err))
	}
}

func TestMergeTimespans(t *testing.T) {
	var i WhitelistInstance
	i.initialize()

	// Check that basic scenario works.
	i.mergeTimespans(a, b, "+")
	i.whitelistHours.IntervalsBetween(a, c, i.updateWhitelistSecondCount)
	if i.whitelistSecondCount != 3600 {
		t.Errorf("Expected 3600 seconds, got '%v'", i.whitelistSecondCount)
	}
	i.whitelistSecondCount = 0

	// Check that merging a multi-day interval works.
	i.mergeTimespans(b, c, "+")
	i.whitelistHours.IntervalsBetween(a, c, i.updateWhitelistSecondCount)
	if i.whitelistSecondCount != 10814640 {
		t.Errorf("Expected 10814640 seconds, got '%v'", i.whitelistSecondCount)
	}
	i.whitelistSecondCount = 0

	// Check that merging a zero interval with + doesn't change second count.
	i.mergeTimespans(a, a, "+")
	i.whitelistHours.IntervalsBetween(a, c, i.updateWhitelistSecondCount)
	if i.whitelistSecondCount != 10814640 {
		t.Errorf("Expected 10814640 seconds, got '%v'", i.whitelistSecondCount)
	}
	i.whitelistSecondCount = 0

	// Check that merging a zero interval with - doesn't change second count.
	i.mergeTimespans(a, a, "-")
	i.whitelistHours.IntervalsBetween(a, c, i.updateWhitelistSecondCount)
	if i.whitelistSecondCount != 10814640 {
		t.Errorf("Expected 10814640 seconds, got '%v'", i.whitelistSecondCount)
	}
	i.whitelistSecondCount = 0

	// Check that merging an interval with - works.
	i.mergeTimespans(b, c, "-")
	i.whitelistHours.IntervalsBetween(a, c, i.updateWhitelistSecondCount)
	if i.whitelistSecondCount != 3600 {
		t.Errorf("Expected 3600 seconds, got '%v'", i.whitelistSecondCount)
	}
	i.whitelistSecondCount = 0

	// Check that merging with a wrong direction panics.
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic on mergeTimespans(*)")
		}
	}()
	i.mergeTimespans(a, b, "*")
}

func TestProcessHours(t *testing.T) {
	var i WhitelistInstance
	i.initialize()

	// Check that argument parsing works.
	i.whitelist = "00:00 - 04:00, 08:00 - 12:00, 16:00 - 20:00"
	i.blacklist = "01:00 - 02:00, 06:00 - 14:00, 15:00 - 17:00"
	i.parseArguments()
	if i.whitelistSecondCount != 21600 {
		t.Errorf("Expected 21600 seconds, got '%v'", i.whitelistSecondCount)
	}
}
