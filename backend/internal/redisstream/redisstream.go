package redisstream

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// BusinessMessage 业务 Stream 上的固定消息体:task_id + task_part_name + hosts。
// 不允许携带端口、模板、字典等大字段。
type BusinessMessage struct {
	TaskID       uint64   `json:"task_id"`
	TaskPartName string   `json:"task_part_name"`
	Hosts        []string `json:"hosts"`
}

// ControlMessage 控制流消息体
type ControlMessage struct {
	EventID   string `json:"event_id"`
	TaskID    uint64 `json:"task_id"`
	PolicyID  uint64 `json:"policy_id"`
	Action    string `json:"action"` // pause|resume|terminate
	CreatedAt int64  `json:"created_at"`
}

// Manager 包装 go-redis,并提供 Stream/Consumer Group/分布式锁等高阶能力。
type Manager struct {
	client *redis.Client
}

// New 直连 Redis,沿用 demo/redis分布式锁.md 的连接方式。
func New(addr, password string, db int) (*Manager, error) {
	c := redis.NewClient(&redis.Options{Addr: addr, Password: password, DB: db})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return &Manager{client: c}, nil
}

// Client 暴露底层 client(供少量需直接调用的场景使用)
func (m *Manager) Client() *redis.Client { return m.client }

// Close 关闭连接
func (m *Manager) Close() error { return m.client.Close() }

// EnsureGroup 创建业务 Stream 的 Consumer Group。已存在则忽略。
// 控制流不使用 Consumer Group,因此这里只为业务 Stream 调用。
func (m *Manager) EnsureGroup(ctx context.Context, stream, group string) error {
	err := m.client.XGroupCreateMkStream(ctx, stream, group, "0").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return err
	}
	return nil
}

// EnsureStream 创建普通 Stream key。Redis 没有空 Stream 创建命令,因此写入一条 init
// 消息作为占位;控制流监听以 "$" 启动,不会回放这条历史消息。
func (m *Manager) EnsureStream(ctx context.Context, stream string, values map[string]interface{}) error {
	if values == nil {
		values = map[string]interface{}{"event": "__init__", "created_at": time.Now().UnixMilli()}
	}
	if ok, err := m.client.Exists(ctx, stream).Result(); err != nil {
		return err
	} else if ok > 0 {
		return nil
	}
	return m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		Values: values,
	}).Err()
}

// DeleteKeys 删除一组 Redis key。不存在的 key 会被 Redis 忽略。
func (m *Manager) DeleteKeys(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return m.client.Del(ctx, keys...).Err()
}

// DeleteByPattern 使用 SCAN 批量删除匹配 key。删除策略时用于清理该策略业务
// Stream 派生出来的分布式锁,避免误删其他策略资源。
func (m *Manager) DeleteByPattern(ctx context.Context, pattern string) error {
	var cursor uint64
	for {
		keys, next, err := m.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := m.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

// XAddBusiness 向业务 Stream 推送一条消息。
// Stream 消息只包含架构 §7 定义的三个字段:task_id / task_part_name / hosts(JSON 编码的字符串数组)。
func (m *Manager) XAddBusiness(ctx context.Context, stream string, msg BusinessMessage) (string, error) {
	hostsJSON, err := json.Marshal(msg.Hosts)
	if err != nil {
		return "", err
	}
	return m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		Values: map[string]interface{}{
			"task_id":        msg.TaskID,
			"task_part_name": msg.TaskPartName,
			"hosts":          string(hostsJSON),
		},
	}).Result()
}

// XReadBusinessGroup 通过 Consumer Group 阻塞读取业务消息。
// 返回的 XMessage 中 Values 含 task_id/task_part_name/hosts。
func (m *Manager) XReadBusinessGroup(ctx context.Context, stream, group, consumer string, count int64, block time.Duration) ([]redis.XMessage, error) {
	streams, err := m.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, ">"},
		Count:    count,
		Block:    block,
	}).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}
	if len(streams) == 0 {
		return nil, nil
	}
	return streams[0].Messages, nil
}

// XAck 确认消息
func (m *Manager) XAck(ctx context.Context, stream, group, msgID string) error {
	return m.client.XAck(ctx, stream, group, msgID).Err()
}

// XPendingExt 列出消费者组待 ACK 消息(供 PEL 恢复使用)
func (m *Manager) XPendingExt(ctx context.Context, stream, group string, count int64) ([]redis.XPendingExt, error) {
	return m.client.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: stream,
		Group:  group,
		Start:  "-",
		End:    "+",
		Count:  count,
	}).Result()
}

// XClaim 抢占未 ACK 的消息到当前 consumer 的 PEL
func (m *Manager) XClaim(ctx context.Context, stream, group, consumer string, minIdle time.Duration, ids ...string) ([]redis.XMessage, error) {
	return m.client.XClaim(ctx, &redis.XClaimArgs{
		Stream:   stream,
		Group:    group,
		Consumer: consumer,
		MinIdle:  minIdle,
		Messages: ids,
	}).Result()
}

// XLen 返回 Stream 长度
func (m *Manager) XLen(ctx context.Context, stream string) (int64, error) {
	return m.client.XLen(ctx, stream).Result()
}

// XAddControl 向控制流推送广播消息。控制流不用 Consumer Group。
func (m *Manager) XAddControl(ctx context.Context, controlStream string, msg ControlMessage) (string, error) {
	if msg.CreatedAt == 0 {
		msg.CreatedAt = time.Now().UnixMilli()
	}
	return m.client.XAdd(ctx, &redis.XAddArgs{
		Stream: controlStream,
		Values: map[string]interface{}{
			"event_id":   msg.EventID,
			"task_id":    msg.TaskID,
			"policy_id":  msg.PolicyID,
			"action":     msg.Action,
			"created_at": msg.CreatedAt,
		},
	}).Result()
}

// XReadControl 控制流广播读取。lastID 由调用方维护,初始可用 "$"。
func (m *Manager) XReadControl(ctx context.Context, controlStream, lastID string, count int64, block time.Duration) ([]redis.XMessage, string, error) {
	streams, err := m.client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{controlStream, lastID},
		Count:   count,
		Block:   block,
	}).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, lastID, nil
		}
		return nil, lastID, err
	}
	if len(streams) == 0 || len(streams[0].Messages) == 0 {
		return nil, lastID, nil
	}
	msgs := streams[0].Messages
	last := msgs[len(msgs)-1].ID
	return msgs, last, nil
}

// ParseBusiness 从 XMessage 解析业务消息。架构 §7 固定三字段:
//
//	task_id / task_part_name / hosts(JSON 编码的字符串数组)
func ParseBusiness(msg redis.XMessage) (*BusinessMessage, error) {
	bm := &BusinessMessage{}
	if v, ok := msg.Values["task_id"].(string); ok {
		fmt.Sscanf(v, "%d", &bm.TaskID)
	}
	if v, ok := msg.Values["task_part_name"].(string); ok {
		bm.TaskPartName = v
	}
	if v, ok := msg.Values["hosts"].(string); ok && v != "" {
		if err := json.Unmarshal([]byte(v), &bm.Hosts); err != nil {
			return nil, fmt.Errorf("invalid hosts json: %w", err)
		}
	}
	if bm.TaskID == 0 || bm.TaskPartName == "" {
		return nil, fmt.Errorf("invalid business message: %v", msg.Values)
	}
	return bm, nil
}

// ParseControl 从 XMessage 解析控制消息
func ParseControl(msg redis.XMessage) (*ControlMessage, error) {
	cm := &ControlMessage{}
	if v, ok := msg.Values["event_id"].(string); ok {
		cm.EventID = v
	}
	if v, ok := msg.Values["action"].(string); ok {
		cm.Action = v
	}
	if v, ok := msg.Values["task_id"].(string); ok {
		fmt.Sscanf(v, "%d", &cm.TaskID)
	}
	if v, ok := msg.Values["policy_id"].(string); ok {
		fmt.Sscanf(v, "%d", &cm.PolicyID)
	}
	if v, ok := msg.Values["created_at"].(string); ok {
		fmt.Sscanf(v, "%d", &cm.CreatedAt)
	}
	if cm.Action == "" {
		return nil, fmt.Errorf("invalid control message: missing action")
	}
	return cm, nil
}
