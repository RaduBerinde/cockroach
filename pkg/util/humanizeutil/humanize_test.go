// Copyright 2016 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package humanizeutil_test

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/util/humanizeutil"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
)

// TestHumanizeBytes verifies both IBytes and ParseBytes.
func TestBytes(t *testing.T) {
	defer leaktest.AfterTest(t)()

	testCases := []struct {
		value       int64
		exp         string
		expNeg      string
		parseExp    int64
		parseErr    string
		parseErrNeg string
	}{
		{0, "0 B", "0 B", 0, "", ""},
		{1024, "1.0 KiB", "-1.0 KiB", 1024, "", ""},
		{1024 << 10, "1.0 MiB", "-1.0 MiB", 1024 << 10, "", ""},
		{1024 << 20, "1.0 GiB", "-1.0 GiB", 1024 << 20, "", ""},
		{1024 << 30, "1.0 TiB", "-1.0 TiB", 1024 << 30, "", ""},
		{1024 << 40, "1.0 PiB", "-1.0 PiB", 1024 << 40, "", ""},
		{1024 << 50, "1.0 EiB", "-1.0 EiB", 1024 << 50, "", ""},
		{int64(math.MaxInt64), "8.0 EiB", "-8.0 EiB", 0, "too large: 8.0 EiB", "too large: -8.0 EiB"},
	}

	for i, testCase := range testCases {
		// Test IBytes.
		if actual := string(humanizeutil.IBytes(testCase.value)); actual != testCase.exp {
			t.Errorf("%d: IBytes(%d) actual:%s does not match expected:%s", i, testCase.value, actual, testCase.exp)
		}
		// Test negative IBytes.
		if actual := string(humanizeutil.IBytes(-testCase.value)); actual != testCase.expNeg {
			t.Errorf("%d: IBytes(%d) actual:%s does not match expected:%s", i, -testCase.value, actual,
				testCase.expNeg)
		}
		// Test ParseBytes.
		if actual, err := humanizeutil.ParseBytes(testCase.exp); err != nil {
			if len(testCase.parseErr) > 0 {
				if testCase.parseErr != err.Error() {
					t.Errorf("%d: ParseBytes(%s) caused an incorrect error actual:%s, expected:%s", i, testCase.exp,
						err, testCase.parseErr)
				}
			} else {
				t.Errorf("%d: ParseBytes(%s) caused an unexpected error:%s", i, testCase.exp, err)
			}
		} else if actual != testCase.parseExp {
			t.Errorf("%d: ParseBytes(%s) actual:%d does not match expected:%d", i, testCase.exp, actual,
				testCase.parseExp)
		}
		// Test negative ParseBytes.
		if actual, err := humanizeutil.ParseBytes(testCase.expNeg); err != nil {
			if len(testCase.parseErrNeg) > 0 {
				if testCase.parseErrNeg != err.Error() {
					t.Errorf("%d: ParseBytes(%s) caused an incorrect error actual:%s, expected:%s", i, testCase.expNeg,
						err, testCase.parseErrNeg)
				}
			} else {
				t.Errorf("%d: ParseBytes(%s) caused an unexpected error:%s", i, testCase.expNeg, err)
			}
		} else if actual != -testCase.parseExp {
			t.Errorf("%d: ParseBytes(%s) actual:%d does not match expected:%d", i, testCase.expNeg, actual,
				-testCase.parseExp)
		}
	}

	// Some extra error cases for good measure.
	testFailCases := []struct {
		value    string
		expected string
	}{
		{"", "parsing \"\": invalid syntax"},   // our error
		{"1 ZB", "unhandled size name: zb"},    // humanize's error
		{"-1 ZB", "unhandled size name: zb"},   // humanize's error
		{"1 ZiB", "unhandled size name: zib"},  // humanize's error
		{"-1 ZiB", "unhandled size name: zib"}, // humanize's error
		{"100 EiB", "too large: 100 EiB"},      // humanize's error
		{"-100 EiB", "too large: 100 EiB"},     // humanize's error
		{"10 EiB", "too large: 10 EiB"},        // our error
		{"-10 EiB", "too large: -10 EiB"},      // our error
	}
	for i, testCase := range testFailCases {
		if _, err := humanizeutil.ParseBytes(testCase.value); err.Error() != testCase.expected {
			t.Errorf("%d: ParseBytes(%s) caused an incorrect error actual:%s, expected:%s", i, testCase.value, err,
				testCase.expected)
		}
	}
}

func TestIBytesExact(t *testing.T) {
	for _, tc := range []struct {
		value    int64
		expected string
	}{
		{value: 0, expected: "0 B"},
		{value: 1, expected: "1 B"},
		{value: -1, expected: "-1 B"},
		{value: 1023, expected: "1023 B"},
		{value: 1024, expected: "1 KiB"},
		{value: -1024, expected: "-1 KiB"},
		{value: 1025, expected: "1025 B"},
		{value: 2 << 20, expected: "2 MiB"},
		{value: 12345 << 20, expected: "12345 MiB"},
		{value: 512 << 30, expected: "512 GiB"},
		{value: 100 << 40, expected: "100 TiB"},
		{value: 123 << 50, expected: "123 PiB"},
		{value: 2 << 60, expected: "2 EiB"},
	} {
		if actual := string(humanizeutil.IBytesExact(tc.value)); actual != tc.expected {
			t.Errorf("IBytesExact(%d) = %q, expected %q", tc.value, actual, tc.expected)
		}
	}
	checkRoundtrip := func(v int64) {
		humanized := humanizeutil.IBytesExact(v)
		after, err := humanizeutil.ParseBytes(string(humanized))
		if err != nil || after != v {
			t.Helper()
			t.Fatalf("IBytesExact(%d)=%q does not roundtrip (after: %d, err: %v)", v, humanized, after, err)
		}
	}
	checkRoundtrip(0)
	for it := 0; it < 1000; it++ {
		// ParseBytes uses a float; very large values are not parsed exactly.
		x := rand.Int64N(1 << 50)
		checkRoundtrip(x)
		checkRoundtrip(-x)
		// Generate numbers that are divisible by powers of 2.
		x = int64(rand.IntN(1024*1024)) << rand.IntN(40)
		checkRoundtrip(x)
		checkRoundtrip(-x)
	}
}
