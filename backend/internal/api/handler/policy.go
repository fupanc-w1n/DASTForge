package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"dast/internal/config"
	"dast/internal/k8s"
	"dast/internal/model"
	"dast/internal/policy"
	"dast/internal/service/scheduler"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// PolicyHandler 策略 CRUD + enable/disable/deploy/scale + runtime 查询
type PolicyHandler struct {
	DB        *gorm.DB
	Sched     *scheduler.Scheduler
	K8s       *k8s.Client
	Namespace string
	Cfg       *config.Config // 用于 DefaultTemplate 时预填 scheduler.internal_ip 等
}

// DefaultTemplate 返回默认策略模板。如果配置了 DAST_SCHEDULER_IP / DAST_K8S_NAMESPACE 等,
// 用它们覆盖模板里的对应字段,前端"新建策略"表单一打开就是合理预设值。
func (h *PolicyHandler) DefaultTemplate(c *gin.Context) {
	p := policy.DefaultPolicy()
	if h.Cfg != nil {
		if h.Cfg.SchedulerInternalIP != "" {
			p.Scheduler.InternalIP = h.Cfg.SchedulerInternalIP
		}
		if h.Cfg.SchedulerRedisPort > 0 {
			p.Scheduler.RedisPort = h.Cfg.SchedulerRedisPort
		}
		if h.Cfg.SchedulerMySQLPort > 0 {
			p.Scheduler.MySQLPort = h.Cfg.SchedulerMySQLPort
		}
		if h.Cfg.K8sNamespace != "" {
			p.Namespace = h.Cfg.K8sNamespace
		}
	}
	c.JSON(http.StatusOK, p)
}

// List 策略分页(简化:全部列出)
func (h *PolicyHandler) List(c *gin.Context) {
	var rows []model.Policy
	if err := h.DB.Order("id desc").Find(&rows).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type item struct {
		model.Policy
		EnabledModules []string `json:"enabled_modules"`
	}
	out := make([]item, 0, len(rows))
	for _, r := range rows {
		p, err := policy.ParseFromJSON(r.ConfigJSON)
		mods := []string{}
		if err == nil {
			mods = policy.EnabledModules(p)
		}
		out = append(out, item{Policy: r, EnabledModules: mods})
	}
	c.JSON(http.StatusOK, gin.H{"items": out, "total": len(out)})
}

// Create 创建策略
func (h *PolicyHandler) Create(c *gin.Context) {
	var p policy.Policy
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := policy.Validate(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cfgJSON, err := policy.ToJSON(&p)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	row := model.Policy{
		ID:                  h.nextPolicyID(),
		Name:                p.Name,
		Description:         p.Description,
		Enabled:             false,
		Status:              "draft",
		Namespace:           p.Namespace,
		SchedulerInternalIP: p.Scheduler.InternalIP,
		SchedulerRedisPort:  p.Scheduler.RedisPort,
		SchedulerMySQLPort:  p.Scheduler.MySQLPort,
		ConfigJSON:          cfgJSON,
	}
	if row.ID > 0 {
		_ = h.DB.Unscoped().Where("id = ? AND deleted_at IS NOT NULL", row.ID).Delete(&model.Policy{}).Error
		_ = h.DB.Unscoped().Where("policy_id = ? AND deleted_at IS NOT NULL", row.ID).Delete(&model.PolicyModule{}).Error
	}
	if err := h.DB.Create(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.upsertPolicyModules(row.ID, &p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": row.ID})
}

// Get 详情
func (h *PolicyHandler) Get(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	row, err := h.findPolicy(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	p, _ := policy.ParseFromJSON(row.ConfigJSON)
	c.JSON(http.StatusOK, gin.H{
		"id":     row.ID,
		"row":    row,
		"policy": p,
	})
}

// Update 修改策略 JSON
func (h *PolicyHandler) Update(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	var p policy.Policy
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := policy.Validate(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := h.findPolicy(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	cfgJSON, _ := policy.ToJSON(&p)
	row.Name = p.Name
	row.Description = p.Description
	row.Namespace = p.Namespace
	row.SchedulerInternalIP = p.Scheduler.InternalIP
	row.SchedulerRedisPort = p.Scheduler.RedisPort
	row.SchedulerMySQLPort = p.Scheduler.MySQLPort
	row.ConfigJSON = cfgJSON
	if err := h.DB.Save(&row).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.upsertPolicyModules(row.ID, &p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Delete 删除策略(若有运行中任务,返回 409)
func (h *PolicyHandler) Delete(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	if h.hasActiveTask(id) {
		c.JSON(http.StatusConflict, gin.H{"error": "policy has active tasks"})
		return
	}
	row, err := h.findPolicy(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	p, _ := policy.ParseFromJSON(row.ConfigJSON)
	if p != nil && h.Sched != nil {
		if err := h.Sched.DeletePolicyResources(c.Request.Context(), p, row.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if err := h.deletePolicyRows(row.ID, &row); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Enable 启用策略并部署
func (h *PolicyHandler) Enable(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	row, err := h.findPolicy(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	p, err := policy.ParseFromJSON(row.ConfigJSON)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if h.Sched != nil {
		if err := h.Sched.DeployPolicy(c.Request.Context(), p, row.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	now := time.Now()
	row.Enabled = true
	row.Status = "active"
	row.LastDeployedAt = &now
	h.DB.Save(&row)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Disable 停用策略(若有运行中任务返回 409)
func (h *PolicyHandler) Disable(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	if h.hasActiveTask(id) {
		c.JSON(http.StatusConflict, gin.H{"error": "policy has active tasks"})
		return
	}
	row, err := h.findPolicy(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	p, _ := policy.ParseFromJSON(row.ConfigJSON)
	if h.Sched != nil && p != nil {
		_ = h.Sched.DisablePolicy(c.Request.Context(), p, row.ID)
	}
	row.Enabled = false
	row.Status = "inactive"
	h.DB.Save(&row)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Deploy 重新生成 ConfigMap + Deployment(滚动更新)
func (h *PolicyHandler) Deploy(c *gin.Context) {
	h.Enable(c) // Enable 本身已经做了 deploy 动作
}

// ScaleModule 调整某模块 replicas
func (h *PolicyHandler) ScaleModule(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	module := c.Param("module")
	var req struct {
		Replicas int32 `json:"replicas"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	row, err := h.findPolicy(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if h.Sched != nil {
		if err := h.Sched.ScaleModule(c.Request.Context(), row.Namespace, row.ID, module, req.Replicas); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	// 同步更新 policy_modules.replicas
	h.DB.Model(&model.PolicyModule{}).
		Where("policy_id = ? AND module = ?", row.ID, module).
		Update("replicas", req.Replicas)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Runtime 返回该策略下 Pod 运行情况
func (h *PolicyHandler) Runtime(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	row, err := h.findPolicy(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	out := gin.H{"deployments": []k8s.DeploymentStatus{}, "pods": []k8s.PodSummary{}}
	if h.K8s != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		if deps, err := h.K8s.ListPolicyDeployments(ctx, row.Namespace, fmt.Sprintf("%d", row.ID)); err == nil {
			out["deployments"] = deps
		}
		if pods, err := h.K8s.ListPolicyPods(ctx, row.Namespace); err == nil {
			out["pods"] = pods
		}
	}
	c.JSON(http.StatusOK, out)
}

// ============ 内部工具 ============

func (h *PolicyHandler) findPolicy(id uint64) (model.Policy, error) {
	var row model.Policy
	err := h.DB.First(&row, id).Error
	return row, err
}

func (h *PolicyHandler) nextPolicyID() uint64 {
	var maxID uint64
	if err := h.DB.Model(&model.Policy{}).Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
		return 0
	}
	return maxID + 1
}

func (h *PolicyHandler) deletePolicyRows(policyID uint64, row *model.Policy) error {
	return h.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Unscoped().Where("policy_id = ?", policyID).Delete(&model.PolicyModule{}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Delete(row).Error
	})
}

func (h *PolicyHandler) hasActiveTask(policyID uint64) bool {
	var c int64
	h.DB.Model(&model.Task{}).
		Where("policy_id = ? AND status IN ?", policyID,
			[]string{model.TaskStatusQueued, model.TaskStatusRunning, model.TaskStatusPaused, model.TaskStatusRestarting}).
		Count(&c)
	return c > 0
}

// upsertPolicyModules 把策略 JSON 中四个模块的部署字段与 ConfigMap 快照写入 policy_modules
func (h *PolicyHandler) upsertPolicyModules(policyID uint64, p *policy.Policy) error {
	for _, key := range []string{model.ModulePortScan, model.ModuleNmap, model.ModuleNuclei, model.ModuleWeakPass} {
		m := p.Modules[key]
		if m == nil {
			m = &policy.Module{Enabled: false}
		}
		row := model.PolicyModule{
			PolicyID:       policyID,
			Module:         key,
			Enabled:        m.Enabled,
			Image:          m.Image,
			Replicas:       m.Replicas,
			DeploymentName: policy.DeploymentName(policyID, key),
			ConfigMapName:  policy.ConfigMapName(policyID, key),
		}
		if m.Enabled {
			if cfg, err := policy.BuildModuleConfig(p, policyID, key); err == nil {
				content, _ := policy.MarshalPodConfig(cfg)
				row.ConfigJSON = content
				row.ConfigHash = policy.HashConfig(content)
			}
		}
		// upsert by (policy_id, module)
		var existing model.PolicyModule
		err := h.DB.Where("policy_id = ? AND module = ?", policyID, key).First(&existing).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := h.DB.Create(&row).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			row.ID = existing.ID
			row.CreatedAt = existing.CreatedAt
			if err := h.DB.Save(&row).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func mustParseID(c *gin.Context) uint64 {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return 0
	}
	return id
}
