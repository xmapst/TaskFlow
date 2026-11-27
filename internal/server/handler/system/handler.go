package system

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"math"
	"net/http"
	"time"
	
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	
	"github.com/xmapst/taskflow/api"
	"github.com/xmapst/taskflow/api/httpx"
	"github.com/xmapst/taskflow/api/iproto"
	"github.com/xmapst/taskflow/internal/chanrpc"
)

// Handler implements system endpoints.
type Handler struct{}

// GetSystemConfig 返回已应用的工作池大小及本机资源指标。
// @Summary 获取系统状态
// @Description 返回已生效的工作池大小及 CPU、内存、磁盘指标。容量单位为字节，使用率为百分比，无法读取的指标为 null。实时指标订阅走 /api/v1/ws 的 system 主题。
// @Tags 系统
// @Produce application/json,application/yaml
// @Success 200 {object} api.Response[iproto.SSystemConfig]
// @Failure 503 {string} string "工作池查询不可用或超时"
// @Router /api/v1/system [get]
func (h *Handler) GetSystemConfig(w http.ResponseWriter, r *http.Request) {
	result, err := h.snapshot(r.Context())
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		http.Error(w, "worker pool query unavailable or timed out", http.StatusServiceUnavailable)
		return
	}
	httpx.Send(w, r, api.WithData(result))
}

// RegisterStream exposes the resource metrics as the "system" topic and the
// pool resize as the "system.update" method.
func (h *Handler) RegisterStream(reg *httpx.StreamRegistry) {
	reg.Topic(api.TopicSystem, func(jsontext.Value) (httpx.StreamSession, error) {
		return &httpx.SnapshotSession[iproto.SSystemConfig]{
			Query: func(ctx context.Context) (api.Code, iproto.SSystemConfig, error) {
				result, err := h.snapshot(ctx)
				if err != nil {
					return api.CodeNoData, iproto.SSystemConfig{}, err
				}
				return api.CodeSuccess, result, nil
			},
			Interval: 3 * time.Second,
			Frame: func(snapshot httpx.Snapshot[iproto.SSystemConfig]) (api.StreamFrame, bool) {
				if snapshot.Err != nil {
					return api.StreamFrame{Code: api.CodeNoData, Message: "暂时无法读取工作池状态"}, true
				}
				return api.StreamFrame{Code: snapshot.Code, Data: snapshot.Data}, true
			},
		}, nil
	})
	reg.Method(api.MethodSystemUpdate, h.updatePool)
}

// updatePool resizes the worker pool. A shrink may wait for retiring workers to
// finish their current task, which is why the caller is expected to allow the
// call to take its time.
func (h *Handler) updatePool(ctx context.Context, params jsontext.Value) (any, error) {
	var request struct {
		Size int `json:"size"`
	}
	if err := httpx.DecodeStreamParams(params, &request); err != nil {
		return nil, err
	}
	message := iproto.WorkerPoolMessage{Size: request.Size}
	if err := message.Validate(); err != nil {
		return nil, err
	}
	if err := chanrpc.Submit(ctx, message); err != nil {
		return nil, errors.New("worker pool resize unavailable or already in progress")
	}
	return nil, nil
}

func (h *Handler) snapshot(ctx context.Context) (iproto.SSystemConfig, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	size, err := chanrpc.Call[int](queryCtx, iproto.WorkerPoolSizeQuery{})
	cancel()
	if err != nil {
		return iproto.SSystemConfig{}, err
	}
	result := h.systemMetrics(ctx)
	result.PoolSize = size
	return result, nil
}

func (h *Handler) systemMetrics(ctx context.Context) iproto.SSystemConfig {
	result := iproto.SSystemConfig{Disks: []iproto.SSystemDisk{}}
	if count, err := cpu.CountsWithContext(ctx, true); err == nil && count > 0 {
		result.CPUCores = &count
	}
	if values, err := cpu.PercentWithContext(ctx, 200*time.Millisecond, false); err == nil && len(values) > 0 {
		result.CPUUsagePercent = h.validPercent(values[0])
	}
	if usage, err := mem.VirtualMemoryWithContext(ctx); err == nil && usage.Total != 0 {
		result.MemoryTotalBytes = &usage.Total
		result.MemoryUsedPercent = h.validPercent(usage.UsedPercent)
	}
	partitions, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		result.Disks = nil
		return result
	}
	for _, partition := range partitions {
		if ctx.Err() != nil {
			break
		}
		entry := iproto.SSystemDisk{Device: partition.Device, Mountpoint: partition.Mountpoint, FSType: partition.Fstype}
		if usage, err := disk.UsageWithContext(ctx, partition.Mountpoint); err == nil && usage.Total != 0 {
			entry.TotalBytes = &usage.Total
			entry.UsedBytes = &usage.Used
			entry.UsedPercent = h.validPercent(usage.UsedPercent)
		}
		result.Disks = append(result.Disks, entry)
	}
	return result
}

func (h *Handler) validPercent(value float64) *float64 {
	if math.IsNaN(value) || value < 0 || value > 100 {
		return nil
	}
	return &value
}
