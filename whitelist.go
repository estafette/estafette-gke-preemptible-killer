package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/go-intervals/timespanset"
	"github.com/rs/zerolog/log"
)

const (
	// whitelistStartPrefix in `YYYY-MM-DDT` format, can be anthing
	whitelistStartPrefix = "2000-01-01T"

	// whitelistEndPrefix in `YYYY-MM-DDT` format, has to be whitelistStartPrefix plus one day
	whitelistEndPrefix = "2000-01-02T"

	// whitelistTimePostfix in `:ssZ` format, can be anything
	whitelistTimePostfix = ":00Z"
)

var (
	whitelistStart time.Time
	whitelistEnd   time.Time
)

func init() {
	var err error

	// whitelistStart is the start of the day
	whitelistStart, err = time.Parse(time.RFC3339, whitelistStartPrefix+"00:00"+whitelistTimePostfix)
	if err != nil {
		panic("whitelistStart parse error")
	}

	// whitelistEnd is the start of the next day
	whitelistEnd, err = time.Parse(time.RFC3339, whitelistEndPrefix+"00:00"+whitelistTimePostfix)
	if err != nil {
		panic("whitelistEnd parse error")
	}
}

// WhitelistInstance is resposible for one processing of whitelist and blacklist hours
type WhitelistInstance struct {
	// blacklist contains blacklists as passed arguments
	blacklist string

	// whitelist contains whitelists as passed arguments
	whitelist string

	// whitelistHours are whitelist periods
	whitelistHours *timespanset.Set

	// whitelistSecondCount is the total number of seconds cumulated from all the whitelist periods combined
	whitelistSecondCount int
}

// initializeWhitelistHours initializes data structures by taking command line arguments into account.
func (w *WhitelistInstance) initialize() {
	w.whitelistHours = timespanset.Empty()
	w.whitelistSecondCount = 0
}

func (w *WhitelistInstance) parseArguments() {
	w.initialize()
	if len(w.whitelist) == 0 {
		// If there's no whitelist, than the maximum range has to be allowed so that any blacklist
		// might be subtracted from it.
		w.processHours("00:00 - 12:00, 12:00 - 00:00", "+")
	} else {
		w.processHours(w.whitelist, "+")
	}

	w.processHours(w.blacklist, "-")
	w.whitelistHours.IntervalsBetween(whitelistStart, whitelistEnd, w.updateWhitelistSecondCount)
}

// getExpiryDate calculates the expiry date of a node.
func (w *WhitelistInstance) getExpiryDate(t time.Time, timeToBeAdded time.Duration) (expiryDatetime time.Time) {
	offset := 0 * time.Second
	truncatedCreationTime := t.Truncate(24 * time.Hour)
	projectedCreation := whitelistStart.Add(t.Sub(truncatedCreationTime))

	for timeToBeAdded > 0 {
		// If the expiry date time is bound to surpass the 24h hard limit...
		if offset >= 48*time.Hour {
			// Let the user know and fallback to no whitelist.
			log.Error().Msg(`Falling back to no whitelists. Contact
				maintainer, whitelist resolution is wrong, it surpasses the
				24h hard limit of preemptible nodes.`)
			w.initialize()
			w.whitelist = ""
			w.blacklist = ""
			w.parseArguments()
			return w.getExpiryDate(t, timeToBeAdded)
		}

		// For all whitelisted intervals...
		w.whitelistHours.IntervalsBetween(whitelistStart, whitelistEnd, func(start, end time.Time) bool {
			// If the current interval ends before the creation...
			if offset == 0 && end.Before(projectedCreation) {
				// Skip for now.
				return true
			}

			// If creation is in the middle of the current interval...
			if offset == 0 && start.Before(projectedCreation) {
				// Start with creation.
				start = projectedCreation
			}

			// If expiry time has been reached...
			intervalDuration := end.Sub(start)
			if timeToBeAdded <= intervalDuration {
				// This is it, project it back to real time.
				expiryDatetime = truncatedCreationTime.Add(start.Add(timeToBeAdded).Sub(whitelistStart)).Add(offset)
			}

			// Consume this interval.
			timeToBeAdded -= intervalDuration

			// Do we want another interval?
			return timeToBeAdded > 0
		})

		// Advancing to the next set of intervals means advancing another 24h in the expiry date
		// time, but this is not effective in the times used above since they repeat in the same
		// interval, so this offset brings that advance into effect. This does not make the expiry
		// date time surpass the 24h hard limit of preemptible nodes. AN offset of 24h would mean
		// 24h since truncatedCreationTime meaning it is the next day, but before usual deletion.
		offset += 24 * time.Hour
	}
	return expiryDatetime
}

// mergeTimespans merges time intervals.
func (w *WhitelistInstance) mergeTimespans(start time.Time, end time.Time, direction string) {
	if direction == "+" {
		w.whitelistHours.Insert(start, end)
	} else if direction == "-" {
		subtrahend := timespanset.Empty()
		subtrahend.Insert(start, end)
		w.whitelistHours.Sub(subtrahend)
	} else {
		panic(fmt.Sprintf("mergeTimespans(): direction expected to be + or - but got '%v'", direction))
	}
}

// processHours parses time stamps and passes them to mergeTimespans(), direction can be "+" or "-".
func (w *WhitelistInstance) processHours(input string, direction string) {
	// Time not specified, continue with no restrictions.
	if len(input) == 0 {
		return
	}

	// Split in intervals.
	input = strings.Replace(input, " ", "", -1)
	intervals := strings.Split(input, ",")
	for _, timeInterval := range intervals {
		times := strings.Split(timeInterval, "-")

		// Check format.
		if len(times) != 2 {
			panic(fmt.Sprintf("processHours(): interval '%v' should be of the form `09:00 - 11:00[, 21:00 - 23:00[, ...]]`", timeInterval))
		}

		// Start time
		start, err := time.Parse(time.RFC3339, whitelistStartPrefix+times[0]+whitelistTimePostfix)
		if err != nil {
			panic(fmt.Sprintf("processHours(): %v cannot be parsed: %v", times[0], err))
		}

		// End time
		end, err := time.Parse(time.RFC3339, whitelistStartPrefix+times[1]+whitelistTimePostfix)
		if err != nil {
			panic(fmt.Sprintf("processHours(): %v cannot be parsed: %v", times[1], err))
		}

		// If end is before start it means it contains midnight, so split in two.
		if end.Before(start) {
			w.mergeTimespans(whitelistStart, end, direction)
			end = whitelistEnd
		}

		// Merge timespans.
		w.mergeTimespans(start, end, direction)
	}
}

// updateWhitelistSecondCount adds the difference between two times to an accumulator.
func (w *WhitelistInstance) updateWhitelistSecondCount(start, end time.Time) bool {
	if end.Before(start) {
		panic(fmt.Sprintf("updateWhitelistSecondCount(): go-intervals timespanset is acting up providing reverse intervals: %v - %v", start, end))
	}
	w.whitelistSecondCount += int(end.Sub(start).Seconds())
	return true
}
