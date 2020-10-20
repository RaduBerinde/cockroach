// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package testutils

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/sql/opt"
	"github.com/stretchr/testify/assert"
)

func TestScalarVars(t *testing.T) {
	// toStr recreates the variables definition from md and notNullCols.
	toStr := func(md *opt.Metadata, notNullCols opt.ColSet) string {
		var buf bytes.Buffer
		for i := 0; i < md.NumColumns(); i++ {
			id := opt.ColumnID(i + 1)
			m := md.ColumnMeta(id)
			if i > 0 {
				buf.WriteString(", ")
			}
			fmt.Fprintf(&buf, "%s %s", m.Alias, m.Type)
			if notNullCols.Contains(id) {
				buf.WriteString(" not null")
			}
		}
		return buf.String()
	}

	vars := "a int, b string not null, c decimal"
	sc, err := MakeScalarVars(vars)
	if err != nil {
		t.Fatal(err)
	}
	md := &opt.Metadata{}
	md.Init()
	notNullCols := sc.AddToMetadata(md)
	assert.Equal(t, toStr(md, notNullCols), vars)
}
