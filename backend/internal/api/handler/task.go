package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"dast/internal/model"
	"dast/internal/policy"
	"dast/internal/redisstream"
	"dast/internal/service/scheduler"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// TaskHandler 任务相关接口
type TaskHandler struct {
	DB    *gorm.DB
	Sched *scheduler.Scheduler
}

type createTaskReq struct {
	Name     string   `json:"name" binding:"required"`
	PolicyID uint64   `json:"policy_id" binding:"required"`
	Targets  []string `json:"targets" binding:"required"`
}

// Create 新建任务并提交到端口扫描 Stream
func (h *TaskHandler) Create(c *gin.Context) {
	var req createTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Targets) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "targets required"})
		return
	}
	cleaned := make([]string, 0, len(req.Targets))
	for _, t := range req.Targets {
		for _, item := range splitTargetInput(t) {
			if strings.Contains(item, ":") {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("host:port not supported: %s", item)})
				return
			}
			cleaned = append(cleaned, item)
		}
	}
	if len(cleaned) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no valid targets"})
		return
	}

	var pRow model.Policy
	if err := h.DB.First(&pRow, req.PolicyID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "policy not found"})
		return
	}
	if !pRow.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "policy not enabled"})
		return
	}
	p, err := policy.ParseFromJSON(pRow.ConfigJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := policy.Validate(p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	targetsJSON, _ := json.Marshal(cleaned)
	task := model.Task{
		Name:        req.Name,
		PolicyID:    req.PolicyID,
		Status:      model.TaskStatusQueued,
		TargetsJSON: string(targetsJSON),
		SubmittedBy: "api",
	}
	if err := h.DB.Create(&task).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	go func(t model.Task) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if err := h.Sched.SubmitTask(ctx, &t, p, p.Runtime.TargetBatchSize); err != nil {
			h.recordEvent(t.ID, "error", "scheduler", err.Error(), nil)
			h.DB.Model(&model.Task{}).Where("id = ?", t.ID).Updates(map[string]interface{}{
				"status": model.TaskStatusFailed,
			})
		}
	}(task)

	c.JSON(http.StatusCreated, gin.H{"id": task.ID})
}

// List 任务分页:每页 10 条
func (h *TaskHandler) List(c *gin.Context) {
	page := atoiDefault(c.DefaultQuery("page", "1"), 1)
	size := atoiDefault(c.DefaultQuery("page_size", "10"), 10)
	if size <= 0 || size > 100 {
		size = 10
	}
	status := c.Query("status")
	q := h.DB.Model(&model.Task{}).Order("id desc")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var total int64
	q.Count(&total)
	var rows []model.Task
	q.Offset((page - 1) * size).Limit(size).Find(&rows)
	c.JSON(http.StatusOK, gin.H{"items": rows, "total": total, "page": page, "page_size": size})
}

// Get 任务详情 + 进度聚合
func (h *TaskHandler) Get(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	var t model.Task
	if err := h.DB.First(&t, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	progress := h.summarizeProgress(id)
	c.JSON(http.StatusOK, gin.H{"task": t, "progress": progress, "alive_hosts": h.aliveHosts(id)})
}

// Pause 暂停
func (h *TaskHandler) Pause(c *gin.Context) {
	h.control(c, "pause", model.TaskStatusPaused, []string{model.TaskStatusRunning})
}

// Resume 继续
func (h *TaskHandler) Resume(c *gin.Context) {
	h.control(c, "resume", model.TaskStatusRunning, []string{model.TaskStatusPaused})
}

// Terminate 终止
func (h *TaskHandler) Terminate(c *gin.Context) {
	h.control(c, "terminate", model.TaskStatusTerminated, []string{
		model.TaskStatusRunning, model.TaskStatusPaused, model.TaskStatusQueued, model.TaskStatusRestarting,
	})
}

// Delete 删除已结束任务及其任务级结果数据。运行中任务必须先终止或等待结束。
func (h *TaskHandler) Delete(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	var t model.Task
	if err := h.DB.First(&t, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if isActiveTaskStatus(t.Status) {
		c.JSON(http.StatusConflict, gin.H{"error": "task is active; terminate it before delete"})
		return
	}
	if err := h.deleteTaskRows(t.ID, &t); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Restart 重启已结束任务。completed/failed 复用原 task_id; terminated 新建任务获取新的自增 task_id。
func (h *TaskHandler) Restart(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	var t model.Task
	if err := h.DB.First(&t, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if t.Status != model.TaskStatusCompleted && t.Status != model.TaskStatusFailed && t.Status != model.TaskStatusTerminated {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task not restartable"})
		return
	}
	var pRow model.Policy
	if err := h.DB.First(&pRow, t.PolicyID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if pRow.CreatedAt.After(t.CreatedAt) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task policy was deleted and recreated; historical task cannot restart"})
		return
	}
	p, err := policy.ParseFromJSON(pRow.ConfigJSON)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := policy.Validate(p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if t.Status == model.TaskStatusTerminated {
		newTask := model.Task{
			Name:        t.Name,
			PolicyID:    t.PolicyID,
			Status:      model.TaskStatusQueued,
			TargetsJSON: t.TargetsJSON,
			SubmittedBy: t.SubmittedBy,
		}
		if err := h.DB.Create(&newTask).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		h.recordEvent(t.ID, "info", "task", fmt.Sprintf("terminated task restarted as new task: task_id=%d", newTask.ID), map[string]interface{}{"new_task_id": newTask.ID})
		h.recordEvent(newTask.ID, "info", "task", fmt.Sprintf("task created from terminated task: source_task_id=%d", t.ID), map[string]interface{}{"source_task_id": t.ID})

		go func(task model.Task) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if err := h.Sched.SubmitTask(ctx, &task, p, p.Runtime.TargetBatchSize); err != nil {
				h.recordEvent(task.ID, "error", "scheduler", err.Error(), nil)
				h.DB.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]interface{}{
					"status": model.TaskStatusFailed,
				})
			}
		}(newTask)

		c.JSON(http.StatusCreated, gin.H{"ok": true, "id": newTask.ID, "new_task_id": newTask.ID})
		return
	}

	now := time.Now()
	if err := h.DB.Model(&t).Updates(map[string]interface{}{
		"status":      model.TaskStatusRestarting,
		"started_at":  now,
		"finished_at": nil,
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.recordEvent(t.ID, "info", "task", "task restart requested", nil)

	if err := h.resetPartsForRestart(t.ID, p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	go func(task model.Task) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		dispatched, err := h.republishParts(ctx, &task, p)
		if err != nil {
			h.recordEvent(task.ID, "error", "scheduler", err.Error(), nil)
			h.DB.Model(&model.Task{}).Where("id = ?", task.ID).Update("status", model.TaskStatusFailed)
			return
		}
		if dispatched == 0 {
			now := time.Now()
			h.DB.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]interface{}{
				"status":      model.TaskStatusCompleted,
				"finished_at": now,
			})
			return
		}
		h.recordEvent(task.ID, "info", "scheduler", fmt.Sprintf("restart dispatched parts: parts=%d", dispatched), map[string]interface{}{"part_total": dispatched})
		h.DB.Model(&model.Task{}).Where("id = ?", task.ID).Update("status", model.TaskStatusRunning)
	}(t)

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Events 事件流
func (h *TaskHandler) Events(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	var rows []model.TaskEvent
	h.DB.Where("task_id = ?", id).Order("id asc").Find(&rows)
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

// PortResults 端口结果
func (h *TaskHandler) PortResults(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	var rows []model.PortResult
	h.DB.Where("task_id = ?", id).Order("id asc").Find(&rows)
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

// ServiceResults 服务识别结果
func (h *TaskHandler) ServiceResults(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	var rows []model.ServiceResult
	h.DB.Where("task_id = ?", id).Order("id asc").Find(&rows)
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

// Vulnerabilities 漏洞结果
func (h *TaskHandler) Vulnerabilities(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	var rows []model.Vulnerability
	h.DB.Where("task_id = ?", id).Order("id asc").Find(&rows)
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

// WeakPasswords 弱口令结果
func (h *TaskHandler) WeakPasswords(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	var rows []model.WeakPasswordFinding
	h.DB.Where("task_id = ?", id).Order("id asc").Find(&rows)
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

// Parts 分片进度
func (h *TaskHandler) Parts(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	var rows []model.TaskPartsProgress
	h.DB.Where("task_id = ?", id).Order("part_index asc").Find(&rows)
	c.JSON(http.StatusOK, gin.H{"items": rows})
}

// ============ 内部 ============

func (h *TaskHandler) control(c *gin.Context, action, target string, allowed []string) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	var t model.Task
	if err := h.DB.First(&t, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	allow := false
	for _, s := range allowed {
		if t.Status == s {
			allow = true
			break
		}
	}
	if !allow {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid state for action"})
		return
	}
	if h.Sched != nil {
		if err := h.Sched.PublishControl(c.Request.Context(), t.PolicyID, t.ID, action); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	now := time.Now()
	updates := map[string]interface{}{"status": target}
	if target == model.TaskStatusTerminated {
		updates["finished_at"] = now
	}
	if err := h.DB.Model(&t).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.recordEvent(t.ID, "info", "task", controlEventMessage(action), nil)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *TaskHandler) summarizeProgress(taskID uint64) gin.H {
	var (
		partTotal     int64
		partCompleted int64
		portDone      int64
		serviceDone   int64
		nucleiDone    int64
		weakpassDone  int64
		vulnTotal     int64
		weakpassTotal int64
		portOpenTotal int64
		serviceTotal  int64
	)
	h.DB.Model(&model.TaskPartsProgress{}).Where("task_id = ?", taskID).Count(&partTotal)
	h.DB.Model(&model.TaskPartsProgress{}).Where("task_id = ? AND status = ?", taskID, model.PartStatusCompleted).Count(&partCompleted)
	h.DB.Model(&model.TaskPartsProgress{}).Where("task_id = ? AND portscan_status = ?", taskID, model.PartStatusCompleted).Count(&portDone)
	h.DB.Model(&model.TaskPartsProgress{}).Where("task_id = ? AND nmap_status = ?", taskID, model.PartStatusCompleted).Count(&serviceDone)
	h.DB.Model(&model.TaskPartsProgress{}).Where("task_id = ? AND nuclei_status = ?", taskID, model.PartStatusCompleted).Count(&nucleiDone)
	h.DB.Model(&model.TaskPartsProgress{}).Where("task_id = ? AND weakpass_status = ?", taskID, model.PartStatusCompleted).Count(&weakpassDone)
	h.DB.Model(&model.PortResult{}).Where("task_id = ?", taskID).Count(&portOpenTotal)
	h.DB.Model(&model.ServiceResult{}).Where("task_id = ?", taskID).Count(&serviceTotal)
	h.DB.Model(&model.Vulnerability{}).Where("task_id = ?", taskID).Count(&vulnTotal)
	h.DB.Model(&model.WeakPasswordFinding{}).Where("task_id = ?", taskID).Count(&weakpassTotal)
	return gin.H{
		"part_total":              partTotal,
		"part_completed":          partCompleted,
		"portscan_part_completed": portDone,
		"nmap_part_completed":     serviceDone,
		"nuclei_part_completed":   nucleiDone,
		"weakpass_part_completed": weakpassDone,
		"port_open_total":         portOpenTotal,
		"service_total":           serviceTotal,
		"vulnerability_total":     vulnTotal,
		"weak_password_total":     weakpassTotal,
	}
}

func (h *TaskHandler) aliveHosts(taskID uint64) []string {
	var rows []model.TaskPartsProgress
	if err := h.DB.Where("task_id = ?", taskID).Order("part_index asc").Find(&rows).Error; err != nil {
		return []string{}
	}
	out := []string{}
	for _, row := range rows {
		var hosts []string
		if err := json.Unmarshal([]byte(row.HostsJSON), &hosts); err != nil {
			continue
		}
		out = append(out, hosts...)
	}
	return out
}

func (h *TaskHandler) resetPartsForRestart(taskID uint64, p *policy.Policy) error {
	pending := model.PartStatusPending
	return h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("task_id = ?", taskID).Delete(&model.PortResult{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id = ?", taskID).Delete(&model.ServiceResult{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id = ?", taskID).Delete(&model.Vulnerability{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id = ?", taskID).Delete(&model.WeakPasswordFinding{}).Error; err != nil {
			return err
		}

		var rows []model.TaskPartsProgress
		if err := tx.Where("task_id = ?", taskID).Find(&rows).Error; err != nil {
			return err
		}
		for _, r := range rows {
			updates := map[string]interface{}{
				"status":          model.PartStatusPending,
				"error":           "",
				"completed_at":    nil,
				"portscan_status": setIfEnabled(p, model.ModulePortScan, &pending),
				"nmap_status":     setIfEnabled(p, model.ModuleNmap, &pending),
				"nuclei_status":   setIfEnabled(p, model.ModuleNuclei, &pending),
				"weakpass_status": setIfEnabled(p, model.ModuleWeakPass, &pending),
			}
			if err := tx.Model(&model.TaskPartsProgress{}).Where("id = ?", r.ID).Updates(updates).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func setIfEnabled(p *policy.Policy, key string, val *string) interface{} {
	if m, ok := p.Modules[key]; ok && m != nil && m.Enabled {
		return val
	}
	return gorm.Expr("NULL")
}

func (h *TaskHandler) republishParts(ctx context.Context, t *model.Task, p *policy.Policy) (int, error) {
	if h.Sched == nil {
		return 0, errors.New("scheduler not initialized")
	}
	if h.Sched.Redis == nil {
		return 0, errors.New("redis not initialized: cannot republish task")
	}
	var rows []model.TaskPartsProgress
	if err := h.DB.Where("task_id = ?", t.ID).Find(&rows).Error; err != nil {
		return 0, err
	}
	streamPort := policy.StreamName(t.PolicyID, model.ModulePortScan)
	dispatched := 0
	for _, r := range rows {
		var hosts []string
		_ = json.Unmarshal([]byte(r.HostsJSON), &hosts)
		if len(hosts) == 0 {
			continue
		}
		bm := redisstream.BusinessMessage{TaskID: t.ID, TaskPartName: r.TaskPartName, Hosts: hosts}
		if _, err := h.Sched.Redis.XAddBusiness(ctx, streamPort, bm); err != nil {
			return dispatched, err
		}
		running := model.PartStatusRunning
		if err := h.DB.Model(&model.TaskPartsProgress{}).
			Where("task_id = ? AND task_part_name = ?", t.ID, r.TaskPartName).
			Updates(map[string]interface{}{
				"status":          model.PartStatusRunning,
				"portscan_status": &running,
			}).Error; err != nil {
			return dispatched, err
		}
		dispatched++
	}
	return dispatched, nil
}

func (h *TaskHandler) recordEvent(taskID uint64, level, module, msg string, meta interface{}) {
	metaJSON := ""
	if meta != nil {
		b, _ := json.Marshal(meta)
		metaJSON = string(b)
	}
	h.DB.Create(&model.TaskEvent{
		TaskID: taskID, Level: level, Module: module, Message: msg, MetaJSON: metaJSON,
	})
}

func controlEventMessage(action string) string {
	switch action {
	case "pause":
		return "task paused"
	case "resume":
		return "task resumed"
	case "terminate":
		return "task terminated"
	default:
		return "task control: " + action
	}
}

func (h *TaskHandler) deleteTaskRows(taskID uint64, t *model.Task) error {
	return h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("task_id = ?", taskID).Delete(&model.TaskPartsProgress{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id = ?", taskID).Delete(&model.TaskEvent{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id = ?", taskID).Delete(&model.PortResult{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id = ?", taskID).Delete(&model.ServiceResult{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id = ?", taskID).Delete(&model.Vulnerability{}).Error; err != nil {
			return err
		}
		if err := tx.Where("task_id = ?", taskID).Delete(&model.WeakPasswordFinding{}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Delete(t).Error
	})
}

func isActiveTaskStatus(status string) bool {
	switch status {
	case model.TaskStatusQueued, model.TaskStatusRunning, model.TaskStatusPaused, model.TaskStatusRestarting:
		return true
	default:
		return false
	}
}

func atoiDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func splitTargetInput(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}
