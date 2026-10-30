package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	requestsTotal    *prometheus.CounterVec
	requestDuration  *prometheus.HistogramVec
	requestsInFlight prometheus.Gauge
}

func NewMetrics(registry *prometheus.Registry) *Metrics {
	m := &Metrics{
		requestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "trailnest",
				Name:      "http_requests_total",
				Help:      "Total number of HTTP requests.",
			},
			[]string{"method", "route", "status"},
		),

		requestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "trailnest",
				Name:      "http_request_duration_seconds",
				Help:      "Duration of HTTP requests in seconds.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"method", "route", "status"},
		),

		requestsInFlight: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: "trailnest",
				Name:      "http_requests_in_flight",
				Help:      "Number of HTTP requests currently being processed.",
			},
		),
	}

	registry.MustRegister(
		m.requestsTotal,
		m.requestDuration,
		m.requestsInFlight,
	)

	return m
}

func (m *Metrics) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.FullPath() == "/metrics" {
			c.Next()
			return
		}

		start := time.Now()

		m.requestsInFlight.Inc()
		defer m.requestsInFlight.Dec()

		c.Next()

		duration := time.Since(start).Seconds()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}

		method := c.Request.Method
		status := strconv.Itoa(c.Writer.Status())

		m.requestsTotal.WithLabelValues(
			method,
			route,
			status,
		).Inc()

		m.requestDuration.WithLabelValues(
			method,
			route,
			status,
		).Observe(duration)
	}
}
