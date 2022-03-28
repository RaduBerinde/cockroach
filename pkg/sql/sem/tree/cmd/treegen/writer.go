// Copyright 2022 The Cockroach Authors.
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
	"go/format"
	"strings"
)

type writer struct {
	buf       bytes.Buffer
	nestLevel int
}

func (w *writer) Write(format string, args ...interface{}) {
	fmt.Fprintf(&w.buf, strings.Repeat("  ", w.nestLevel)+format+"\n", args...)
}

func (w *writer) Nest(format string, args ...interface{}) {
	w.Write(format, args...)
	w.nestLevel++
}

func (w *writer) Unnest(format string, args ...interface{}) {
	w.nestLevel--
	w.Write(format, args...)
}

func (w *writer) RunGoFmt() error {
	formatted, err := format.Source(w.buf.Bytes())
	if err != nil {
		return err
	}
	w.buf.Reset()
	w.buf.Write(formatted)
	return nil
}

func (w *writer) Result() string {
	return w.buf.String()
}
