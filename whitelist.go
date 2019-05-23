package main

import (
	"os"
	"strings"
	"time"

	"github.com/google/go-intervals/timespanset"
	"github.com/rs/zerolog/log"
)

const (
	// whitelistTimePrefix in `YYYY-MM-DDT` format, can be anthing
	whitelistTimePrefix = "2222-22-22T"

	// whitelistTimePlusOneDayPrefix in `YYYY-MM-DDT` format, has to be whitelistTimePrefix plus one day
	whitelistTimePlusOneDayPrefix = "2222-22-23T"

	// whitelistTimePostfix in `:ssZ` format, can be anything
	whitelistTimePostfix = ":00Z"
)

var (
	// whitelistAbsoluteStart is the start of the day
	whitelistAbsoluteStart, _ = time.Parse(time.RFC3339, whitelistTimePrefix+"00:00"+whitelistTimePostfix)

	// whitelistAbsoluteEnd is the start of the next day
	whitelistAbsoluteEnd, _ = time.Parse(time.RFC3339, whitelistTimePlusOneDayPrefix+"00:00"+whitelistTimePostfix)

	// whitelistHours are whitelist periods
	whitelistHours = timespanset.Empty()

	// whitelistSecondCount is the total number of seconds cumulated from all the whitelist periods combined
	whitelistSecondCount = 0
)

// initializeWhitelistHours initializes data structures by taking command line arguments into account
func initializeWhitelistHours() {
	if len(*whitelist) == 0 {
		// If there's no whitelist, than the maximum range has to be allowed so that any blacklist
		// might be subtracted from it.
		processHours("00:00 - 23:59, 23:59 - 00:00", whitelistHours, "+")
	} else {
		processHours(*whitelist, whitelistHours, "+")
	}

	processHours(*blacklist, whitelistHours, "-")
	whitelistHours.IntervalsBetween(whitelistAbsoluteStart, whitelistAbsoluteEnd, updateWhitelistSecondCount)
	if whitelistSecondCount == 0 {
		// In case of no blacklists & no whitelists.
		whitelistSecondCount = 3600 * 24
	}
}

func getExpiryDate(t time.Time, timeToBeAdded time.Duration) (expiryDatetime time.Time) {
	truncatedCreationTime := t.Truncate(24 * time.Hour)
	durationFromStartOfDayUntilCreation := t.Sub(truncatedCreationTime)
	whitelistAdjustedSecondsToBeAdded := time.Duration(int((durationFromStartOfDayUntilCreation.Seconds()+timeToBeAdded.Seconds()))%whitelistSecondCount) * time.Second

	secondTime := false
	for whitelistAdjustedSecondsToBeAdded.Seconds() > 0 {
		whitelistHours.IntervalsBetween(whitelistAbsoluteStart, whitelistAbsoluteEnd, func(start, end time.Time) bool {
			// If the current interval ends before the creation, skip for now.
			projectedCreation := start.Truncate(24 * time.Hour).Add(durationFromStartOfDayUntilCreation)
			if projectedCreation.After(end) {
				return true
			}

			// If creation is in the middle of the current interval, start with the creation.
			if projectedCreation.After(start) {
				start = projectedCreation
			}

			// Check if we have reached the wanted time.
			intervalPeriod := end.Sub(start)
			if whitelistAdjustedSecondsToBeAdded.Seconds() < intervalPeriod.Seconds() {
				expiryDatetime = truncatedCreationTime.Add(start.Add(whitelistAdjustedSecondsToBeAdded).Sub(whitelistAbsoluteStart))
			}

			// Subtract from how much we want to add.
			whitelistAdjustedSecondsToBeAdded = time.Duration(whitelistAdjustedSecondsToBeAdded.Seconds()-intervalPeriod.Seconds()) * time.Second
			if whitelistAdjustedSecondsToBeAdded.Seconds() < 0 {
				return false
			}

			return true
		})

		// Just a safeguard, this loop should never run more than twice.
		if secondTime {
			log.Warn().Msgf("whitelist loop wants to run a third time")
			break
		}

		secondTime = true
	}
	return expiryDatetime
}

// mergeTimespans merges time intervals
func mergeTimespans(start time.Time, end time.Time, direction string) {
	if direction == "+" {
		whitelistHours.Insert(start, end)
	} else if direction == "-" {
		subtrahend := timespanset.Empty()
		subtrahend.Insert(start, end)
		whitelistHours.Sub(subtrahend)
	} else {
		log.Error().Msgf("processHours(): direction can only be + or -")
		os.Exit(4)
	}
}

// processHours parses time stamps and passed them to mergeTimespans(), direction can be "+" or "-"
func processHours(input string, output *timespanset.Set, direction string) {
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
			log.Error().Msgf("processHours(): interval %v should be of the form `09:00 - 12:00`", timeInterval)
			os.Exit(1)
		}

		// Start time
		start, err := time.Parse(time.RFC3339, whitelistTimePrefix+times[0]+whitelistTimePostfix)
		if err != nil {
			log.Error().Msgf("processHours(): %v cannot be parsed: %v", times[0], err)
			os.Exit(2)
		}

		// End time
		end, err := time.Parse(time.RFC3339, whitelistTimePrefix+times[1]+whitelistTimePostfix)
		if err != nil {
			log.Error().Msgf("processHours(): %v cannot be parsed: %v", times[1], err)
			os.Exit(3)
		}

		// If start is after end it means it contains midnight, so split in two.
		if start.After(end) {
			nextDayStart := whitelistAbsoluteStart
			nextDayEnd := end
			mergeTimespans(nextDayStart, nextDayEnd, direction)
			end = whitelistAbsoluteEnd
		}

		// Merge timespans.
		mergeTimespans(start, end, direction)
	}
}

// updateWhitelistSecondCount adds the difference between two times to an accumulator
func updateWhitelistSecondCount(start, end time.Time) bool {
	whitelistSecondCount += int(end.Sub(start).Seconds())
	return true
}
