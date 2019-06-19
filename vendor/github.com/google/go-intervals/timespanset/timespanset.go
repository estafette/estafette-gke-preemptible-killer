// Copyright 2017 Google Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package timespanset is a finite set implementation for time spans.
//
// DISCLAIMER: This library is not yet stable, so expect breaking changes.
package timespanset

import (
	"fmt"
	"time"

	"github.com/google/go-intervals/intervalset"
)

// Set is a finite set of time spans. Functions are provided for iterating over the
// spans and performing set operations (intersection, union, subtraction).
//
// This is a time span-specific implemention of intervalset.Set.
type Set struct {
	iset *intervalset.Set
}

// String returns a human readable version of the set.
func (s *Set) String() string {
	return s.iset.String()
}

// Empty returns a new, empty Set.
func Empty() *Set {
	return &Set{intervalset.Empty()}
}

// Copy returns a copy of a set that may be mutated without affecting the original.
func (s *Set) Copy() *Set {
	return &Set{s.iset.Copy()}
}

// Insert adds a single time span into the set.
func (s *Set) Insert(start, end time.Time) {
	if end.Before(start) {
		panic(fmt.Errorf("start %s before end %s", start, end))
	}
	s.iset.Add(intervalset.NewSet([]intervalset.Interval{&timespan{start, end}}))
}

// Add performs an in-place union of two sets.
func (s *Set) Add(b *Set) {
	s.iset.Add(b.iset)
}

// Sub performs an in-place subtraction of set b from set a.
func (s *Set) Sub(b *Set) {
	s.iset.Sub(b.iset)
}

// Intersect performs an in-place intersection of sets a and b.
func (s *Set) Intersect(b *Set) {
	s.iset.Intersect(b.iset)
}

// Extent returns the start and end time that defines the entire timespan
// covering the set. The returned times are the zero value for an empty set.
func (s *Set) Extent() (time.Time, time.Time) {
	x := s.iset.Extent()
	if x == nil {
		return time.Time{}, time.Time{}
	}
	tr := trOrPanic(x)
	return tr.start, tr.end
}

// Empty reports if the extent of the set is zero.
func (s *Set) Empty() bool {
	x := s.iset.Extent()
	if x == nil {
		return true
	}
	return x.IsZero()
}

// Contains reports whether a time span is entirely contained within the set.
func (s *Set) Contains(start, end time.Time) bool {
	return s.iset.Contains(&timespan{start, end})
}

// IntervalReceiver is a function used for iterating over a set of time
// ranges. It takes the start and end times and returns true if the iteration
// should continue.
type IntervalReceiver func(start, end time.Time) bool

// ensureNonAdjoining returns a modified version of an IntervalsBetween callback
// function that will always be called with non-adjoining intervals. To do this,
// it returns a function that accumulates adjoining intervals, calling f with
// the combined interval. The second return value is a function that should be
// called after iteration is complete to ensure the last interval is sent to f.
func ensureNonAdjoining(f IntervalReceiver) (IntervalReceiver, func()) {
	last := &timespan{}
	isDone := false
	doneFn := func() {
		if isDone {
			return
		}
		if !last.IsZero() {
			f(last.start, last.end)
		}
	}
	receiveInterval := func(start, end time.Time) bool {
		if isDone {
			panic("should not be done")
		}
		current := &timespan{start, end}
		adjoined := last.adjoin(current)
		if !adjoined.IsZero() {
			// Always continue if this interval adjoins the last one because the next
			// may also adjoin.
			last = adjoined
			return true
		}
		if !last.IsZero() {
			isDone = !f(last.start, last.end)
			if isDone {
				return false //stop iteration
			}
		}
		last = current
		return true // continue iteration
	}
	return receiveInterval, doneFn
}

// IntervalsBetween iterates over the time ranges within the set and calls f with the
// start (inclusive) and end (exclusive) of each. If f returns false, iteration
// ceases. Only intervals in between the provided start and end times are
// included.
func (s *Set) IntervalsBetween(start, end time.Time, f IntervalReceiver) {
	fPrime, done := ensureNonAdjoining(f)
	s.iset.IntervalsBetween(&timespan{start, end}, func(x intervalset.Interval) bool {
		tr := trOrPanic(x)
		return fPrime(tr.start, tr.end)
	})
	done()
}
