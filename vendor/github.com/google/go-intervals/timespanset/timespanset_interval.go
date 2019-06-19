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

package timespanset

import (
	"fmt"
	"time"

	"github.com/google/go-intervals/intervalset"
)

func min(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func max(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// timespan is a private implementation of intervalset.Interval.
type timespan struct {
	start, end time.Time
}

func (ts *timespan) String() string {
	return fmt.Sprintf("[%s, %s)", ts.start, ts.end)
}

func (ts *timespan) Equal(b *timespan) bool {
	return ts.start.Equal(b.start) && ts.end.Equal(ts.end)
}

func (ts *timespan) intersect(b *timespan) *timespan {
	result := &timespan{
		max(ts.start, b.start),
		min(ts.end, b.end),
	}
	if result.start.Before(result.end) {
		return result
	}
	return &timespan{}
}

func (ts *timespan) IsZero() bool {
	return ts.start.IsZero() && ts.end.IsZero()
}

func trOrPanic(i intervalset.Interval) *timespan {
	tr, ok := i.(*timespan)
	if !ok {
		panic(fmt.Errorf("interval must be a time range: %v", i))
	}
	return tr
}

func (ts *timespan) Before(other intervalset.Interval) bool {
	return !trOrPanic(other).start.Before(ts.end)
}

func (ts *timespan) Bisect(other intervalset.Interval) (intervalset.Interval, intervalset.Interval) {
	b := trOrPanic(other)
	intersection := ts.intersect(b)
	if intersection.IsZero() {
		if ts.Before(b) {
			return ts, &timespan{}
		}
		return &timespan{}, ts
	}
	maybeZero := func(start, end time.Time) *timespan {
		if start.Equal(end) {
			return &timespan{}
		}
		return &timespan{start, end}
	}
	return maybeZero(ts.start, intersection.start), maybeZero(intersection.end, ts.end)
}

func (ts *timespan) Intersect(other intervalset.Interval) intervalset.Interval {
	return ts.intersect(trOrPanic(other))
}

func (ts *timespan) adjoin(b *timespan) *timespan {
	if ts.end.Equal(b.start) {
		return &timespan{ts.start, b.end}
	}
	if b.end.Equal(ts.start) {
		return &timespan{b.start, ts.end}
	}
	return &timespan{}
}

func (ts *timespan) Adjoin(other intervalset.Interval) intervalset.Interval {
	return ts.adjoin(trOrPanic(other))
}

func (ts *timespan) encompass(b *timespan) intervalset.Interval {
	return &timespan{min(ts.start, b.start), max(ts.end, b.end)}
}

func (ts *timespan) Encompass(other intervalset.Interval) intervalset.Interval {
	return ts.encompass(trOrPanic(other))
}
