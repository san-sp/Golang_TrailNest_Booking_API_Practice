package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func TestMetricsMiddlewareRecordsRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	router := gin.New()
	router.Use(metrics.Middleware())
	router.GET("/healthz", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)

	expected := `
		# HELP trailnest_http_requests_total Total number of HTTP requests.
		# TYPE trailnest_http_requests_total counter
		trailnest_http_requests_total{method="GET",route="/healthz",status="200"} 1
	`

	require.NoError(t, testutil.CollectAndCompare(
		metrics.requestsTotal,
		strings.NewReader(expected),
		"trailnest_http_requests_total",
	))

	require.Equal(
		t,
		float64(0),
		testutil.ToFloat64(metrics.requestsInFlight),
	)

	metricFamilies, err := registry.Gather()
	require.NoError(t, err)

	var histogramCount uint64

	for _, family := range metricFamilies {
		if family.GetName() != "trailnest_http_request_duration_seconds" {
			continue
		}

		for _, metric := range family.GetMetric() {
			histogramCount += metric.GetHistogram().GetSampleCount()
		}
	}

	require.Equal(t, uint64(1), histogramCount)
}

func TestMetricsMiddlewareExcludesMetricsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	router := gin.New()
	router.Use(metrics.Middleware())

	router.GET("/metrics", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)

	require.Equal(
		t,
		float64(0),
		testutil.ToFloat64(metrics.requestsInFlight),
	)

	require.NoError(t, testutil.CollectAndCompare(
		metrics.requestsTotal,
		strings.NewReader(""),
		"trailnest_http_requests_total",
	))

	metricFamilies, err := registry.Gather()
	require.NoError(t, err)

	for _, family := range metricFamilies {
		require.NotEqual(
			t,
			"trailnest_http_request_duration_seconds",
			family.GetName(),
		)
	}
}

func TestMetricsMiddlewareRecordsErrorStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)

	router := gin.New()
	router.Use(metrics.Middleware())

	router.GET("/missing", func(c *gin.Context) {
		c.Status(http.StatusNotFound)
	})

	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusNotFound, response.Code)

	expected := `
        # HELP trailnest_http_requests_total Total number of HTTP requests.
        # TYPE trailnest_http_requests_total counter
        trailnest_http_requests_total{method="GET",route="/missing",status="404"} 1
    `

	require.NoError(t, testutil.CollectAndCompare(
		metrics.requestsTotal,
		strings.NewReader(expected),
		"trailnest_http_requests_total",
	))
}
