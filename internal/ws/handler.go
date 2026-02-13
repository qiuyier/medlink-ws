package ws

type MessageHandler interface {
	HandleMessage(conn *Connection, data []byte) error
}
