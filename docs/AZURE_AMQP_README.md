# Azure AMQP 1.0 Driver for RoadRunner

This driver provides AMQP 1.0 support for RoadRunner Jobs using [Azure's go-amqp library](https://github.com/Azure/go-amqp).

## Features

- **AMQP 1.0 Protocol**: Full support for AMQP 1.0 specification
- **Azure Service Bus Compatibility**: Works seamlessly with Azure Service Bus
- **Multi-Broker Support**: Compatible with any AMQP 1.0 compliant broker
- **Auto/Manual Acknowledgment**: Configurable message acknowledgment modes
- **Message Requeuing**: Failed message requeuing support
- **Distributed Tracing**: OpenTelemetry tracing integration

## Supported Brokers

- **Azure Service Bus** (primary target)
- **Apache ActiveMQ Artemis**
- **Apache Qpid Broker-J**
- **Red Hat AMQ**
- Any AMQP 1.0 compliant message broker

## Configuration

### Basic Configuration

```yaml
version: "2024.1.0"

jobs:
  pipelines:
    azure-amqp:
      driver: azure-amqp
      config:
        # AMQP 1.0 connection string
        connection_string: "amqp://guest:guest@localhost:5672/"
        
        # AMQP 1.0 address (replaces queue/exchange concept)
        address: "jobs"
        
        # Number of messages to prefetch
        prefetch: 10
        
        # Message priority
        priority: 10
        
        # Auto-acknowledge messages (set to false for manual ack)
        auto_ack: false
        
        # Requeue messages on failure
        requeue_on_fail: true
```

### Azure Service Bus Configuration

```yaml
jobs:
  pipelines:
    azure-service-bus:
      driver: azure-amqp
      config:
        # Azure Service Bus connection string with SAS token
        connection_string: "amqps://your-namespace.servicebus.windows.net"
        
        # Queue or topic name
        address: "your-queue-name"
        
        prefetch: 20
        priority: 5
        auto_ack: false
        requeue_on_fail: true
```

### Apache ActiveMQ Artemis Configuration

```yaml
jobs:
  pipelines:
    artemis:
      driver: azure-amqp
      config:
        connection_string: "amqp://admin:admin@localhost:5672/"
        address: "jobs.queue"
        prefetch: 15
        auto_ack: false
```

## Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `connection_string` | string | **required** | AMQP 1.0 connection string |
| `address` | string | **required** | Target address (queue/topic name) |
| `prefetch` | int | `10` | Number of messages to prefetch |
| `priority` | int | `0` | Default message priority |
| `auto_ack` | bool | `false` | Automatically acknowledge messages |
| `requeue_on_fail` | bool | `true` | Requeue messages on processing failure |

## AMQP 1.0 vs AMQP 0.9.1 Differences

### Conceptual Differences

| AMQP 0.9.1 (RabbitMQ) | AMQP 1.0 (Azure) |
|----------------------|------------------|
| Exchange + Queue + Routing Key | Address |
| Channel | Session + Sender/Receiver |
| `delivery.Ack()` | `receiver.AcceptMessage()` |
| `delivery.Nack()` | `receiver.RejectMessage()` |
| Queue Declaration | Address Resolution |

### Migration Considerations

1. **Address Mapping**: Replace exchange/queue combinations with single addresses
2. **No Queue Declaration**: AMQP 1.0 doesn't support dynamic queue creation
3. **Different Error Types**: Error handling patterns are different
4. **Session Management**: Connection -> Session -> Sender/Receiver pattern

## Usage Examples

### Sending Jobs

```php
<?php
// PHP worker example
use Spiral\RoadRunner\Jobs\Jobs;
use Spiral\RoadRunner\Jobs\Options;

$jobs = new Jobs();

// Send job to Azure AMQP
$jobs->push('azure-amqp', 'process-order', [
    'order_id' => 12345,
    'customer' => 'john@example.com'
], new Options(
    delay: 0,
    priority: 10
));
```

### Processing Jobs

```php
<?php
// Worker script
use Spiral\RoadRunner\Jobs\Consumer;
use Spiral\RoadRunner\Jobs\Task;

$consumer = new Consumer();

while ($task = $consumer->waitTask()) {
    try {
        $payload = $task->getPayload();
        
        // Process the job
        processOrder($payload['order_id'], $payload['customer']);
        
        // Acknowledge successful processing
        $task->ack();
        
    } catch (\Throwable $e) {
        // Reject and requeue on failure
        $task->requeue($e->getMessage());
    }
}
```

## Connection Strings

### Local AMQP 1.0 Broker
```
amqp://username:password@localhost:5672/
```

### Azure Service Bus with Connection String
```
amqps://your-namespace.servicebus.windows.net
```

### Azure Service Bus with SAS Token
```
amqps://your-namespace.servicebus.windows.net?amqp:sasl-mechanisms=PLAIN&amqp:auth=your-sas-token
```

### Secure Connection (TLS)
```
amqps://username:password@broker.example.com:5671/
```

## Error Handling

The driver provides robust error handling with automatic reconnection:

- **Connection Failures**: Automatic reconnection with exponential backoff
- **Message Processing Errors**: Configurable requeue or reject behavior
- **Session Errors**: Automatic session recreation
- **Timeout Handling**: Configurable receive timeouts

## Monitoring and Observability

### Metrics
- Message send/receive rates
- Error rates and types
- Connection status
- Queue depths (broker-dependent)

### Tracing
- OpenTelemetry integration
- Distributed tracing across message boundaries
- Span creation for send/receive operations

### Logging
- Structured logging with zap
- Debug information for connection events
- Error logging with context

## Performance Considerations

### Prefetch Settings
- Higher prefetch = better throughput, more memory usage
- Lower prefetch = better load distribution, lower memory usage
- Recommended: 10-50 for most workloads

### Connection Pooling
- Use connection pooling for high-throughput scenarios
- Single connection can handle multiple sessions
- Consider broker connection limits

### Message Batching
- AMQP 1.0 supports message batching for improved throughput
- Configure based on latency vs. throughput requirements

## Troubleshooting

### Common Issues

1. **Connection Refused**
   - Check broker is running and accessible
   - Verify connection string format
   - Check firewall and network connectivity

2. **Authentication Failures**
   - Verify credentials in connection string
   - Check SAS token expiration (Azure Service Bus)
   - Ensure user has proper permissions

3. **Address Not Found**
   - Verify queue/topic exists on broker
   - Check address naming conventions
   - Some brokers auto-create addresses, others don't

4. **Message Loss**
   - Disable `auto_ack` for critical messages
   - Implement proper error handling
   - Use message persistence settings

### Debug Mode

Enable debug logging to troubleshoot issues:

```yaml
logs:
  level: debug
  
jobs:
  pipelines:
    azure-amqp:
      driver: azure-amqp
      config:
        # ... your config
```

## Contributing

To contribute to the Azure AMQP driver:

1. Ensure you have access to an AMQP 1.0 broker for testing
2. Run integration tests with: `go test -tags=integration`
3. Follow the existing code patterns and error handling
4. Add appropriate logging and metrics
5. Update documentation for any new features

## License

This driver is part of the RoadRunner project and follows the same MIT license.
