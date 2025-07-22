# AMQP Migration to Azure go-amqp

This migration is converting the RoadRunner AMQP driver from `github.com/rabbitmq/amqp091-go` (AMQP 0.9.1) to `github.com/Azure/go-amqp` (AMQP 1.0).

## Key Differences

### Protocol Changes
- **AMQP 0.9.1 → AMQP 1.0**: These are completely different protocols that are not compatible
- **Queue/Exchange Model**: AMQP 1.0 doesn't have the same queue/exchange/binding model as AMQP 0.9.1
- **Message Structure**: Different message formats and property structures

### API Architecture Changes
- **RabbitMQ AMQP 0.9.1**: `Connection → Channel → Queue/Exchange operations`
- **Azure AMQP 1.0**: `Conn → Session → Sender/Receiver → Message operations`

### Major Code Changes Required

1. **Connection Management**: 
   - Replace `amqp.Connection` with `amqp.Conn`
   - Replace `amqp.Channel` with `amqp.Session` and `amqp.Sender`/`amqp.Receiver`

2. **Message Handling**:
   - Replace `amqp.Delivery` with `amqp.Message`
   - Different acknowledgment patterns (`receiver.AcceptMessage()` vs `delivery.Ack()`)

3. **Queue/Exchange Operations**:
   - AMQP 1.0 uses "addresses" instead of queues/exchanges
   - No direct equivalent to `QueueDeclare`, `ExchangeDeclare`, etc.

4. **Error Handling**:
   - Different error types and structures
   - No `amqp.Error` type in Azure implementation

5. **Publishing/Consuming**:
   - `PublishWithContext()` → `sender.Send()`
   - `Consume()` → `receiver.Receive()` (different patterns)

## Implementation Status

Currently implementing basic structure changes:
- ✅ Updated imports
- ✅ Updated go.mod dependencies  
- ✅ Basic type replacements
- 🔄 Connection/session management (in progress)
- ❌ Message publishing logic
- ❌ Message consuming logic
- ❌ Queue/address management
- ❌ Error handling migration
- ❌ Test updates

## Compatibility Notes

This migration will break compatibility with RabbitMQ brokers that only support AMQP 0.9.1. The new implementation will only work with AMQP 1.0 compliant brokers such as:
- Azure Service Bus
- Apache ActiveMQ Artemis
- Apache Qpid
- Red Hat AMQ

## Next Steps

This is a major architectural change that requires:
1. Complete rewrite of message handling logic
2. New configuration options for AMQP 1.0 concepts
3. Updated documentation
4. Extensive testing with AMQP 1.0 brokers
5. Migration guide for users
