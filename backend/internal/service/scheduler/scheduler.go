package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dast/internal/alive"
	"dast/internal/k8s"
	"dast/internal/model"
	"dast/internal/policy"
	"dast/internal/redisstream"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Scheduler 把策略部署到 K3s,把任务测活后投递到端口扫描 Stream,并提供 pause/resume/terminate 控制流。
type Scheduler struct {
	DB    *gorm.DB
	K8s   *k8s.Client // 沙箱可空
	Redis *redisstream.Manager
}

// New 构造调度器,k8sClient 可为 nil(允许本地无集群运行;部署相关接口会报错)。
func New(db *gorm.DB, k8sClient *k8s.Client, rm *redisstream.Manager) *Scheduler {
	return &Scheduler{DB: db, K8s: k8sClient, Redis: rm}
}

// ============ 策略部署 ============

// DeployPolicy 根据完整策略 JSON 创建/更新 ConfigMap + Deployment + Consumer Group。
// 控制流 Stream 只通过 XAdd 建立,不创建共享 Consumer Group。
func (s *Scheduler) DeployPolicy(ctx context.Context, p *policy.Policy, policyID uint64) error {
	if err := policy.Validate(p); err != nil {
		return err
	}
	if err := s.EnsurePolicyStreams(ctx, p, policyID); err != nil {
		return err
	}
	if s.K8s == nil {
		return errors.New("k8s client not initialized")
	}
	if err := s.K8s.EnsureNamespace(ctx, p.Namespace); err != nil {
		return fmt.Errorf("ensure ns: %w", err)
	}

	for _, key := range policy.EnabledModules(p) {
		cfg, err := policy.BuildModuleConfig(p, policyID, key)
		if err != nil {
			return err
		}
		content, err := policy.MarshalPodConfig(cfg)
		if err != nil {
			return err
		}
		labels := map[string]string{
			"app.kubernetes.io/name": "dast-scanner",
			"dast/policy-id":         fmt.Sprintf("%d", policyID),
			"dast/module":            key,
			"dast/managed-by":        "dast-scheduler",
		}
		cmName := policy.ConfigMapName(policyID, key)
		depName := policy.DeploymentName(policyID, key)

		if err := s.K8s.CreateOrUpdateConfigMap(ctx, p.Namespace, cmName, content, labels); err != nil {
			return fmt.Errorf("configmap %s: %w", cmName, err)
		}

		mod := p.Modules[key]
		if err := s.K8s.CreateOrUpdateDeployment(ctx, k8s.DeploymentSpec{
			Namespace:     p.Namespace,
			Name:          depName,
			Image:         mod.Image,
			Replicas:      int32(mod.Replicas),
			ConfigMapName: cmName,
			ConfigHash:    policy.HashConfig(content),
			Labels:        labels,
		}); err != nil {
			return fmt.Errorf("deployment %s: %w", depName, err)
		}

		// 业务 Stream Consumer Group 已由 EnsurePolicyStreams 为启用模块提前创建。
	}
	for _, key := range []string{model.ModulePortScan, model.ModuleNmap, model.ModuleNuclei, model.ModuleWeakPass} {
		if moduleEnabled(p, key) {
			continue
		}
		if err := s.K8s.DeleteDeployment(ctx, p.Namespace, policy.DeploymentName(policyID, key)); err != nil {
			return fmt.Errorf("delete disabled deployment %s: %w", key, err)
		}
		if err := s.K8s.DeleteConfigMap(ctx, p.Namespace, policy.ConfigMapName(policyID, key)); err != nil {
			return fmt.Errorf("delete disabled configmap %s: %w", key, err)
		}
	}
	return nil
}

// EnsurePolicyStreams 初始化一个策略的 Redis Stream。只为策略启用的业务模块创建
// Stream 和 Consumer Group;控制流总是创建,保持广播读取模型。
func (s *Scheduler) EnsurePolicyStreams(ctx context.Context, p *policy.Policy, policyID uint64) error {
	if s.Redis == nil {
		return nil
	}
	for _, key := range policy.EnabledModules(p) {
		stream := policy.StreamName(policyID, key)
		group := policy.GroupName(policyID, key)
		if err := s.Redis.EnsureGroup(ctx, stream, group); err != nil {
			return fmt.Errorf("ensure stream/group %s: %w", key, err)
		}
	}
	for _, key := range []string{model.ModulePortScan, model.ModuleNmap, model.ModuleNuclei, model.ModuleWeakPass} {
		if moduleEnabled(p, key) {
			continue
		}
		stream := policy.StreamName(policyID, key)
		if err := s.Redis.DeleteKeys(ctx, stream); err != nil {
			return fmt.Errorf("delete disabled stream %s: %w", key, err)
		}
		if err := s.Redis.DeleteByPattern(ctx, fmt.Sprintf("lock:stream:%s:message:*", stream)); err != nil {
			return fmt.Errorf("delete disabled stream locks %s: %w", key, err)
		}
	}
	control := policy.ControlStreamName(policyID)
	if err := s.Redis.EnsureStream(ctx, control, map[string]interface{}{
		"event_id":   "__init__",
		"task_id":    0,
		"policy_id":  policyID,
		"action":     "init",
		"created_at": time.Now().UnixMilli(),
	}); err != nil {
		return fmt.Errorf("ensure control stream: %w", err)
	}
	return nil
}

// DisablePolicy 缩容至 0(MVP)。如有更激进的释放策略,可改为 DeleteDeployment。
func (s *Scheduler) DisablePolicy(ctx context.Context, p *policy.Policy, policyID uint64) error {
	if s.K8s == nil {
		return errors.New("k8s client not initialized")
	}
	for _, key := range []string{model.ModulePortScan, model.ModuleNmap, model.ModuleNuclei, model.ModuleWeakPass} {
		name := policy.DeploymentName(policyID, key)
		_ = s.K8s.ScaleDeployment(ctx, p.Namespace, name, 0)
	}
	return nil
}

// DeletePolicyResources 删除策略相关 K3s 资源
func (s *Scheduler) DeletePolicyResources(ctx context.Context, p *policy.Policy, policyID uint64) error {
	if s.Redis != nil {
		keys := []string{policy.ControlStreamName(policyID)}
		streams := make([]string, 0, 4)
		for _, key := range []string{model.ModulePortScan, model.ModuleNmap, model.ModuleNuclei, model.ModuleWeakPass} {
			stream := policy.StreamName(policyID, key)
			keys = append(keys, stream)
			streams = append(streams, stream)
		}
		if err := s.Redis.DeleteKeys(ctx, keys...); err != nil {
			return fmt.Errorf("delete redis streams: %w", err)
		}
		for _, stream := range streams {
			if err := s.Redis.DeleteByPattern(ctx, fmt.Sprintf("lock:stream:%s:message:*", stream)); err != nil {
				return fmt.Errorf("delete redis locks for %s: %w", stream, err)
			}
		}
	}
	if s.K8s == nil {
		return nil
	}
	for _, key := range []string{model.ModulePortScan, model.ModuleNmap, model.ModuleNuclei, model.ModuleWeakPass} {
		if err := s.K8s.DeleteDeployment(ctx, p.Namespace, policy.DeploymentName(policyID, key)); err != nil {
			return fmt.Errorf("delete deployment %s: %w", key, err)
		}
		if err := s.K8s.DeleteConfigMap(ctx, p.Namespace, policy.ConfigMapName(policyID, key)); err != nil {
			return fmt.Errorf("delete configmap %s: %w", key, err)
		}
	}
	return nil
}

// ScaleModule 调整某模块 replicas
func (s *Scheduler) ScaleModule(ctx context.Context, namespace string, policyID uint64, moduleKey string, replicas int32) error {
	if s.K8s == nil {
		return errors.New("k8s client not initialized")
	}
	return s.K8s.ScaleDeployment(ctx, namespace, policy.DeploymentName(policyID, moduleKey), replicas)
}

// ============ 任务分发 ============

// SubmitTask 分批投递任务到端口扫描 Stream。
//
//  1. 主机测活,只保留存活 host。
//  2. 按 batchSize <= 10 切片,生成 task_part_name,写入 task_parts_progress。
//  3. XADD 到 portscan Stream,XADD 成功后把 portscan_status = running, status = running。
//
// 任务记录(tasks)由调用方先行创建并传入 task。
func (s *Scheduler) SubmitTask(ctx context.Context, task *model.Task, p *policy.Policy, batchSize int) error {
	if task == nil {
		return errors.New("task is nil")
	}
	if s.Redis == nil {
		return errors.New("redis not initialized: cannot submit task")
	}
	if batchSize <= 0 || batchSize > 10 {
		batchSize = 10
	}

	// 原始目标列表(去重/去空)
	var rawTargets []string
	if task.TargetsJSON != "" {
		var t []string
		if err := json.Unmarshal([]byte(task.TargetsJSON), &t); err == nil {
			rawTargets = dedup(t)
		}
	}
	task.TargetTotal = len(rawTargets)

	// 主机测活
	results := alive.CheckAll(ctx, rawTargets, 32)
	aliveHosts := make([]string, 0, len(results))
	for _, r := range results {
		if r.Alive {
			aliveHosts = append(aliveHosts, r.Target)
		}
	}
	task.AliveTotal = len(aliveHosts)
	s.recordEvent(ctx, task.ID, "info", "scheduler",
		fmt.Sprintf("alive check completed: alive=%d total=%d", task.AliveTotal, task.TargetTotal),
		map[string]interface{}{"alive_total": task.AliveTotal, "target_total": task.TargetTotal})

	// 按 batchSize 分片
	parts := chunk(aliveHosts, batchSize)
	task.PartTotal = len(parts)
	now := time.Now()
	task.StartedAt = &now
	if stopped, err := s.taskTerminated(ctx, task.ID); err != nil {
		return err
	} else if stopped {
		s.recordEvent(ctx, task.ID, "info", "scheduler", "task submit skipped: terminated before dispatch", nil)
		return nil
	}
	if len(parts) == 0 {
		task.Status = model.TaskStatusCompleted
		task.FinishedAt = &now
		if err := s.DB.WithContext(ctx).Save(task).Error; err != nil {
			return err
		}
		s.recordEvent(ctx, task.ID, "info", "scheduler", "task completed: no alive hosts", nil)
		return nil
	}
	task.Status = model.TaskStatusRunning

	if err := s.DB.WithContext(ctx).Save(task).Error; err != nil {
		return err
	}

	// 写入 task_parts_progress
	rows := make([]model.TaskPartsProgress, 0, len(parts))
	for idx, batch := range parts {
		hostsJSON, _ := json.Marshal(batch)
		row := model.TaskPartsProgress{
			TaskID:       task.ID,
			TaskPartName: fmt.Sprintf("task-%d-part-%06d", task.ID, idx+1),
			PartIndex:    idx + 1,
			HostsJSON:    string(hostsJSON),
			HostCount:    len(batch),
			Status:       model.PartStatusPending,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		// 按策略启用模块初始化 module 状态
		initModuleStatus(&row, p)
		rows = append(rows, row)
	}
	if len(rows) > 0 {
		if err := s.DB.WithContext(ctx).CreateInBatches(rows, 50).Error; err != nil {
			return err
		}
	}

	// 把每个 part 投到端口扫描 Stream;XADD 成功后改 portscan_status = running, status = running
	streamPort := policy.StreamName(task.PolicyID, model.ModulePortScan)
	for _, row := range rows {
		if stopped, err := s.taskTerminated(ctx, task.ID); err != nil {
			return err
		} else if stopped {
			s.recordEvent(ctx, task.ID, "info", "scheduler", "task dispatch stopped: terminated", map[string]interface{}{"task_part_name": row.TaskPartName})
			return nil
		}
		bm := redisstream.BusinessMessage{
			TaskID:       task.ID,
			TaskPartName: row.TaskPartName,
			Hosts:        nil,
		}
		_ = json.Unmarshal([]byte(row.HostsJSON), &bm.Hosts)
		if _, err := s.Redis.XAddBusiness(ctx, streamPort, bm); err != nil {
			return fmt.Errorf("xadd portscan: %w", err)
		}
		running := model.PartStatusRunning
		updates := map[string]interface{}{
			"status":          model.PartStatusRunning,
			"portscan_status": &running,
			"updated_at":      time.Now(),
		}
		if err := s.DB.WithContext(ctx).Model(&model.TaskPartsProgress{}).
			Where("task_id = ? AND task_part_name = ?", task.ID, row.TaskPartName).
			Updates(updates).Error; err != nil {
			return err
		}
	}
	s.recordEvent(ctx, task.ID, "info", "scheduler",
		fmt.Sprintf("dispatched portscan parts: parts=%d batch_size=%d", len(rows), batchSize),
		map[string]interface{}{"part_total": len(rows), "batch_size": batchSize})
	return nil
}

func initModuleStatus(row *model.TaskPartsProgress, p *policy.Policy) {
	pending := model.PartStatusPending
	if moduleEnabled(p, model.ModulePortScan) {
		row.PortScanStatus = &pending
	}
	if moduleEnabled(p, model.ModuleNmap) {
		s := pending
		row.NmapStatus = &s
	}
	if moduleEnabled(p, model.ModuleNuclei) {
		s := pending
		row.NucleiStatus = &s
	}
	if moduleEnabled(p, model.ModuleWeakPass) {
		s := pending
		row.WeakPassStatus = &s
	}
}

func moduleEnabled(p *policy.Policy, key string) bool {
	if m, ok := p.Modules[key]; ok && m != nil {
		return m.Enabled
	}
	return false
}

func (s *Scheduler) taskTerminated(ctx context.Context, taskID uint64) (bool, error) {
	var status string
	if err := s.DB.WithContext(ctx).Model(&model.Task{}).
		Where("id = ?", taskID).
		Select("status").
		Scan(&status).Error; err != nil {
		return false, err
	}
	return status == model.TaskStatusTerminated, nil
}

// ============ 控制流 ============

// PublishControl 把控制消息 XADD 到策略控制流,所有该策略下 Pod 通过广播读取。
func (s *Scheduler) PublishControl(ctx context.Context, policyID, taskID uint64, action string) error {
	return s.publishControl(ctx, policyID, taskID, action)
}

func (s *Scheduler) publishControl(ctx context.Context, policyID, taskID uint64, action string) error {
	if action != "pause" && action != "resume" && action != "terminate" {
		return fmt.Errorf("invalid action: %s", action)
	}
	if s.Redis == nil {
		return errors.New("redis not initialized: cannot publish control")
	}
	msg := redisstream.ControlMessage{
		EventID:   uuid.NewString(),
		TaskID:    taskID,
		PolicyID:  policyID,
		Action:    action,
		CreatedAt: time.Now().UnixMilli(),
	}
	_, err := s.Redis.XAddControl(ctx, policy.ControlStreamName(policyID), msg)
	return err
}

func (s *Scheduler) recordEvent(ctx context.Context, taskID uint64, level, module, msg string, meta interface{}) {
	if s.DB == nil || taskID == 0 {
		return
	}
	metaJSON := ""
	if meta != nil {
		b, _ := json.Marshal(meta)
		metaJSON = string(b)
	}
	_ = s.DB.WithContext(ctx).Create(&model.TaskEvent{
		TaskID: taskID, Level: level, Module: module, Message: msg, MetaJSON: metaJSON,
	}).Error
}

// ============ 工具 ============

func dedup(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, x := range in {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}

func chunk(in []string, size int) [][]string {
	if len(in) == 0 {
		return nil
	}
	out := make([][]string, 0, (len(in)+size-1)/size)
	for i := 0; i < len(in); i += size {
		end := i + size
		if end > len(in) {
			end = len(in)
		}
		out = append(out, in[i:end])
	}
	return out
}
