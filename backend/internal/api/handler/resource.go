package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"dast/internal/k8s"
	"dast/internal/model"
	"dast/internal/redisstream"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ResourceHandler 集群资源 + 健康检查
type ResourceHandler struct {
	DB        *gorm.DB
	K8s       *k8s.Client
	Redis     *redisstream.Manager
	Namespace string
}

// Summary 集群总览
func (h *ResourceHandler) Summary(c *gin.Context) {
	out := gin.H{
		"node_total":         0,
		"pod_total":          0,
		"pod_running":        0,
		"pod_abnormal":       0,
		"policy_enabled":     int64(0),
		"redis_ok":           h.Redis != nil,
		"mysql_ok":           h.DB != nil,
	}
	if h.K8s != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		if nodes, err := h.K8s.ListNodes(ctx); err == nil {
			out["node_total"] = len(nodes)
		}
		if pods, err := h.K8s.ListPolicyPods(ctx, h.Namespace); err == nil {
			out["pod_total"] = len(pods)
			run, abn := 0, 0
			for _, p := range pods {
				if p.Phase == "Running" && p.Ready {
					run++
				} else if p.Phase == "Failed" || (p.Phase == "Pending" && p.RestartCount > 0) {
					abn++
				}
			}
			out["pod_running"] = run
			out["pod_abnormal"] = abn
		}
	}
	if h.DB != nil {
		var c2 int64
		h.DB.Model(&model.Policy{}).Where("enabled = ?", true).Count(&c2)
		out["policy_enabled"] = c2
	}
	c.JSON(http.StatusOK, out)
}

// Nodes Node 列表
func (h *ResourceHandler) Nodes(c *gin.Context) {
	if h.K8s == nil {
		c.JSON(http.StatusOK, gin.H{"items": []k8s.NodeSummary{}})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	nodes, err := h.K8s.ListNodes(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": nodes})
}

// Pods Pod 列表
func (h *ResourceHandler) Pods(c *gin.Context) {
	if h.K8s == nil {
		c.JSON(http.StatusOK, gin.H{"items": []k8s.PodSummary{}})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	pods, err := h.K8s.ListPolicyPods(ctx, h.Namespace)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": pods})
}

// PolicyPods 按策略分组的 Pod
func (h *ResourceHandler) PolicyPods(c *gin.Context) {
	id := mustParseID(c)
	if id == 0 {
		return
	}
	if h.K8s == nil {
		c.JSON(http.StatusOK, gin.H{"deployments": []k8s.DeploymentStatus{}, "pods": []k8s.PodSummary{}})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	deps, _ := h.K8s.ListPolicyDeployments(ctx, h.Namespace, idToStr(id))
	pods, _ := h.K8s.ListPolicyPods(ctx, h.Namespace)
	c.JSON(http.StatusOK, gin.H{"deployments": deps, "pods": pods})
}

// Streams 策略下 Stream 长度
func (h *ResourceHandler) Streams(c *gin.Context) {
	policyID := c.Query("policy_id")
	if policyID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "policy_id required"})
		return
	}
	modules := []string{"portscan", "nmap", "nuclei", "weakpass"}
	out := make(map[string]int64, len(modules))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	for _, m := range modules {
		stream := "dast:policy:" + policyID + ":" + m
		if h.Redis != nil {
			n, _ := h.Redis.XLen(ctx, stream)
			out[m] = n
		}
	}
	c.JSON(http.StatusOK, out)
}

// Health 健康检查
func (h *ResourceHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "time": time.Now().Format(time.RFC3339)})
}

func idToStr(id uint64) string {
	return strconv.FormatUint(id, 10)
}
