// Copyright 2021 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package tenantcostserver

import (
	"github.com/cockroachdb/cockroach/pkg/util/metric"
	"github.com/cockroachdb/cockroach/pkg/util/metric/aggmetric"
)

// Metrics is a metric.Struct for the LimiterFactory.
type Metrics struct {
	TotalRU                *aggmetric.AggGaugeFloat64
	TotalReadRequests      *aggmetric.AggGauge
	TotalReadBytes         *aggmetric.AggGauge
	TotalWriteRequests     *aggmetric.AggGauge
	TotalWriteBytes        *aggmetric.AggGauge
	TotalSQLPodsCPUSeconds *aggmetric.AggGaugeFloat64
}

var _ metric.Struct = (*Metrics)(nil)

// MetricStruct indicates that Metrics is a metric.Struct
func (m *Metrics) MetricStruct() {}

var (
	metaTotalRU = metric.Metadata{
		Name:        "tenant.consumption.request_units",
		Help:        "Total RU consumption",
		Measurement: "Request Units",
		Unit:        metric.Unit_COUNT,
	}
	metaTotalReadRequests = metric.Metadata{
		Name:        "tenant.consumption.read_requests",
		Help:        "Total number of KV read requests",
		Measurement: "Requests",
		Unit:        metric.Unit_COUNT,
	}
	metaTotalReadBytes = metric.Metadata{
		Name:        "tenant.consumption.read_bytes",
		Help:        "Total number of bytes read from KV",
		Measurement: "Bytes",
		Unit:        metric.Unit_COUNT,
	}
	metaTotalWriteRequests = metric.Metadata{
		Name:        "tenant.consumption.write_requests",
		Help:        "Total number of KV write requests",
		Measurement: "Requests",
		Unit:        metric.Unit_COUNT,
	}
	metaTotalWriteBytes = metric.Metadata{
		Name:        "tenant.consumption.write_bytes",
		Help:        "Total number of bytes written to KV",
		Measurement: "Bytes",
		Unit:        metric.Unit_COUNT,
	}
	metaTotalSQLPodsCPUSeconds = metric.Metadata{
		Name:        "tenant.consumption.sql_pods_cpu_seconds",
		Help:        "Total number of bytes written to KV",
		Measurement: "CPU Seconds",
		Unit:        metric.Unit_SECONDS,
	}
)

// TenantIDLabel is the label used with metrics associated with a tenant.
// The value will be the integer tenant ID.
// TODO(radu): move this in a suitable common place.
const TenantIDLabel = "tenant_id"

func makeMetrics() Metrics {
	b := aggmetric.MakeBuilder(TenantIDLabel)
	return Metrics{
		TotalRU:                b.GaugeFloat64(metaTotalRU),
		TotalReadRequests:      b.Gauge(metaTotalReadRequests),
		TotalReadBytes:         b.Gauge(metaTotalReadBytes),
		TotalWriteRequests:     b.Gauge(metaTotalWriteRequests),
		TotalWriteBytes:        b.Gauge(metaTotalWriteBytes),
		TotalSQLPodsCPUSeconds: b.GaugeFloat64(metaTotalSQLPodsCPUSeconds),
	}
}
