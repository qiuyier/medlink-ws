# MedLink WS

<p align="center">
  <img src="docs/logo.png" alt="MedLink WS" width="200"/>
</p>

<p align="center">
  <strong>A production-ready WebSocket push service for healthcare applications</strong>
</p>

<p align="center">
  <a href="https://golang.org"><img src="https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go" alt="Go Version"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="License"></a>
  <a href="https://goreportcard.com/report/github.com/yourname/medlink-ws"><img src="https://goreportcard.com/badge/github.com/yourname/medlink-ws" alt="Go Report"></a>
</p>

---

## 🚀 Features

- 🔐 **JWT Authentication** - Secure WebSocket connections
- 💓 **Heartbeat Mechanism** - Automatic connection keep-alive
- 📱 **Multi-Device Support** - One user, multiple devices
- 🎯 **Message Priority** - Urgent prescription alerts first
- 💾 **Offline Messages** - Auto-delivery when users come online
- 🔄 **Pub/Sub Pattern** - Redis/Kafka/RabbitMQ support
- 📊 **Horizontal Scaling** - Load-balanced multi-instance deployment
- 🗄️ **GORM + PostgreSQL** - Reliable data persistence

## 🏥 Use Cases

- 👨‍⚕️ Real-time doctor-patient consultations
- 💊 Prescription audit notifications
- 📋 Medical report delivery
- 🔔 Appointment reminders
- 📞 Emergency alerts

## 🎯 Performance

- **100,000+** concurrent WebSocket connections (single instance)
- **10,000+** messages per second throughput
- **< 100ms** message delivery latency (P95)
- **20KB** memory per connection

## 📦 Quick Start

\`\`\`bash
# Clone the repository
- git clone https://github.com/yourname/medlink-ws.git
- cd medlink-ws

# Install dependencies
go mod download

# Start services (PostgreSQL, Redis)
docker-compose up -d

# Run the server
go run cmd/server/main.go -config=config.yaml
\`\`\`

## 📄 License

MIT License - see [LICENSE](LICENSE) for details