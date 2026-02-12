#!/bin/bash

# 等待 Kafka 启动
echo "等待 Kafka 启动..."
sleep 10

# 创建业务主题
docker exec medlink-kafka kafka-topics --bootstrap-server localhost:9092 \
  --create --if-not-exists \
  --topic prescription_audit \
  --partitions 3 \
  --replication-factor 1

docker exec medlink-kafka kafka-topics --bootstrap-server localhost:9092 \
  --create --if-not-exists \
  --topic chat \
  --partitions 3 \
  --replication-factor 1

docker exec medlink-kafka kafka-topics --bootstrap-server localhost:9092 \
  --create --if-not-exists \
  --topic system \
  --partitions 3 \
  --replication-factor 1

docker exec medlink-kafka kafka-topics --bootstrap-server localhost:9092 \
  --create --if-not-exists \
  --topic online_status \
  --partitions 3 \
  --replication-factor 1

echo "Kafka Topics 创建完成！"
docker exec medlink-kafka kafka-topics --bootstrap-server localhost:9092 --list