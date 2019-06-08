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
	whitelistTimePrefix = "2000-01-01T"

	// whitelistTimePlusOneDayPrefix in `YYYY-MM-DDT` format, has to be whitelistTimePrefix plus one day
	whitelistTimePlusOneDayPrefix = "2000-01-02T"

	// whitelistTimePlusOneDayPrefix in `YYYY-MM-DDT` format, has to be whitelistTimePrefix plus two days
	whitelistTimePlusTwoDaysPrefix = "2000-01-03T"

	// whitelistTimePostfix in `:ssZ` format, can be anything
	whitelistTimePostfix = ":00Z"
)

var (
	// whitelistStart is the start of the day
	whitelistStart, _ = time.Parse(time.RFC3339, whitelistTimePrefix+"00:00"+whitelistTimePostfix)

	// whitelistNextDayStart is the start of the next day
	whitelistNextDayStart, _ = time.Parse(time.RFC3339, whitelistTimePlusOneDayPrefix+"00:00"+whitelistTimePostfix)

	// whitelistAfterTwoDaysStart is the start of the day after the next day
	whitelistAfterTwoDaysStart, _ = time.Parse(time.RFC3339, whitelistTimePlusTwoDaysPrefix+"00:00"+whitelistTimePostfix)

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
		processHours("00:00 - 23:59, 23:59 - 23:58", whitelistHours, "+")
	} else {
		processHours(*whitelist, whitelistHours, "+")
	}

	processHours(*blacklist, whitelistHours, "-")
	whitelistHours.IntervalsBetween(whitelistStart, whitelistAfterTwoDaysStart, updateWhitelistSecondCount)
}

func getExpiryDate(t time.Time, timeToBeAdded time.Duration) (expiryDatetime time.Time) {
	truncatedCreationTime := t.Truncate(24 * time.Hour)
	durationFromStartOfDayUntilCreation := t.Sub(truncatedCreationTime)
	secondsToBeAdded := int(t.Add(timeToBeAdded).Sub(truncatedCreationTime).Seconds())
	whitelistAdjustedDurationToBeAdded := time.Duration(secondsToBeAdded%whitelistSecondCount) * time.Second

	firstInterval := true
	var projectedCreation time.Time
	for whitelistAdjustedDurationToBeAdded.Seconds() > 0 {
		whitelistHours.IntervalsBetween(whitelistStart, whitelistAfterTwoDaysStart, func(start, end time.Time) bool {
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

// mergeTimespans merges time intervals
func mergeTimespans(start time.Time, end time.Time, direction string) {
	if direction == "+" {
		whitelistHours.Insert(start, end)
	} else if direction == "-" {
		subtrahend := timespanset.Empty()
		subtrahend.Insert(start, end)
		whitelistHours.Sub(subtrahend)
	} else {
		log.Error().Msgf("mergeTimespans(): direction can only be + or -")
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
			nextDayStart := whitelistNextDayStart

			nextDayEnd, err := time.Parse(time.RFC3339, whitelistTimePlusOneDayPrefix+times[1]+whitelistTimePostfix)
			if err != nil {
				log.Error().Msgf("processHours(): %v cannot be parsed: %v", times[1], err)
				os.Exit(4)
			}

			mergeTimespans(nextDayStart, nextDayEnd, direction)
			end = whitelistNextDayStart
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
