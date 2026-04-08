// Package system provides system-level utilities for resource monitoring and control.
package system

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MemStatus represents the current system memory status.
type MemStatus struct {
	Total     uint64  // Total RAM in KB
	Available uint64  // Available RAM in KB (includes cache/buffers)
	Used      uint64  // Used RAM in KB
	Percent   float64 // Used percentage
}

// MemThrottler monitors system memory and suggests when to pause heavy tasks.
type MemThrottler struct {
	thresholdPercent float64
	minAvailableMB   uint64
	mu               sync.RWMutex
	lastStatus       MemStatus
	stopChan         chan struct{}
}

// NewMemThrottler creates a new throttler with given thresholds.
// thresholdPercent: Pause if used memory > this (e.g., 90.0)
// minAvailableMB: Pause if available memory < this (e.g., 512)
func NewMemThrottler(thresholdPercent float64, minAvailableMB uint64) *MemThrottler {
	mt := &MemThrottler{
		thresholdPercent: thresholdPercent,
		minAvailableMB:   minAvailableMB,
		stopChan:         make(chan struct{}),
	}
	// Initial update
	mt.update()
	// Start background monitoring
	go mt.monitor()
	return mt
}

func (mt *MemThrottler) monitor() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			mt.update()
		case <-mt.stopChan:
			return
		}
	}
}

func (mt *MemThrottler) update() {
	status, err := readMemInfo()
	if err != nil {
		return
	}
	mt.mu.Lock()
	mt.lastStatus = status
	mt.mu.Unlock()
}

// Stop halts the memory monitoring and throttling goroutine.
func (mt *MemThrottler) Stop() {
	close(mt.stopChan)
}

// CanStartLSP returns true if the system has enough RAM to safely start an LSP process.
// It checks if available memory is at least 10% of total memory.
func (mt *MemThrottler) CanStartLSP() bool {
	mt.mu.RLock()
	defer mt.mu.RUnlock()

	// Threshold: At least 10% available
	if mt.lastStatus.Available < mt.lastStatus.Total/10 {
		return false
	}

	return true
}

// ShouldThrottle returns true if current memory usage exceeds thresholds.
func (mt *MemThrottler) ShouldThrottle() bool {
	mt.mu.RLock()
	defer mt.mu.RUnlock()

	// 1. Check percentage
	if mt.lastStatus.Percent > mt.thresholdPercent {
		return true
	}

	// 2. Check absolute available
	if mt.lastStatus.Available/1024 < mt.minAvailableMB {
		return true
	}

	return false
}

// GetStatus returns the latest memory snapshot.
func (mt *MemThrottler) GetStatus() MemStatus {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	return mt.lastStatus
}

// readMemInfo parses /proc/meminfo for Linux systems.
func readMemInfo() (MemStatus, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return MemStatus{}, err
	}
	defer func() { _ = file.Close() }()

	var total, available uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		val, _ := strconv.ParseUint(parts[1], 10, 64)
		switch parts[0] {
		case "MemTotal:":
			total = val
		case "MemAvailable:":
			available = val
		}
	}

	if total == 0 {
		return MemStatus{}, fmt.Errorf("failed to read MemTotal")
	}

	used := total - available
	percent := (float64(used) / float64(total)) * 100

	return MemStatus{
		Total:     total,
		Available: available,
		Used:      used,
		Percent:   percent,
	}, nil
}
