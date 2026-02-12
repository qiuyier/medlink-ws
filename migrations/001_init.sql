-- 创建数据库
-- CREATE DATABASE medical_ws;

-- 消息表
CREATE TABLE IF NOT EXISTS messages (
                                        id BIGSERIAL PRIMARY KEY,
                                        msg_id VARCHAR(64) UNIQUE NOT NULL,
    from_user_id VARCHAR(64) NOT NULL,
    to_user_id VARCHAR(64) NOT NULL,
    biz_type VARCHAR(32) NOT NULL,  -- prescription_audit, chat, system
    priority INT DEFAULT 2,  -- 0:紧急, 1:高, 2:普通, 3:低
    content JSONB NOT NULL,
    status VARCHAR(16) DEFAULT 'pending',  -- pending, delivered, read, failed
    created_at TIMESTAMP DEFAULT NOW(),
    delivered_at TIMESTAMP,
    read_at TIMESTAMP,

    INDEX idx_msg_id (msg_id),
    INDEX idx_to_user_status (to_user_id, status, created_at),
    INDEX idx_from_user (from_user_id, created_at),
    INDEX idx_created_at (created_at),
    INDEX idx_status_priority (status, priority, created_at)
    );

-- 分区优化（可选，数据量大时使用）
-- 按月分区
-- CREATE TABLE messages_2024_01 PARTITION OF messages
-- FOR VALUES FROM ('2024-01-01') TO ('2024-02-01');

-- 离线消息表
CREATE TABLE IF NOT EXISTS offline_messages (
                                                id BIGSERIAL PRIMARY KEY,
                                                user_id VARCHAR(64) NOT NULL,
    msg_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMP DEFAULT NOW(),

    UNIQUE(user_id, msg_id),
    INDEX idx_user_created (user_id, created_at)
    );

-- WebSocket 会话表（审计日志）
CREATE TABLE IF NOT EXISTS ws_sessions (
                                           session_id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(64) NOT NULL,
    device_id VARCHAR(64) NOT NULL,
    role VARCHAR(16) NOT NULL,  -- doctor, patient
    connect_time TIMESTAMP NOT NULL,
    disconnect_time TIMESTAMP,
    disconnect_reason VARCHAR(128),
    server_instance VARCHAR(128),

    INDEX idx_user_connect (user_id, connect_time),
    INDEX idx_session_time (connect_time, disconnect_time)
    );

-- 用户表（示例，实际应该在用户服务中）
CREATE TABLE IF NOT EXISTS users (
                                     user_id VARCHAR(64) PRIMARY KEY,
    username VARCHAR(64) UNIQUE NOT NULL,
    password_hash VARCHAR(256) NOT NULL,
    role VARCHAR(16) NOT NULL,  -- doctor, patient, admin
    dept_id VARCHAR(64),  -- 科室ID（医生）
    real_name VARCHAR(64),
    phone VARCHAR(32),
    email VARCHAR(128),
    status VARCHAR(16) DEFAULT 'active',  -- active, inactive, banned
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),

    INDEX idx_username (username),
    INDEX idx_role (role),
    INDEX idx_dept (dept_id)
    );

-- 处方审核记录表（示例）
CREATE TABLE IF NOT EXISTS prescription_audits (
                                                   id BIGSERIAL PRIMARY KEY,
                                                   prescription_id VARCHAR(64) NOT NULL,
    doctor_id VARCHAR(64) NOT NULL,
    patient_id VARCHAR(64) NOT NULL,
    audit_status VARCHAR(16) DEFAULT 'pending',  -- pending, approved, rejected
    audit_result JSONB,
    auditor_id VARCHAR(64),
    audited_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW(),

    INDEX idx_prescription (prescription_id),
    INDEX idx_doctor (doctor_id, created_at),
    INDEX idx_status (audit_status, created_at)
    );

-- 聊天会话表
CREATE TABLE IF NOT EXISTS chat_sessions (
                                             session_id VARCHAR(64) PRIMARY KEY,
    doctor_id VARCHAR(64) NOT NULL,
    patient_id VARCHAR(64) NOT NULL,
    status VARCHAR(16) DEFAULT 'active',  -- active, closed
    created_at TIMESTAMP DEFAULT NOW(),
    closed_at TIMESTAMP,

    INDEX idx_doctor (doctor_id, status),
    INDEX idx_patient (patient_id, status)
    );

-- 添加注释
COMMENT ON TABLE messages IS '消息表';
COMMENT ON COLUMN messages.priority IS '消息优先级: 0紧急 1高 2普通 3低';
COMMENT ON TABLE offline_messages IS '离线消息队列';
COMMENT ON TABLE ws_sessions IS 'WebSocket连接会话审计日志';

-- 创建清理历史数据的函数（可选）
CREATE OR REPLACE FUNCTION clean_old_messages() RETURNS void AS $$
BEGIN
    -- 删除 30 天前的已读消息
DELETE FROM messages
WHERE status = 'read'
  AND read_at < NOW() - INTERVAL '30 days';

-- 删除 7 天前的会话日志
DELETE FROM ws_sessions
WHERE disconnect_time < NOW() - INTERVAL '7 days';
END;
$$ LANGUAGE plpgsql;

-- 创建定时清理任务（需要 pg_cron 扩展）
-- CREATE EXTENSION IF NOT EXISTS pg_cron;
-- SELECT cron.schedule('clean-old-data', '0 2 * * *', 'SELECT clean_old_messages()');