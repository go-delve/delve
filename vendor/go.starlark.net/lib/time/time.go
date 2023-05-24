// Copyright 2021 The Bazel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package time provides time-related constants and functions.
package time // import "go.starlark.net/lib/time"

import (
	"fmt"
	"sort"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
)

// Module time is a Starlark module of time-related functions and constants.
// The module defines the following functions:
//
//     from_timestamp(sec, nsec) - Converts the given Unix time corresponding to the number of seconds
//                                 and (optionally) nanoseconds since January 1, 1970 UTC into an object
//                                 of type Time. For more details, refer to https://pkg.go.dev/time#Unix.
//
//     is_valid_timezone(loc) - Reports whether loc is a valid time zone name.
//
//     now() - Returns the current local time. Applications may replace this function by a deterministic one.
//
//     parse_duration(d) - Parses the given duration string. For more details, refer to
//                         https://pkg.go.dev/time#ParseDuration.
//
//     parseTime(x, format, location) - Parses the given time string using a specific time format and location.
//                                      The expected arguments are a time string (mandatory), a time format
//                                      (optional, set to RFC3339 by default, e.g. "2021-03-22T23:20:50.52Z")
//                                      and a name of location (optional, set to UTC by default). For more details,
//                                      refer to https://pkg.go.dev/time#Parse and https://pkg.go.dev/time#ParseInLocation.
//
//     time(year, month, day, hour, minute, second, nanosecond, location) - Returns the Time corresponding to
//	                                                                        yyyy-mm-dd hh:mm:ss + nsec nanoseconds
//                                                                          in the appropriate zone for that time
//                                                                          in the given location. All the parameters
//                                                                          are optional.
// The module also defines the following constants:
//
//     nanosecond - A duration representing one nanosecond.
//     microsecond - A duration representing one microsecond.
//     millisecond - A duration representing one millisecond.
//     second - A duration representing one second.
//     minute - A duration representing one minute.
//     hour - A duration representing one hour.
//
var Module = &starlarkstruct.Module{
	Name: "time",
	Members: starlark.StringDict{
		"from_timestamp":    starlark.NewBuiltin("from_timestamp", fromTimestamp),
		"is_valid_timezone": starlark.NewBuiltin("is_valid_timezone", isValidTimezone),
		"now":               starlark.NewBuiltin("now", now),
		"parse_duration":    starlark.NewBuiltin("parse_duration", parseDuration),
		"parse_time":        starlark.NewBuiltin("parse_time", parseTime),
		"time":              starlark.NewBuiltin("time", newTime),

		"nanosecond":  Duration(time.Nanosecond),
		"microsecond": Duration(time.Microsecond),
		"millisecond": Duration(time.Millisecond),
		"second":      Duration(time.Second),
		"minute":      Duration(time.Minute),
		"hour":        Duration(time.Hour),
	},
}

// NowFunc is a function that generates the current time. Intentionally exported
// so that it can be overridden, for example by applications that require their
// Starlark scripts to be fully deterministic.
var NowFunc = time.Now

func parseDuration(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var d Duration
	err := starlark.UnpackPositionalArgs("parse_duration", args, kwargs, 1, &d)
	return d, err
}

func isValidTimezone(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	if err := starlark.UnpackPositionalArgs("is_valid_timezone", args, kwargs, 1, &s); err != nil {
		return nil, err
	}
	_, err := time.LoadLocation(s)
	return starlark.Bool(err == nil), nil
}

func parseTime(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		x        string
		location = "UTC"
		format   = time.RFC3339
	)
	if err := starlark.UnpackArgs("parse_time", args, kwargs, "x", &x, "format?", &format, "location?", &location); err != nil {
		return nil, err
	}

	if location == "UTC" {
		t, err := time.Parse(format, x)
		if err != nil {
			return nil, err
		}
		return Time(t), nil
	}

	loc, err := time.LoadLocation(location)
	if err != nil {
		return nil, err
	}
	t, err := time.ParseInLocation(format, x, loc)
	if err != nil {
		return nil, err
	}
	return Time(t), nil
}

func fromTimestamp(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		sec  int64
		nsec int64 = 0
	)
	if err := starlark.UnpackPositionalArgs("from_timestamp", args, kwargs, 1, &sec, &nsec); err != nil {
		return nil, err
	}
	return Time(time.Unix(sec, nsec)), nil
}

func now(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	return Time(NowFunc()), nil
}

// Duration is a Starlark representation of a duration.
type Duration time.Duration

// Assert at compile time that Duration implements Unpacker.
var _ starlark.Unpacker = (*Duration)(nil)

// Unpack is a custom argument unpacker
func (d *Duration) Unpack(v starlark.Value) error {
	switch x := v.(type) {
	case Duration:
		*d = x
		return nil
	case starlark.String:
		dur, err := time.ParseDuration(string(x))
		if err != nil {
			return err
		}

		*d = Duration(dur)
		return nil
	}

	return fmt.Errorf("got %s, want a duration, string, or int", v.Type())
}

// String implements the Stringer interface.
func (d Duration) String() string { return time.Duration(d).String() }

// Type returns a short string describing the value's type.
func (d Duration) Type() string { return "time.duration" }

// Freeze renders Duration immutable. required by starlark.Value interface
// because duration is already immutable this is a no-op.
func (d Duration) Freeze() {}

// Hash returns a function of x such that Equals(x, y) => Hash(x) == Hash(y)
// required by starlark.Value interface.
func (d Duration) Hash() (uint32, error) {
	return uint32(d) ^ uint32(int64(d)>>32), nil
}

// Truth reports whether the duration is non-zero.
func (d Duration) Truth() starlark.Bool { return d != 0 }

// Attr gets a value for a string attribute, implementing dot expression support
// in starklark. required by starlark.HasAttrs interface.
func (d Duration) Attr(name string) (starlark.Value, error) {
	switch name {
	case "hours":
		return starlark.Float(time.Duration(d).Hours()), nil
	case "minutes":
		return starlark.Float(time.Duration(d).Minutes()), nil
	case "seconds":
		return starlark.Float(time.Duration(d).Seconds()), nil
	case "milliseconds":
		return starlark.MakeInt64(time.Duration(d).Milliseconds()), nil
	case "microseconds":
		return starlark.MakeInt64(time.Duration(d).Microseconds()), nil
	case "nanoseconds":
		return starlark.MakeInt64(time.Duration(d).Nanoseconds()), nil
	}
	return nil, fmt.Errorf("unrecognized %s attribute %q", d.Type(), name)
}

// AttrNames lists available dot expression strings. required by
// starlark.HasAttrs interface.
func (d Duration) AttrNames() []string {
	return []string{
		"hours",
		"minutes",
		"seconds",
		"milliseconds",
		"microseconds",
		"nanoseconds",
	}
}

// CompareSameType implements comparison of two Duration values. required by
// starlark.Comparable interface.
func (d Duration) CompareSameType(op syntax.Token, v starlark.Value, depth int) (bool, error) {
	cmp := 0
	if x, y := d, v.(Duration); x < y {
		cmp = -1
	} else if x > y {
		cmp = 1
	}
	return threeway(op, cmp), nil
}

// Binary implements binary operators, which satisfies the starlark.HasBinary
// interface. operators:
//    duration + duration = duration
//    duration + time = time
//    duration - duration = duration
//    duration / duration = float
//    duration / int = duration
//    duration / float = duration
//    duration // duration = int
//    duration * int = duration
func (d Duration) Binary(op syntax.Token, y starlark.Value, side starlark.Side) (starlark.Value, error) {
	x := time.Duration(d)

	switch op {
	case syntax.PLUS:
		switch y := y.(type) {
		case Duration:
			return Duration(x + time.Duration(y)), nil
		case Time:
			return Time(time.Time(y).Add(x)), nil
		}

	case syntax.MINUS:
		switch y := y.(type) {
		case Duration:
			return Duration(x - time.Duration(y)), nil
		}

	case syntax.SLASH:
		switch y := y.(type) {
		case Duration:
			if y == 0 {
				return nil, fmt.Errorf("%s division by zero", d.Type())
			}
			return starlark.Float(x.Nanoseconds()) / starlark.Float(time.Duration(y).Nanoseconds()), nil
		case starlark.Int:
			if side == starlark.Right {
				return nil, fmt.Errorf("unsupported operation")
			}
			i, ok := y.Int64()
			if !ok {
				return nil, fmt.Errorf("int value out of range (want signed 64-bit value)")
			}
			if i == 0 {
				return nil, fmt.Errorf("%s division by zero", d.Type())
			}
			return d / Duration(i), nil
		case starlark.Float:
			f := float64(y)
			if f == 0 {
				return nil, fmt.Errorf("%s division by zero", d.Type())
			}
			return Duration(float64(x.Nanoseconds()) / f), nil
		}

	case syntax.SLASHSLASH:
		switch y := y.(type) {
		case Duration:
			if y == 0 {
				return nil, fmt.Errorf("%s division by zero", d.Type())
			}
			return starlark.MakeInt64(x.Nanoseconds() / time.Duration(y).Nanoseconds()), nil
		}

	case syntax.STAR:
		switch y := y.(type) {
		case starlark.Int:
			i, ok := y.Int64()
			if !ok {
				return nil, fmt.Errorf("int value out of range (want signed 64-bit value)")
			}
			return d * Duration(i), nil
		}
	}

	return nil, nil
}

// Time is a Starlark representation of a moment in time.
type Time time.Time

func newTime(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		year, month, day, hour, min, sec, nsec int
		loc                                    string
	)
	if err := starlark.UnpackArgs("time", args, kwargs,
		"year?", &year,
		"month?", &month,
		"day?", &day,
		"hour?", &hour,
		"minute?", &min,
		"second?", &sec,
		"nanosecond?", &nsec,
		"location?", &loc,
	); err != nil {
		return nil, err
	}
	if len(args) > 0 {
		return nil, fmt.Errorf("time: unexpected positional arguments")
	}
	location, err := time.LoadLocation(loc)
	if err != nil {
		return nil, err
	}
	return Time(time.Date(year, time.Month(month), day, hour, min, sec, nsec, location)), nil
}

// String returns the time formatted using the format string
//	"2006-01-02 15:04:05.999999999 -0700 MST".
func (t Time) String() string { return time.Time(t).String() }

// Type returns "time.time".
func (t Time) Type() string { return "time.time" }

// Freeze renders time immutable. required by starlark.Value interface
// because Time is already immutable this is a no-op.
func (t Time) Freeze() {}

// Hash returns a function of x such that Equals(x, y) => Hash(x) == Hash(y)
// required by starlark.Value interface.
func (t Time) Hash() (uint32, error) {
	return uint32(time.Time(t).UnixNano()) ^ uint32(int64(time.Time(t).UnixNano())>>32), nil
}

// Truth returns the truth value of an object required by starlark.Value
// interface.
func (t Time) Truth() starlark.Bool { return starlark.Bool(time.Time(t).IsZero()) }

// Attr gets a value for a string attribute, implementing dot expression support
// in starklark. required by starlark.HasAttrs interface.
func (t Time) Attr(name string) (starlark.Value, error) {
	switch name {
	case "year":
		return starlark.MakeInt(time.Time(t).Year()), nil
	case "month":
		return starlark.MakeInt(int(time.Time(t).Month())), nil
	case "day":
		return starlark.MakeInt(time.Time(t).Day()), nil
	case "hour":
		return starlark.MakeInt(time.Time(t).Hour()), nil
	case "minute":
		return starlark.MakeInt(time.Time(t).Minute()), nil
	case "second":
		return starlark.MakeInt(time.Time(t).Second()), nil
	case "nanosecond":
		return starlark.MakeInt(time.Time(t).Nanosecond()), nil
	case "unix":
		return starlark.MakeInt64(time.Time(t).Unix()), nil
	case "unix_nano":
		return starlark.MakeInt64(time.Time(t).UnixNano()), nil
	}
	return builtinAttr(t, name, timeMethods)
}

// AttrNames lists available dot expression strings for time. required by
// starlark.HasAttrs interface.
func (t Time) AttrNames() []string {
	return append(builtinAttrNames(timeMethods),
		"year",
		"month",
		"day",
		"hour",
		"minute",
		"second",
		"nanosecond",
		"unix",
		"unix_nano",
	)
}

// CompareSameType implements comparison of two Time values. required by
// starlark.Comparable interface.
func (t Time) CompareSameType(op syntax.Token, yV starlark.Value, depth int) (bool, error) {
	x := time.Time(t)
	y := time.Time(yV.(Time))
	cmp := 0
	if x.Before(y) {
		cmp = -1
	} else if x.After(y) {
		cmp = 1
	}
	return threeway(op, cmp), nil
}

// Binary implements binary operators, which satisfies the starlark.HasBinary
// interface
//    time + duration = time
//    time - duration = time
//    time - time = duration
func (t Time) Binary(op syntax.Token, y starlark.Value, side starlark.Side) (starlark.Value, error) {
	x := time.Time(t)

	switch op {
	case syntax.PLUS:
		switch y := y.(type) {
		case Duration:
			return Time(x.Add(time.Duration(y))), nil
		}
	case syntax.MINUS:
		switch y := y.(type) {
		case Duration:
			return Time(x.Add(time.Duration(-y))), nil
		case Time:
			// time - time = duration
			return Duration(x.Sub(time.Time(y))), nil
		}
	}

	return nil, nil
}

var timeMethods = map[string]builtinMethod{
	"in_location": timeIn,
	"format":      timeFormat,
}

func timeFormat(fnname string, recV starlark.Value, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var x string
	if err := starlark.UnpackPositionalArgs("format", args, kwargs, 1, &x); err != nil {
		return nil, err
	}

	recv := time.Time(recV.(Time))
	return starlark.String(recv.Format(x)), nil
}

func timeIn(fnname string, recV starlark.Value, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var x string
	if err := starlark.UnpackPositionalArgs("in_location", args, kwargs, 1, &x); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(x)
	if err != nil {
		return nil, err
	}

	recv := time.Time(recV.(Time))
	return Time(recv.In(loc)), nil
}

type builtinMethod func(fnname string, recv starlark.Value, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error)

func builtinAttr(recv starlark.Value, name string, methods map[string]builtinMethod) (starlark.Value, error) {
	method := methods[name]
	if method == nil {
		return nil, nil // no such method
	}

	// Allocate a closure over 'method'.
	impl := func(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		return method(b.Name(), b.Receiver(), args, kwargs)
	}
	return starlark.NewBuiltin(name, impl).BindReceiver(recv), nil
}

func builtinAttrNames(methods map[string]builtinMethod) []string {
	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Threeway interprets a three-way comparison value cmp (-1, 0, +1)
// as a boolean comparison (e.g. x < y).
func threeway(op syntax.Token, cmp int) bool {
	switch op {
	case syntax.EQL:
		return cmp == 0
	case syntax.NEQ:
		return cmp != 0
	case syntax.LE:
		return cmp <= 0
	case syntax.LT:
		return cmp < 0
	case syntax.GE:
		return cmp >= 0
	case syntax.GT:
		return cmp > 0
	}
	panic(op)
}
