/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// reloadTotal counts hot-reload operations by component and result.
	// result label: "success", "failed", "skipped"
	reloadTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "homeassistant_reload_total",
			Help: "Total number of Home Assistant component reload operations.",
		},
		[]string{"component", "result"},
	)

	// reloadDuration measures how long each reload operation takes.
	reloadDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "homeassistant_reload_duration_seconds",
			Help:    "Duration of Home Assistant component reload operations in seconds.",
			Buckets: []float64{0.5, 1, 2, 5, 10, 15, 30},
		},
		[]string{"component"},
	)

	// reloadRetriesTotal counts extra retry attempts (attempts - 1) per component.
	reloadRetriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "homeassistant_reload_retries_total",
			Help: "Total number of retry attempts during Home Assistant component reload operations.",
		},
		[]string{"component"},
	)

	// addonReconcileTotal counts HomeAssistantAddon reconcile operations.
	// result label: "success" | "failed"
	// profile label: "mosquito" | "mariadb" | "node-red" | "custom"
	addonReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "haaddon_reconcile_total",
			Help: "Total number of HomeAssistantAddon reconcile operations.",
		},
		[]string{"result", "profile"},
	)

	// addonReconcileDuration measures how long each addon reconcile operation takes.
	addonReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "haaddon_reconcile_duration_seconds",
			Help:    "Duration of HomeAssistantAddon reconcile operations in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"profile"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		reloadTotal, reloadDuration, reloadRetriesTotal,
		addonReconcileTotal, addonReconcileDuration,
	)
}
