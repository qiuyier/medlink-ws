package ws

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"

	cmap "github.com/orcaman/concurrent-map/v2"
	"github.com/qiuyier/medlink-ws/internal/consts"
	"go.uber.org/zap"
)

var (
	ErrUserOffline      = errors.New("user offline")
	ErrTooManyConns     = errors.New("too many connections for this user")
	ErrConnectionExists = errors.New("connection already exists")
)

type UserConnections struct {
	Conns map[string]*Connection
	mu    sync.RWMutex
}

type ConnectionManager struct {
	connMap cmap.ConcurrentMap[string, *UserConnections]

	// 统计
	totalConns   atomic.Int64
	doctorConns  atomic.Int64
	patientConns atomic.Int64

	// 配置
	maxConnPerUser int

	logger *zap.Logger
}

func NewConnectionManager(maxConnPerUser int, logger *zap.Logger) *ConnectionManager {
	return &ConnectionManager{
		connMap:        cmap.New[*UserConnections](),
		maxConnPerUser: maxConnPerUser,
		logger:         logger,
	}
}

// AddConnection 添加连接
func (cm *ConnectionManager) AddConnection(conn *Connection) error {
	userConns, _ := cm.connMap.Get(conn.UserID)
	if userConns == nil {
		userConns = &UserConnections{
			Conns: make(map[string]*Connection),
		}
		cm.connMap.Set(conn.UserID, userConns)
	}

	userConns.mu.Lock()
	defer userConns.mu.Unlock()

	// 检查连接数限制
	if len(userConns.Conns) >= cm.maxConnPerUser {
		return ErrTooManyConns
	}

	// 检查是否已存在（同一设备重复连接）
	if _, exists := userConns.Conns[conn.DeviceID]; exists {
		return ErrConnectionExists
	}

	userConns.Conns[conn.DeviceID] = conn

	// 更新统计
	cm.totalConns.Add(1)

	if conn.Role == consts.DoctorRole {
		cm.doctorConns.Add(1)
	} else if conn.Role == consts.PatientRole {
		cm.patientConns.Add(1)
	}

	cm.logger.Info("connection added",
		zap.String("user_id", conn.UserID),
		zap.String("device_id", conn.DeviceID),
		zap.String("role", conn.Role),
		zap.Int64("total", cm.totalConns.Load()),
	)

	return nil
}

// RemoveConnection 移除连接
func (cm *ConnectionManager) RemoveConnection(userID, deviceID string) {
	userConns, ok := cm.connMap.Get(userID)
	if !ok {
		return
	}

	userConns.mu.Lock()

	conn, exists := userConns.Conns[deviceID]
	if exists {
		delete(userConns.Conns, deviceID)

		// 更新统计
		cm.totalConns.Add(-1)
		if conn.Role == consts.DoctorRole {
			cm.doctorConns.Add(-1)
		} else if conn.Role == consts.PatientRole {
			cm.patientConns.Add(-1)
		}

		cm.logger.Info("connection removed",
			zap.String("user_id", userID),
			zap.String("device_id", deviceID),
			zap.Int64("total", cm.totalConns.Load()),
		)
	}

	isEmpty := len(userConns.Conns) == 0
	userConns.mu.Unlock()

	// 如果用户所有连接都断开，删除用户记录
	if isEmpty {
		cm.connMap.Remove(userID)
	}
}

// SendToUser 发送给指定用户的所有设备
func (cm *ConnectionManager) SendToUser(userID string, data []byte) error {
	userConns, ok := cm.connMap.Get(userID)
	if !ok {
		return ErrUserOffline
	}

	userConns.mu.RLock()
	defer userConns.mu.RUnlock()

	sendCount := 0

	for _, conn := range userConns.Conns {
		if conn.Send(data) {
			sendCount++
		}
	}

	if sendCount == 0 {
		return ErrUserOffline
	}

	return nil
}

// SendToDevice 发送给指定用户指定设备
func (cm *ConnectionManager) SendToDevice(userID, deviceID string, data []byte) error {
	userConns, ok := cm.connMap.Get(userID)
	if !ok {
		return ErrUserOffline
	}

	userConns.mu.RLock()
	conn, exists := userConns.Conns[deviceID]
	userConns.mu.RUnlock()

	if !exists {
		return ErrUserOffline
	}

	if !conn.Send(data) {
		return errors.New("send failed")
	}
	return nil
}

// Broadcast 广播给所有连接
func (cm *ConnectionManager) Broadcast(data []byte, filter func(connection *Connection) bool) {
	cm.connMap.IterCb(func(userID string, userConns *UserConnections) {
		userConns.mu.RLock()
		defer userConns.mu.RUnlock()

		for _, conn := range userConns.Conns {
			if filter == nil || filter(conn) {
				conn.Send(data)
			}
		}
	})
}

// BroadcastToDoctors 广播给所有医生
func (cm *ConnectionManager) BroadcastToDoctors(data []byte) {
	cm.Broadcast(data, func(conn *Connection) bool {
		return conn.Role == consts.DoctorRole
	})
}

// BroadcastToDepartment 广播给指定科室的医生
func (cm *ConnectionManager) BroadcastToDepartment(deptID string, data []byte) {
	cm.Broadcast(data, func(conn *Connection) bool {
		return conn.Role == consts.DoctorRole && conn.DeptID == deptID
	})
}

// BroadcastToUsers 广播给指定的多个用户
func (cm *ConnectionManager) BroadcastToUsers(userIDs []string, data []byte) int {
	sentCount := 0

	for _, userID := range userIDs {
		if err := cm.SendToUser(userID, data); err == nil {
			sentCount++
		}
	}

	cm.logger.Info("broadcast to users",
		zap.Int("target_count", len(userIDs)),
		zap.Int("sent_count", sentCount),
	)

	return sentCount
}

// BroadcastToDoctorList 广播给指定的多个医生（通过医生ID）
func (cm *ConnectionManager) BroadcastToDoctorList(doctorIDs []string, data []byte) int {
	sentCount := 0

	for _, doctorID := range doctorIDs {
		userConns, ok := cm.connMap.Get(doctorID)
		if !ok {
			continue
		}

		userConns.mu.RLock()
		for _, conn := range userConns.Conns {
			if conn.Role == consts.DoctorRole {
				if conn.Send(data) {
					sentCount++
				}
			}
		}
		userConns.mu.RUnlock()
	}

	cm.logger.Info("broadcast to doctor list",
		zap.Int("target_count", len(doctorIDs)),
		zap.Int("sent_count", sentCount),
	)

	return sentCount
}

// GetOnlineDoctors 获取在线医生列表
func (cm *ConnectionManager) GetOnlineDoctors() []map[string]any {
	doctors := make([]map[string]any, 0)
	seen := make(map[string]bool) // 去重

	cm.connMap.IterCb(func(userID string, userConns *UserConnections) {
		userConns.mu.RLock()
		defer userConns.mu.RUnlock()

		for _, conn := range userConns.Conns {
			if conn.Role == consts.DoctorRole && !seen[conn.UserID] {
				seen[conn.UserID] = true
				doctors = append(doctors, map[string]any{
					"user_id":      conn.UserID,
					"dept_id":      conn.DeptID,
					"device_count": len(userConns.Conns),
					"last_active":  conn.GetLastActive(),
				})
			}
		}
	})

	return doctors
}

// GetDepartmentOnlineCount 获取指定科室的在线医生数量
func (cm *ConnectionManager) GetDepartmentOnlineCount(deptID string) int {
	count := 0
	seen := make(map[string]bool)

	cm.connMap.IterCb(func(userID string, userConns *UserConnections) {
		userConns.mu.RLock()
		defer userConns.mu.RUnlock()

		for _, conn := range userConns.Conns {
			if conn.Role == consts.DoctorRole && conn.DeptID == deptID && !seen[conn.UserID] {
				seen[conn.UserID] = true
				count++
			}
		}
	})

	return count
}

// GetUserConnections 获取用户的所有连接
func (cm *ConnectionManager) GetUserConnections(userID string) []*Connection {
	userConns, ok := cm.connMap.Get(userID)
	if !ok {
		return nil
	}

	userConns.mu.RLock()
	defer userConns.mu.RUnlock()

	conns := make([]*Connection, 0, len(userConns.Conns))
	for _, conn := range userConns.Conns {
		conns = append(conns, conn)
	}

	return conns
}

// IsUserOnline 判断用户是否在线
func (cm *ConnectionManager) IsUserOnline(userID string) bool {
	userConn, ok := cm.connMap.Get(userID)
	if !ok {
		return false
	}

	userConn.mu.RLock()
	defer userConn.mu.RUnlock()

	return len(userConn.Conns) > 0
}

// GetStats 获取在线统计
func (cm *ConnectionManager) GetStats() map[string]int64 {
	return map[string]int64{
		"total":   cm.totalConns.Load(),
		"doctor":  cm.doctorConns.Load(),
		"patient": cm.patientConns.Load(),
	}
}

func (cm *ConnectionManager) KickoutUser(userID string, reason string) {
	userConns, ok := cm.connMap.Get(userID)
	if !ok {
		return
	}

	userConns.mu.RLock()
	conns := make([]*Connection, 0, len(userConns.Conns))
	for _, conn := range userConns.Conns {
		conns = append(conns, conn)
	}
	userConns.mu.RUnlock()

	// 发送踢出消息
	kickMsg, _ := NewWSMessage(MessageTypeKickout, &ErrorPayload{
		Code:    consts.ErrorCodeKickout,
		Message: reason,
	})
	kickData, _ := json.Marshal(kickMsg)

	for _, conn := range conns {
		conn.Send(kickData)
		conn.Close()
	}
}
