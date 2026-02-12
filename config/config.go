package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Redis    RedisConfig    `yaml:"redis"`
	Kafka    KafkaConfig    `yaml:"kafka"`
	RabbitMQ RabbitMQConfig `yaml:"rabbitmq"`
	JWT      JWTConfig      `yaml:"jwt"`
	WS       WSConfig       `yaml:"ws"`
}

type ServerConfig struct {
	HTTPPort string `yaml:"http_port"`
	WSPort   string `yaml:"ws_port"`
	Mode     string `yaml:"mode"` // debug, release
}

type DatabaseConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	DBName   string `yaml:"dbname"`
	SSLMode  string `yaml:"sslmode"`
}

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type KafkaConfig struct {
	Brokers []string `yaml:"brokers"`
	Topic   string   `yaml:"topic"`
	GroupID string   `yaml:"group_id"`
}

type RabbitMQConfig struct {
	URL      string `yaml:"url"`
	Exchange string `yaml:"exchange"`
	Queue    string `yaml:"queue"`
}

type JWTConfig struct {
	Secret     string        `yaml:"secret"`
	ExpireTime time.Duration `yaml:"expire_time"`
}

type WSConfig struct {
	ReadBufferSize   int           `yaml:"read_buffer_size"`
	WriteBufferSize  int           `yaml:"write_buffer_size"`
	HandshakeTimeout time.Duration `yaml:"handshake_timeout"`
	PingInterval     time.Duration `yaml:"ping_interval"`
	PongTimeout      time.Duration `yaml:"pong_timeout"`
	MaxMessageSize   int64         `yaml:"max_message_size"`
	SendChannelSize  int           `yaml:"send_channel_size"`
	MaxConnPerUser   int           `yaml:"max_conn_per_user"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
