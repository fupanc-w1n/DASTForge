package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"dast/internal/model"
)

// Policy 表示前后端共享的完整策略 JSON。包含基础字段、Scheduler 连接信息、
// runtime 锁/PEL 参数、modules 四个模块的部署字段和运行字段。
type Policy struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Enabled     bool               `json:"enabled"`
	Namespace   string             `json:"namespace"`
	Scheduler   Scheduler          `json:"scheduler"`
	Runtime     Runtime            `json:"runtime"`
	Modules     map[string]*Module `json:"modules"`
}

// Scheduler Pod 启动连接字段
type Scheduler struct {
	InternalIP string `json:"internal_ip"`
	RedisPort  int    `json:"redis_port"`
	MySQLPort  int    `json:"mysql_port"`
}

// Runtime 调度层/Worker 共享运行参数
type Runtime struct {
	TargetBatchSize    int `json:"target_batch_size"`
	LockTTLSeconds     int `json:"lock_ttl_seconds"`
	LockRenewSeconds   int `json:"lock_renew_seconds"`
	PendingIdleSeconds int `json:"pending_idle_seconds"`
}

// Module 单模块配置:部署字段 + 运行字段(运行字段类型按模块不同)
type Module struct {
	Enabled  bool   `json:"enabled"`
	Image    string `json:"image"`
	Replicas int    `json:"replicas"`

	// portscan
	Ports []string `json:"ports,omitempty"`

	// 共用:portscan / nmap / nuclei / weakpass
	QPS int `json:"qps,omitempty"`

	// nuclei
	TemplateIDs []string `json:"template_ids,omitempty"`

	// weakpass: { ssh: {username:[], password:[]}, mysql:..., redis:... }
	Dictionary map[string]ServiceDict `json:"dictionary,omitempty"`
}

// ServiceDict 弱口令字典(按服务划分)
type ServiceDict struct {
	Username []string `json:"username"`
	Password []string `json:"password"`
}

// DefaultPolicy 返回前端新建策略时使用的默认模板。
// 该模板对应 02-FRONTEND-PREDEV.md §5/§7.3 的字段约定,以及 01-BACKEND-PREDEV.md §5。
func DefaultPolicy() *Policy {
	return &Policy{
		Name:        "full-scan-default",
		Description: "默认全量扫描策略",
		Enabled:     true,
		Namespace:   "dast-system",
		Scheduler: Scheduler{
			InternalIP: "10.0.0.1",
			RedisPort:  6379,
			MySQLPort:  3306,
		},
		Runtime: Runtime{
			TargetBatchSize:    10,
			LockTTLSeconds:     360,
			LockRenewSeconds:   300,
			PendingIdleSeconds: 120,
		},
		Modules: map[string]*Module{
			model.ModulePortScan: {
				Enabled:  true,
				Image:    "ghcr.io/<owner>/dast-port-scanner:latest",
				Replicas: 3,
				Ports:    []string{"1-65535"},
				QPS:      10,
			},
			model.ModuleNmap: {
				Enabled:  true,
				Image:    "ghcr.io/<owner>/dast-nmap:latest",
				Replicas: 2,
				QPS:      150,
			},
			model.ModuleNuclei: {
				Enabled:     true,
				Image:       "ghcr.io/<owner>/dast-nuclei:latest",
				Replicas:    3,
				QPS:         200,
				TemplateIDs: []string{},
			},
			model.ModuleWeakPass: {
				Enabled:  true,
				Image:    "ghcr.io/<owner>/dast-weakpass:latest",
				Replicas: 2,
				QPS:      1,
				Dictionary: map[string]ServiceDict{
					"ssh": {
						Username: []string{"root", "admin", "ubuntu", "user", "test", "guest"},
						Password: []string{"", "root", "toor", "123456", "password", "admin", "admin123", "ubuntu", "user", "test", "guest"},
					},
					"mysql": {
						Username: []string{"root", "admin", "mysql", "test"},
						Password: []string{"", "root", "toor", "123456", "password", "admin", "mysql", "test"},
					},
					"redis": {
						Username: []string{""},
						Password: []string{"", "redis", "123456", "password", "admin"},
					},
				},
			},
		},
	}
}

// Validate 检查策略字段完整性与模块依赖关系。
// 依赖规则(架构 §8 / 后端 §5):
//   - 启用 nmap 必须启用 portscan
//   - 启用 nuclei/weakpass 必须启用 portscan 与 nmap
//   - 所有启用模块必须填写 image 与 replicas
func Validate(p *Policy) error {
	if p == nil {
		return errors.New("policy is nil")
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name required")
	}
	if p.Namespace == "" {
		p.Namespace = "dast-system"
	}
	if p.Scheduler.InternalIP == "" {
		return errors.New("scheduler.internal_ip required")
	}
	if p.Scheduler.RedisPort == 0 {
		p.Scheduler.RedisPort = 6379
	}
	if p.Scheduler.MySQLPort == 0 {
		p.Scheduler.MySQLPort = 3306
	}
	if p.Runtime.TargetBatchSize <= 0 || p.Runtime.TargetBatchSize > 10 {
		p.Runtime.TargetBatchSize = 10
	}
	if p.Runtime.LockTTLSeconds <= 0 {
		p.Runtime.LockTTLSeconds = 360
	}
	if p.Runtime.LockRenewSeconds <= 0 {
		p.Runtime.LockRenewSeconds = 300
	}
	if p.Runtime.LockRenewSeconds >= p.Runtime.LockTTLSeconds {
		p.Runtime.LockRenewSeconds = p.Runtime.LockTTLSeconds / 2
		if p.Runtime.LockRenewSeconds <= 0 {
			p.Runtime.LockRenewSeconds = 1
		}
	}
	if p.Runtime.PendingIdleSeconds <= 0 {
		p.Runtime.PendingIdleSeconds = 120
	}
	if p.Modules == nil {
		return errors.New("modules required")
	}

	if err := validateModule(p, model.ModulePortScan); err != nil {
		return err
	}
	if err := validateModule(p, model.ModuleNmap); err != nil {
		return err
	}
	if err := validateModule(p, model.ModuleNuclei); err != nil {
		return err
	}
	if err := validateModule(p, model.ModuleWeakPass); err != nil {
		return err
	}

	port := moduleEnabled(p, model.ModulePortScan)
	nmap := moduleEnabled(p, model.ModuleNmap)
	nuclei := moduleEnabled(p, model.ModuleNuclei)
	weak := moduleEnabled(p, model.ModuleWeakPass)

	if nmap && !port {
		return errors.New("enable nmap requires portscan")
	}
	if nuclei && (!port || !nmap) {
		return errors.New("enable nuclei requires portscan and nmap")
	}
	if weak && (!port || !nmap) {
		return errors.New("enable weakpass requires portscan and nmap")
	}

	// 端口范围校验
	if port {
		if err := validatePorts(p.Modules[model.ModulePortScan].Ports); err != nil {
			return err
		}
	}
	return nil
}

func validateModule(p *Policy, key string) error {
	m, ok := p.Modules[key]
	if !ok || m == nil {
		// 缺失视为未启用,补一个空对象,便于上层判断
		p.Modules[key] = &Module{Enabled: false}
		return nil
	}
	if !m.Enabled {
		return nil
	}
	if strings.TrimSpace(m.Image) == "" {
		return fmt.Errorf("module %s: image required", key)
	}
	if m.Replicas < 0 {
		return fmt.Errorf("module %s: replicas must >= 0", key)
	}
	if m.QPS <= 0 {
		return fmt.Errorf("module %s: qps must > 0", key)
	}
	return nil
}

func moduleEnabled(p *Policy, key string) bool {
	if m, ok := p.Modules[key]; ok && m != nil {
		return m.Enabled
	}
	return false
}

func validatePorts(ports []string) error {
	if len(ports) == 0 {
		return errors.New("portscan ports required")
	}
	for _, raw := range ports {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		segs := strings.Split(raw, ",")
		for _, seg := range segs {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				continue
			}
			if strings.Contains(seg, "-") {
				parts := strings.Split(seg, "-")
				if len(parts) != 2 {
					return fmt.Errorf("invalid port range: %s", seg)
				}
				lo, err1 := strconv.Atoi(parts[0])
				hi, err2 := strconv.Atoi(parts[1])
				if err1 != nil || err2 != nil {
					return fmt.Errorf("invalid port range: %s", seg)
				}
				if lo < 1 || hi > 65535 || lo > hi {
					return fmt.Errorf("invalid port range: %s", seg)
				}
			} else {
				n, err := strconv.Atoi(seg)
				if err != nil || n < 1 || n > 65535 {
					return fmt.Errorf("invalid port: %s", seg)
				}
			}
		}
	}
	return nil
}

// Downstream 单个直接下游元数据。Worker 据此判断有没有目标时能不能投递。
type Downstream struct {
	Module string `json:"module"`
	Stream string `json:"stream"`
}

// Workflow 写入模块 ConfigMap 的 workflow 子段
type Workflow struct {
	Downstreams map[string]Downstream `json:"downstreams"`
}

// BuildWorkflow 严格按 §8/§7 生成每个模块的 workflow.downstreams。
// 返回 map[moduleKey]Workflow。
func BuildWorkflow(p *Policy, policyID uint64) map[string]Workflow {
	res := map[string]Workflow{
		model.ModulePortScan: {Downstreams: map[string]Downstream{}},
		model.ModuleNmap:     {Downstreams: map[string]Downstream{}},
		model.ModuleNuclei:   {Downstreams: map[string]Downstream{}},
		model.ModuleWeakPass: {Downstreams: map[string]Downstream{}},
	}

	if moduleEnabled(p, model.ModuleNmap) {
		res[model.ModulePortScan] = Workflow{
			Downstreams: map[string]Downstream{
				"open_port": {
					Module: model.ModuleNmap,
					Stream: StreamName(policyID, model.ModuleNmap),
				},
			},
		}
	}

	nmapDS := map[string]Downstream{}
	if moduleEnabled(p, model.ModuleNuclei) {
		nmapDS["http"] = Downstream{
			Module: model.ModuleNuclei,
			Stream: StreamName(policyID, model.ModuleNuclei),
		}
	}
	if moduleEnabled(p, model.ModuleWeakPass) {
		nmapDS["weakpass"] = Downstream{
			Module: model.ModuleWeakPass,
			Stream: StreamName(policyID, model.ModuleWeakPass),
		}
	}
	res[model.ModuleNmap] = Workflow{Downstreams: nmapDS}

	// Nuclei / WeakPass 为终点模块,downstreams 固定为空。
	return res
}

// ============ 资源命名工具(架构 §4) ============

// StreamName dast:policy:{id}:{module}
func StreamName(policyID uint64, moduleKey string) string {
	return fmt.Sprintf("dast:policy:%d:%s", policyID, moduleKey)
}

// GroupName group:policy:{id}:{module}
func GroupName(policyID uint64, moduleKey string) string {
	return fmt.Sprintf("group:policy:%d:%s", policyID, moduleKey)
}

// ControlStreamName dast:policy:{id}:control
func ControlStreamName(policyID uint64) string {
	return fmt.Sprintf("dast:policy:%d:control", policyID)
}

// ConfigMapName dast-policy-{id}-{module}-config
func ConfigMapName(policyID uint64, moduleKey string) string {
	return fmt.Sprintf("dast-policy-%d-%s-config", policyID, moduleKey)
}

// DeploymentName dast-policy-{id}-{module}
func DeploymentName(policyID uint64, moduleKey string) string {
	return fmt.Sprintf("dast-policy-%d-%s", policyID, moduleKey)
}

// ============ ConfigMap 启动配置生成(架构 §8.2) ============

// PodConfig 写入 ConfigMap Data["config.json"] 的内容。
// 不包含 image/replicas,这两个字段只用于 Deployment。
type PodConfig struct {
	PolicyID     uint64                 `json:"policy_id"`
	Module       string                 `json:"module"`
	Scheduler    Scheduler              `json:"scheduler"`
	Redis        PodRedisConfig         `json:"redis"`
	Workflow     Workflow               `json:"workflow"`
	ModuleConfig map[string]interface{} `json:"module_config"`
}

// PodRedisConfig Pod 启动时读取的 Redis 相关参数
type PodRedisConfig struct {
	Stream             string `json:"stream"`
	Group              string `json:"group"`
	ControlStream      string `json:"control_stream"`
	ControlBroadcast   bool   `json:"control_broadcast"`
	LockTTLSeconds     int    `json:"lock_ttl_seconds"`
	LockRenewSeconds   int    `json:"lock_renew_seconds"`
	PendingIdleSeconds int    `json:"pending_idle_seconds"`
}

// BuildModuleConfig 抽取某个模块的 Pod 启动配置。policyID 必须使用数据库自增主键。
// 返回值已是 JSON 可序列化的结构体,调用方再 MarshalIndent。
func BuildModuleConfig(p *Policy, policyID uint64, moduleKey string) (*PodConfig, error) {
	m, ok := p.Modules[moduleKey]
	if !ok || m == nil {
		return nil, fmt.Errorf("module %s not found in policy", moduleKey)
	}
	flows := BuildWorkflow(p, policyID)
	cfg := &PodConfig{
		PolicyID:  policyID,
		Module:    moduleKey,
		Scheduler: p.Scheduler,
		Redis: PodRedisConfig{
			Stream:             StreamName(policyID, moduleKey),
			Group:              GroupName(policyID, moduleKey),
			ControlStream:      ControlStreamName(policyID),
			ControlBroadcast:   true,
			LockTTLSeconds:     p.Runtime.LockTTLSeconds,
			LockRenewSeconds:   p.Runtime.LockRenewSeconds,
			PendingIdleSeconds: p.Runtime.PendingIdleSeconds,
		},
		Workflow:     flows[moduleKey],
		ModuleConfig: extractModuleConfig(moduleKey, m),
	}
	return cfg, nil
}

func extractModuleConfig(moduleKey string, m *Module) map[string]interface{} {
	mc := map[string]interface{}{}
	switch moduleKey {
	case model.ModulePortScan:
		mc["ports"] = m.Ports
		mc["qps"] = m.QPS
	case model.ModuleNmap:
		mc["qps"] = m.QPS
	case model.ModuleNuclei:
		mc["qps"] = m.QPS
		templateIDs := m.TemplateIDs
		if templateIDs == nil {
			templateIDs = []string{}
		}
		mc["template_ids"] = templateIDs
	case model.ModuleWeakPass:
		mc["qps"] = m.QPS
		mc["dictionary"] = m.Dictionary
	}
	return mc
}

// MarshalPodConfig 把 PodConfig 序列化为带缩进的 JSON 字符串,作为 ConfigMap Data。
func MarshalPodConfig(cfg *PodConfig) (string, error) {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// HashConfig 对 ConfigMap 内容求 SHA256,用作 Deployment annotation `dast/config-hash` 以触发滚动更新。
func HashConfig(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// EnabledModules 返回当前策略启用的模块 key 列表(固定顺序)。
func EnabledModules(p *Policy) []string {
	order := []string{model.ModulePortScan, model.ModuleNmap, model.ModuleNuclei, model.ModuleWeakPass}
	out := make([]string, 0, 4)
	for _, k := range order {
		if moduleEnabled(p, k) {
			out = append(out, k)
		}
	}
	return out
}

// ParseFromJSON 反序列化策略 JSON。
func ParseFromJSON(raw string) (*Policy, error) {
	if raw == "" {
		return nil, errors.New("empty policy json")
	}
	var p Policy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// ToJSON 把策略序列化为 JSON 字符串,写入 policies.config_json。
func ToJSON(p *Policy) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
