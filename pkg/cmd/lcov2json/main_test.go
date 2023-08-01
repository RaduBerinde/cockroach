// Copyright 2023 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/cockroachdb/datadriven"
)

func TestLcovToJson(t *testing.T) {
	datadriven.RunTest(t, "testdata/lcov2json", func(t *testing.T, td *datadriven.TestData) string {
		switch td.Cmd {
		case "convert":
			in := strings.NewReader(td.Input)
			out := &bytes.Buffer{}
			if err := convertLcovToJson(in, out); err != nil {
				return fmt.Sprintf("error: %v", err)
			}
			result := out.String()
			return result

		default:
			td.Fatalf(t, "unknown command %s", td.Cmd)
			return ""
		}
	})
}
