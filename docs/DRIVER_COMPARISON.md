# Driver Comparison: AMQP 0.9.1 vs AMQP 1.0

This document compares the original RabbitMQ AMQP 0.9.1 driver with the new Azure AMQP 1.0 driver.

## Feature Comparison

| Feature | AMQP 0.9.1 (Original) | AMQP 1.0 (Azure) |
|---------|----------------------|------------------|
| **Protocol Version** | AMQP 0.9.1 | AMQP 1.0 |
| **Library** | `github.com/rabbitmq/amqp091-go` | `github.com/Azure/go-amqp` |
| **Driver Name** | `amqp` | `azure-amqp` |
| **Primary Broker** | RabbitMQ | Azure Service Bus |
| **Connection Model** | Connection → Channel | Connection → Session → Sender/Receiver |
| **Queue Management** | QueueDeclare, ExchangeDeclare | Address-based routing |
| **Publishing** | `PublishWithContext()` | `sender.Send()` |
| **Consuming** | `ConsumeWithContext()` | `receiver.Receive()` |
| **Acknowledgment** | `delivery.Ack()` | `receiver.AcceptMessage()` |
| **NACK/Reject** | `delivery.Nack()` | `receiver.RejectMessage()` |

## Configuration Comparison

### AMQP 0.9.1 Configuration
```yaml
jobs:
  pipelines:
    rabbitmq:
      driver: amqp
      config:
        addr: "amqp://guest:guest@localhost:5672/"
        queue: "jobs"
        exchange: "amqp.default"
        exchange_type: "direct"
        routing_key: "jobs"
        prefetch: 10
        durable: true
        auto_ack: false
        requeue_on_fail: true
```

### AMQP 1.0 Configuration
```yaml
jobs:
  pipelines:
    azure:
      driver: azure-amqp
      config:
        connection_string: "amqps://namespace.servicebus.windows.net"
        address: "jobs"
        prefetch: 10
        auto_ack: false
        requeue_on_fail: true
```

## Broker Compatibility

### AMQP 0.9.1 Driver
- ✅ **RabbitMQ** (primary)
- ✅ **Apache Qpid** (AMQP 0.9.1 mode)
- ❌ **Azure Service Bus**
- ❌ **Apache ActiveMQ Artemis** (AMQP 1.0 mode)

### AMQP 1.0 Driver
- ❌ **RabbitMQ** (no AMQP 1.0 support)
- ✅ **Azure Service Bus** (primary)
- ✅ **Apache ActiveMQ Artemis**
- ✅ **Apache Qpid Broker-J**
- ✅ **Red Hat AMQ**

## Migration Guide

### When to Use Each Driver

#### Use AMQP 0.9.1 Driver When:
- Using RabbitMQ as your message broker
- Need advanced routing with exchanges and bindings
- Require complex queue topologies
- Using existing RabbitMQ infrastructure

#### Use AMQP 1.0 Driver When:
- Using Azure Service Bus
- Need cloud-native messaging solutions
- Working with AMQP 1.0 compliant brokers
- Require enterprise messaging features

### Configuration Migration

#### From AMQP 0.9.1 to AMQP 1.0

1. **Change Driver Name**
   ```yaml
   # Before
   driver: amqp
   
   # After
   driver: azure-amqp
   ```

2. **Update Connection String**
   ```yaml
   # Before
   addr: "amqp://guest:guest@localhost:5672/"
   
   # After
   connection_string: "amqp://guest:guest@localhost:5672/"
   ```

3. **Simplify Address Configuration**
   ```yaml
   # Before
   queue: "jobs"
   exchange: "amqp.default"
   routing_key: "jobs"
   
   # After
   address: "jobs"
   ```

4. **Remove AMQP 0.9.1 Specific Options**
   ```yaml
   # These options don't exist in AMQP 1.0
   # exchange_type: "direct"
   # durable: true
   # exclusive: false
   # exchange_durable: true
   # exchange_auto_delete: false
   ```

### Code Migration

#### PHP Worker Code
The PHP worker code remains the same! Both drivers use the same RoadRunner Jobs API:

```php
<?php
// This code works with both drivers
use Spiral\RoadRunner\Jobs\Consumer;

$consumer = new Consumer();

while ($task = $consumer->waitTask()) {
    try {
        // Process task
        $payload = $task->getPayload();
        processJob($payload);
        
        // Acknowledge
        $task->ack();
    } catch (\Throwable $e) {
        // Reject/requeue
        $task->requeue($e->getMessage());
    }
}
```

## Performance Comparison

| Metric | AMQP 0.9.1 | AMQP 1.0 | Notes |
|--------|------------|----------|-------|
| **Throughput** | High | High | Both support high throughput |
| **Latency** | Low | Low | Similar latency characteristics |
| **Memory Usage** | Moderate | Moderate | AMQP 1.0 may use slightly more |
| **Connection Overhead** | Low | Moderate | AMQP 1.0 has session overhead |
| **Feature Richness** | High | Moderate | AMQP 0.9.1 has more routing features |

## Error Handling Differences

### AMQP 0.9.1 Error Types
```go
// Specific AMQP error types
if amqpErr, ok := err.(*amqp.Error); ok {
    switch amqpErr.Code {
    case 404: // NOT_FOUND
    case 406: // PRECONDITION_FAILED
    }
}
```

### AMQP 1.0 Error Types
```go
// Standard Go errors with AMQP context
if err != nil {
    // Check using errors.Is/As for specific conditions
    log.Error("AMQP error", zap.Error(err))
}
```

## Monitoring and Observability

Both drivers support:
- ✅ **OpenTelemetry Tracing**
- ✅ **Structured Logging with Zap**
- ✅ **Metrics Collection**
- ✅ **Health Checks**

### Driver-Specific Metrics

#### AMQP 0.9.1 Metrics
- Queue length
- Exchange statistics
- Channel utilization
- Connection pooling stats

#### AMQP 1.0 Metrics
- Session statistics
- Sender/receiver utilization
- Address resolution stats
- Link credit flow

## Deployment Considerations

### Docker Images

#### AMQP 0.9.1 (RabbitMQ)
```dockerfile
# RabbitMQ broker
FROM rabbitmq:3.12-management
```

#### AMQP 1.0 (ActiveMQ Artemis)
```dockerfile
# ActiveMQ Artemis broker
FROM apache/activemq-artemis:2.30.0
```

### Cloud Deployments

#### AMQP 0.9.1
- CloudAMQP
- Amazon MQ (RabbitMQ)
- Self-hosted RabbitMQ

#### AMQP 1.0
- Azure Service Bus (recommended)
- Amazon MQ (ActiveMQ)
- Google Cloud Pub/Sub (with AMQP plugin)

## Conclusion

Choose the appropriate driver based on your messaging infrastructure:

- **Stick with AMQP 0.9.1** if you're using RabbitMQ and need its advanced routing features
- **Migrate to AMQP 1.0** if you're moving to cloud-native solutions like Azure Service Bus
- **Both drivers** can coexist in the same RoadRunner deployment for hybrid scenarios
