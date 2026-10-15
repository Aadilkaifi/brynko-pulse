// Copyright 2026 Brynko Technologies
// SPDX-License-Identifier: AGPL-3.0-only

package monitor

import (
	"context"
	"fmt"
	"sync"

	"github.com/Aadilkaifi/brynko-pulse/internal/platform"
	"github.com/Aadilkaifi/brynko-pulse/internal/platform/host"
	"github.com/Aadilkaifi/brynko-pulse/internal/rule"
)

type disk struct {
	p    platform.Platform
	path string
}

func (c *disk) Check(context.Context) (rule.Values, error) {
	d, err := c.p.DiskUsage(c.path)
	if err != nil {
		return nil, err
	}
	v := rule.Values{
		// Like df: used against what ordinary users can use, so reserved blocks count as full.
		"used_percent": percent(d.UsedBytes, d.UsedBytes+d.FreeBytes),
		"free_bytes":   float64(d.FreeBytes),
		"total_bytes":  float64(d.TotalBytes),
		"free_gb":      float64(d.FreeBytes) / (1 << 30),
	}
	if d.HasInodeDetails {
		v["inodes_used_percent"] = percent(d.InodesTotal-d.InodesFree, d.InodesTotal)
	}
	return v, nil
}

type memory struct{ p platform.Platform }

func (c *memory) Check(context.Context) (rule.Values, error) {
	m, err := c.p.Memory()
	if err != nil {
		return nil, err
	}
	return rule.Values{
		"used_percent":    percent(m.TotalBytes-m.AvailableBytes, m.TotalBytes),
		"available_bytes": float64(m.AvailableBytes),
		"available_gb":    float64(m.AvailableBytes) / (1 << 30),
		"total_bytes":     float64(m.TotalBytes),
	}, nil
}

type swap struct{ p platform.Platform }

func (c *swap) Check(context.Context) (rule.Values, error) {
	m, err := c.p.Memory()
	if err != nil {
		return nil, err
	}
	return rule.Values{
		"used_percent": percent(m.SwapTotalBytes-m.SwapFreeBytes, m.SwapTotalBytes),
		"used_bytes":   float64(m.SwapTotalBytes - m.SwapFreeBytes),
		"total_bytes":  float64(m.SwapTotalBytes),
	}, nil
}

// cpu reports usage between this check and the previous one.
type cpu struct {
	p    platform.Platform
	mu   sync.Mutex
	prev *host.CPUTimes
}

func (c *cpu) Check(context.Context) (rule.Values, error) {
	now, err := c.p.CPUTimes()
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prev := c.prev
	c.prev = &now
	if prev == nil || now.Total <= prev.Total {
		return nil, ErrNotReady
	}
	total := now.Total - prev.Total
	idle := now.Idle - prev.Idle
	if idle > total {
		idle = total
	}
	return rule.Values{"used_percent": percent(total-idle, total)}, nil
}

type load struct{ p platform.Platform }

func (c *load) Check(context.Context) (rule.Values, error) {
	l, err := c.p.LoadAverage()
	if err != nil {
		return nil, err
	}
	v := rule.Values{"load1": l.Load1, "load5": l.Load5, "load15": l.Load15}
	if l.CPUs > 0 {
		v["per_cpu"] = l.Load1 / float64(l.CPUs)
	}
	return v, nil
}

type service struct {
	p      platform.Platform
	name   string
	events host.ServiceWatcher
}

func (c *service) Check(ctx context.Context) (rule.Values, error) {
	s, err := c.p.Service(ctx, c.name)
	if err != nil {
		return nil, err
	}
	v := rule.Values{"active": s.Active, "state": s.State}
	if s.Restarts >= 0 {
		v["restarts"] = float64(s.Restarts)
	}
	return v, nil
}

type process struct {
	p     platform.Platform
	match string
}

func (c *process) Check(context.Context) (rule.Values, error) {
	n, err := c.p.ProcessCount(c.match)
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	return rule.Values{"count": float64(n)}, nil
}
