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
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// lcov2json is a program that converts from LCOV format to the Codecov JSON
// format (https://docs.codecov.com/docs/codecov-custom-coverage-format).
//
// Example of the output format:
//
//	{
//	  "coverage": {
//	    "filename": {
//	      "1": 0,      # line 1 missed
//	      "2": 1,      # line 2 hit once
//	      "7": 5       # line 7 hit 5 times
//	    }
//	  }
//	}
func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <input.dat> <output.json>\n", os.Args[0])
		os.Exit(1)
	}
	lcovFile := os.Args[1]
	jsonFile := os.Args[2]
	in, err := os.Open(lcovFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening %q: %v", lcovFile, err)
		os.Exit(2)
	}
	defer in.Close()
	out, err := os.Create(jsonFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating %q: %v", jsonFile, err)
		os.Exit(2)
	}
	if err := convertLcovToJson(in, out); err != nil {
		fmt.Fprintf(os.Stderr, "%v", err)
		os.Exit(2)
	}
	if err := out.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "Error closing %q: %v", jsonFile, err)
		os.Exit(2)
	}
}

// lineCount is the hit count for a line, or -1 if the line has no count.
type lineCount int

const noCount lineCount = -1

func convertLcovToJson(lcovReader io.Reader, jsonWriter io.Writer) error {
	lcov := bufio.NewScanner(lcovReader)
	w := bufio.NewWriter(jsonWriter)

	w.WriteString("{\n")
	w.WriteString("  \"coverage\": {")
	firstFile := true
	var currentFile string
	var currentLines []lineCount
	for lcov.Scan() {
		l := lcov.Text()
		if l == "end_of_record" {
			if currentFile == "" {
				return errors.New("end_of_record with no file path")
			}
			if !firstFile {
				w.WriteString(",")
			}
			firstFile = false
			w.WriteString("\n")
			fmt.Fprintf(w, "    %q: {", currentFile)
			first := true
			for i, count := range currentLines {
				if count < 0 {
					continue
				}
				if !first {
					w.WriteString(",")
				}
				first = false
				w.WriteString("\n")
				fmt.Fprintf(w, "      \"%d\": %d", i, count)
			}
			w.WriteString("\n    }")

			currentFile = ""
		}
		idx := strings.Index(l, ":")
		if idx == -1 {
			// Don't know how to parse this line; skip.
			continue
		}
		key := l[:idx]
		val := l[idx+1:]
		switch key {
		case "SF":
			currentFile = val
			currentLines = currentLines[:0]
		case "DA":
			var line, count int
			_, err := fmt.Sscanf(val, "%d,%d", &line, &count)
			if err != nil {
				return fmt.Errorf("error parsing DA line: %v", err)
			}
			// Sanity check
			if line > 1000000 {
				break
			}
			for len(currentLines) <= line {
				currentLines = append(currentLines, -1)
			}
			currentLines[line] = lineCount(count)
		}
	}
	w.WriteString("\n  }\n")
	w.WriteString("}\n")
	if currentFile != "" {
		return errors.New("unfinished record")
	}
	if err := lcov.Err(); err != nil {
		return err
	}
	return w.Flush()
}
