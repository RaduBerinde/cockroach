// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package execstats

type Key string

const (
	RowsRead       Key = "rows read"
	LeftRowsRead   Key = "left rows read"
	RightRowsRaed  Key = "right rows read"
	StallTime      Key = "stall time"
	IndexStallTime Key = "index stall time"
	MaxMemory      Key = "max memory used"
	MaxDiskQuery   Key = "max disk used"
	BytesRead      Key = "bytes read"
)

type Entry struct {
	Key   Key
	Value string
}
