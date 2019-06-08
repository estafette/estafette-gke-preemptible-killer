package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/go-intervals/timespanset"
	"github.com/rs/zerolog/log"
)

const (
	// whitelistTimePrefix in `YYYY-MM-DDT` format, can be anthing
	whitelistTimePrefix = "2000-01-01T"

	// whitelistTimePlusOneDayPrefix in `YYYY-MM-DDT` format, has to be whitelistTimePrefix plus one day
	whitelistTimePlusOneDayPrefix = "2000-01-02T"

	// whitelistTimePlusOneDayPrefix in `YYYY-MM-DDT` format, has to be whitelistTimePrefix plus two days
	whitelistTimePlusTwoDaysPrefix = "2000-01-03T"

	// whitelistTimePostfix in `:ssZ` format, can be anything
	whitelistTimePostfix = ":00Z"
)

var (
	whitelistStart             time.Time
	whitelistNextDayStart      time.Time
	whitelistAfterTwoDaysStart time.Time
)

func init() {
	var err error

	// whitelistStart is the start of the day
	whitelistStart, err = time.Parse(time.RFC3339, whitelistTimePrefix+"00:00"+whitelistTimePostfix)
	if err != nil {
		panic("whitelistStart parse error")
	}

	// whitelistNextDayStart is the start of the next day
	whitelistNextDayStart, err = time.Parse(time.RFC3339, whitelistTimePlusOneDayPrefix+"00:00"+whitelistTimePostfix)
	if err != nil {
		panic("whitelistNextDayStart parse error")
	}

	// whitelistAfterTwoDaysStart is the start of the day after the next day
	whitelistAfterTwoDaysStart, err = time.Parse(time.RFC3339, whitelistTimePlusTwoDaysPrefix+"00:00"+whitelistTimePostfix)
	if err != nil {
		panic("whitelistAfterTwoDaysStart parse error")
	}
}

type WhitelistInstance struct {
	// whitelistHours are whitelist periods
	whitelistHours *timespanset.Set

	// whitelistSecondCount is the total number of seconds cumulated from all the whitelist periods combined
	whitelistSecondCount int
}

// initializeWhitelistHours initializes data structures by taking command line arguments into account.
func (this *WhitelistInstance) initialize() {
	this.whitelistHours = timespanset.Empty()
	this.whitelistSecondCount = 0
}

func (this *WhitelistInstance) parseArguments() {
	this.initialize()
	if len(*whitelist) == 0 {
		// If there's no whitelist, than the maximum range has to be allowed so that any blacklist
		// might be subtracted from it.
		this.processHours("00:00 - 23:59, 23:59 - 23:58", "+")
	} else {
		this.processHours(*whitelist, "+")
	}

	this.processHours(*blacklist, "-")
	this.whitelistHours.IntervalsBetween(whitelistStart, whitelistAfterTwoDaysStart, this.updateWhitelistSecondCount)
}

// getExpiryDate calculates the expiry date of a node.
func (this *WhitelistInstance) getExpiryDate(t time.Time, timeToBeAdded time.Duration) (expiryDatetime time.Time) {
	truncatedCreationTime := t.Truncate(24 * time.Hour)
	durationFromStartOfDayUntilCreation := t.Sub(truncatedCreationTime)
	secondsToBeAdded := int(t.Add(timeToBeAdded).Sub(truncatedCreationTime).Seconds())
	whitelistAdjustedDurationToBeAdded := time.Duration(secondsToBeAdded%this.whitelistSecondCount) * time.Second

	firstInterval := true
	var projectedCreation time.Time
	for whitelistAdjustedDurationToBeAdded.Seconds() > 0 {
		this.whitelistHours.IntervalsBetween(whitelistStart, whitelistAfterTwoDaysStart, func(start, end time.Time) bool {
			intervalDuration := end.Sub(start)
			if firstInterval {
				// If the current interval ends before the creation, skip for now.
				projectedCreation = start.Truncate(24 * time.Hour).Add(durationFromStartOfDayUntilCreation)
				if projectedCreation.After(end) {
					// And adjust duration.
					durationFromStartOfDayUntilCreation = durationFromStartOfDayUntilCreation - intervalDuration
					whitelistAdjustedDurationToBeAdded = whitelistAdjustedDurationToBeAdded - intervalDuration
					return true
				}
			}

			// If creation is in the middle of the current interval, start with the creation.
			if projectedCreation.After(start) {
				whitelistAdjustedDurationToBeAdded = whitelistAdjustedDurationToBeAdded - projectedCreation.Sub(start)
				start = projectedCreation
			}

			// Check if we have reached the wanted time.
			if whitelistAdjustedDurationToBeAdded < intervalDuration {
				expiryDatetime = truncatedCreationTime.Add(start.Add(whitelistAdjustedDurationToBeAdded).Sub(whitelistStart))
			}

			// Subtract from how much we want to add.
			whitelistAdjustedDurationToBeAdded = time.Duration(whitelistAdjustedDurationToBeAdded.Seconds()-intervalDuration.Seconds()) * time.Second
			if whitelistAdjustedDurationToBeAdded.Seconds() < 0 {
				return false
			}

			return true
		})

		// Just a safeguard, this loop should never run more than twice.
		if !firstInterval {
			log.Warn().Msgf("whitelist loop wants to run a third time")
			break
		}

		firstInterval = false
	}
	return expiryDatetime
}

// mergeTimespans merges time intervals.
func (this *WhitelistInstance) mergeTimespans(start time.Time, end time.Time, direction string) {
	if direction == "+" {
		this.whitelistHours.Insert(start, end)
	} else if direction == "-" {
		subtrahend := timespanset.Empty()
		subtrahend.Insert(start, end)
		this.whitelistHours.Sub(subtrahend)
	} else {
		panic(fmt.Sprintf("mergeTimespans(): direction expected to be + or - but got '%v'", direction))
	}
}

// processHours parses time stamps and passes them to mergeTimespans(), direction can be "+" or "-".
func (this *WhitelistInstance) processHours(input string, direction string) {
	// Time not specified, continue with no restrictions.
	if len(input) == 0 {
		return
	}

	// Split in intervals.
	intervals := strings.Split(input, ", ")
	for _, timeInterval := range intervals {
		times := strings.Split(timeInterval, " - ")

		// Check format.
		if len(times) != 2 {
			panic(fmt.Sprintf("processHours(): interval '%v' should be of the form `09:00 - 12:00`", timeInterval))
		}

		// Start time
		start, err := time.Parse(time.RFC3339, whitelistTimePrefix+times[0]+whitelistTimePostfix)
		if err != nil {
			panic(fmt.Sprintf("processHours(): %v cannot be parsed: %v", times[0], err))
		}

		// End time
		end, err := time.Parse(time.RFC3339, whitelistTimePrefix+times[1]+whitelistTimePostfix)
		if err != nil {
			panic(fmt.Sprintf("processHours(): %v cannot be parsed: %v", times[1], err))
		}

		// If start is after end it means it contains midnight, so split in two.
		if start.After(end) {
			nextDayStart := whitelistNextDayStart

			nextDayEnd, err := time.Parse(time.RFC3339, whitelistTimePlusOneDayPrefix+times[1]+whitelistTimePostfix)
			if err != nil {
				panic(fmt.Sprintf("processHours(): %v cannot be parsed: %v", times[1], err))
			}

			this.mergeTimespans(nextDayStart, nextDayEnd, direction)
			end = whitelistNextDayStart
		}

		// Merge timespans.
		this.mergeTimespans(start, end, direction)
	}
}

// updateWhitelistSecondCount adds the difference between two times to an accumulator.
func (this *WhitelistInstance) updateWhitelistSecondCount(start, end time.Time) bool {
	if start.After(end) {
		panic(fmt.Sprintf("updateWhitelistSecondCount(): go-intervals timespanset is acting up providing reverse intervals: %v - %v", start, end))
	}
	this.whitelistSecondCount += int(end.Sub(start).Seconds())
	return true
}
