package model

import (
	"time"

	"gorm.io/gorm"
)

// 任务/分片/模块状态常量
const (
	TaskStatusQueued     = "queued"
	TaskStatusRunning    = "running"
	TaskStatusPaused     = "paused"
	TaskStatusRestarting = "restarting"
	TaskStatusCompleted  = "completed"
	TaskStatusTerminated = "terminated"
	TaskStatusFailed     = "failed"

	PartStatusPending   = "pending"
	PartStatusRunning   = "running"
	PartStatusCompleted = "completed"
	PartStatusFailed    = "failed"
)

// 模块 key 常量(全局唯一,用于 Stream/Group/ConfigMap/Deployment 命名以及表字段)
const (
	ModulePortScan = "portscan"
	ModuleNmap     = "nmap"
	ModuleNuclei   = "nuclei"
	ModuleWeakPass = "weakpass"
)

// User 登录账号(MVP 阶段密码明文存储,方便直接在 DB 里手工新增/改密)
type User struct {
	ID        uint64         `gorm:"primaryKey" json:"id"`
	Username  string         `gorm:"size:64;uniqueIndex;not null" json:"username"`
	Password  string         `gorm:"size:255;not null" json:"-"`
	Role      string         `gorm:"size:32;default:admin" json:"role"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// Policy 策略主表
type Policy struct {
	ID                  uint64         `gorm:"primaryKey" json:"id"`
	Name                string         `gorm:"size:64;not null" json:"name"`
	Description         string         `gorm:"type:text" json:"description"`
	Enabled             bool           `gorm:"default:false" json:"enabled"`
	Status              string         `gorm:"size:32;default:draft" json:"status"` // draft/active/inactive/error
	Namespace           string         `gorm:"size:64;default:dast-system" json:"namespace"`
	SchedulerInternalIP string         `gorm:"size:64" json:"scheduler_internal_ip"`
	SchedulerRedisPort  int            `gorm:"default:6379" json:"scheduler_redis_port"`
	SchedulerMySQLPort  int            `gorm:"default:3306" json:"scheduler_mysql_port"`
	ConfigJSON          string         `gorm:"type:longtext" json:"config_json"`
	LastDeployedAt      *time.Time     `json:"last_deployed_at"`
	CreatedAt           time.Time      `json:"created_at"`
	UpdatedAt           time.Time      `json:"updated_at"`
	DeletedAt           gorm.DeletedAt `gorm:"index" json:"-"`
}

// PolicyModule 策略模块配置(策略中四个模块的镜像/replicas/ConfigMap 快照)
type PolicyModule struct {
	ID             uint64         `gorm:"primaryKey" json:"id"`
	PolicyID       uint64         `gorm:"index;not null" json:"policy_id"`
	Module         string         `gorm:"size:32;not null" json:"module"` // portscan/nmap/nuclei/weakpass
	Enabled        bool           `gorm:"default:false" json:"enabled"`
	Image          string         `gorm:"size:255" json:"image"`
	Replicas       int            `gorm:"default:1" json:"replicas"`
	ConfigJSON     string         `gorm:"type:longtext" json:"config_json"` // ConfigMap 内容快照(不含 image/replicas)
	ConfigHash     string         `gorm:"size:64" json:"config_hash"`
	DeploymentName string         `gorm:"size:128" json:"deployment_name"`
	ConfigMapName  string         `gorm:"size:128" json:"configmap_name"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

// Task 任务主表
type Task struct {
	ID          uint64         `gorm:"primaryKey" json:"id"`
	Name        string         `gorm:"size:128;not null" json:"name"`
	PolicyID    uint64         `gorm:"index;not null" json:"policy_id"`
	Status      string         `gorm:"size:32;default:queued" json:"status"`
	TargetTotal int            `json:"target_total"`
	AliveTotal  int            `json:"alive_total"`
	PartTotal   int            `json:"part_total"`
	TargetsJSON string         `gorm:"type:longtext" json:"targets_json"`
	SubmittedBy string         `gorm:"size:64" json:"submitted_by"`
	StartedAt   *time.Time     `json:"started_at"`
	FinishedAt  *time.Time     `json:"finished_at"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TaskPartsProgress 任务分片进度表;四个模块状态 NULL 表示该策略不要求该模块。
// 注意:`PortScanStatus` 和 `WeakPassStatus` 字段名含驼峰大写边界,
// GORM 默认 snake_case 会拆成 `port_scan_status`/`weak_pass_status`,
// 但全工程的 SQL(scheduler.go/task.go/Module mysqldb)都按 `portscan_status`/`weakpass_status` 硬编码,
// 这里必须显式声明 column 名,否则 1054 Unknown column。
type TaskPartsProgress struct {
	ID             uint64         `gorm:"primaryKey" json:"id"`
	TaskID         uint64         `gorm:"index;not null;uniqueIndex:uniq_task_part" json:"task_id"`
	TaskPartName   string         `gorm:"size:64;not null;uniqueIndex:uniq_task_part" json:"task_part_name"`
	PartIndex      int            `json:"part_index"`
	HostsJSON      string         `gorm:"type:longtext" json:"hosts_json"`
	HostCount      int            `json:"host_count"`
	Status         string         `gorm:"size:32;default:pending" json:"status"`
	PortScanStatus *string        `gorm:"column:portscan_status;size:32" json:"portscan_status"`
	NmapStatus     *string        `gorm:"column:nmap_status;size:32" json:"nmap_status"`
	NucleiStatus   *string        `gorm:"column:nuclei_status;size:32" json:"nuclei_status"`
	WeakPassStatus *string        `gorm:"column:weakpass_status;size:32" json:"weakpass_status"`
	Error          string         `gorm:"type:text" json:"error"`
	CompletedAt    *time.Time     `json:"completed_at"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

func (TaskPartsProgress) TableName() string {
	return "task_parts_progress"
}

// PortResult 端口扫描开放端口结果
type PortResult struct {
	ID           uint64    `gorm:"primaryKey" json:"id"`
	TaskID       uint64    `gorm:"index" json:"task_id"`
	PolicyID     uint64    `gorm:"index" json:"policy_id"`
	TaskPartName string    `gorm:"size:64;index" json:"task_part_name"`
	Host         string    `gorm:"size:128;index" json:"host"`
	Port         int       `gorm:"index" json:"port"`
	Protocol     string    `gorm:"size:16;default:tcp" json:"protocol"`
	State        string    `gorm:"size:16;default:open" json:"state"`
	CreatedAt    time.Time `json:"created_at"`
}

// ServiceResult nmap 服务识别结果
type ServiceResult struct {
	ID           uint64    `gorm:"primaryKey" json:"id"`
	TaskID       uint64    `gorm:"index" json:"task_id"`
	TaskPartName string    `gorm:"size:64;index" json:"task_part_name"`
	Host         string    `gorm:"size:128;index" json:"host"`
	Port         int       `gorm:"index" json:"port"`
	Protocol     string    `gorm:"size:16;default:tcp" json:"protocol"`
	State        string    `gorm:"size:16;default:open" json:"state"`
	Service      string    `gorm:"size:64;index" json:"service"`
	Product      string    `gorm:"size:128" json:"product"`
	Version      string    `gorm:"size:128" json:"version"`
	CreatedAt    time.Time `json:"created_at"`
}

// Vulnerability Nuclei 漏洞结果
type Vulnerability struct {
	ID           uint64    `gorm:"primaryKey" json:"id"`
	TaskID       uint64    `gorm:"index" json:"task_id"`
	TaskPartName string    `gorm:"size:64;index" json:"task_part_name"`
	Host         string    `gorm:"size:128;index" json:"host"`
	Port         int       `json:"port"`
	Matched      string    `gorm:"size:512" json:"matched"`
	TemplateID   string    `gorm:"size:128;index" json:"template_id"`
	Name         string    `gorm:"size:255" json:"name"`
	Severity     string    `gorm:"size:32;index" json:"severity"`
	Tags         string    `gorm:"type:text" json:"tags"`
	Request      string    `gorm:"type:longtext" json:"request"`
	Response     string    `gorm:"type:longtext" json:"response"`
	RawEventJSON string    `gorm:"type:longtext" json:"raw_event_json"`
	CreatedAt    time.Time `json:"created_at"`
}

// WeakPasswordFinding 弱口令爆破命中
type WeakPasswordFinding struct {
	ID           uint64    `gorm:"primaryKey" json:"id"`
	TaskID       uint64    `gorm:"index" json:"task_id"`
	TaskPartName string    `gorm:"size:64;index" json:"task_part_name"`
	Host         string    `gorm:"size:128;index" json:"host"`
	Port         int       `gorm:"index" json:"port"`
	Service      string    `gorm:"size:32;index" json:"service"`
	Username     string    `gorm:"size:128" json:"username"`
	Password     string    `gorm:"size:255" json:"password"`
	CreatedAt    time.Time `json:"created_at"`
}

// TaskEvent 任务事件,用于前端时间线
type TaskEvent struct {
	ID        uint64    `gorm:"primaryKey" json:"id"`
	TaskID    uint64    `gorm:"index" json:"task_id"`
	Level     string    `gorm:"size:16" json:"level"`
	Module    string    `gorm:"size:32" json:"module"`
	Message   string    `gorm:"type:text" json:"message"`
	MetaJSON  string    `gorm:"type:longtext" json:"meta_json"`
	CreatedAt time.Time `json:"created_at"`
}

// AllModels 返回需要 AutoMigrate 的全部模型,按依赖顺序排列。
func AllModels() []interface{} {
	return []interface{}{
		&User{},
		&Policy{}, &PolicyModule{},
		&Task{}, &TaskPartsProgress{},
		&PortResult{}, &ServiceResult{},
		&Vulnerability{}, &WeakPasswordFinding{},
		&TaskEvent{},
	}
}
