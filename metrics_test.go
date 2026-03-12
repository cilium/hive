// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package hive_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/cilium/hive"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
)

type MockMetrics struct {
	mock.Mock
}

func (m *MockMetrics) StartDuration(duration time.Duration) {
	m.Called(duration)
}

func (m *MockMetrics) StopDuration(duration time.Duration) {
	m.Called(duration)
}

func (m *MockMetrics) PopulateDuration(duration time.Duration) {
	m.Called(duration)
}

var _ hive.Metrics = &MockMetrics{}

func TestMetrics(t *testing.T) {
	mockMetrics := new(MockMetrics)

	mockMetrics.On("StartDuration", mock.Anything).Return().Once()
	mockMetrics.On("PopulateDuration", mock.Anything).Return().Once()
	mockMetrics.On("StopDuration", mock.Anything).Return().Once()

	log := hivetest.Logger(t)
	h := hive.New(cell.Provide(func(m *MockMetrics) hive.Metrics {
		return m
	}), cell.Provide(func() *MockMetrics {
		return mockMetrics
	}))

	h.Shutdown()

	err := h.Run(log)
	assert.NoError(t, err, "expected Run to succeed")

	mockMetrics.AssertExpectations(t)
}
